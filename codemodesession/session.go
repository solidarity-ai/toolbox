package codemodesession

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	repl "github.com/mackross/repljs"
	storemem "github.com/mackross/repljs/store/mem"
	replsqlite "github.com/mackross/repljs/store/sqlite"
	"github.com/solidarity-ai/toolbox/codemodesdks"
	"github.com/solidarity-ai/toolbox/daemon"
	"github.com/solidarity-ai/toolbox/toolset"
)

const manifestID = "toolbox-codemode-session-ts-v1"

const (
	SuperToolName             = "super_tool"
	TypeScriptCellSourceParam = "typescript_cell_source"
	TimeoutSecsParam          = "timeout_secs"
)

var DefaultSubmitTimeout = 30 * time.Second

type storeCloser interface {
	Close() error
}

type preparedState struct {
	mu       sync.RWMutex
	prepared toolset.PreparedToolset
}

func newPreparedState(prepared toolset.PreparedToolset) *preparedState {
	return &preparedState{prepared: prepared}
}

func (s *preparedState) Get() toolset.PreparedToolset {
	if s == nil {
		return toolset.PreparedToolset{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prepared
}

func (s *preparedState) Set(prepared toolset.PreparedToolset) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prepared = prepared
}

// SessionConfig controls codemode session startup behavior.
type SessionConfig struct {
	PreparedTools toolset.PreparedToolset
}

// Session is the shared TypeScript submit boundary used by the CLI repl and
// codemode MCP surface.
type Session struct {
	mu          sync.Mutex
	session     repl.Session
	storeCloser storeCloser
	id          repl.SessionID
	resumed     bool
	prepared    *preparedState
	applied     toolset.PreparedToolset
	submitting  atomic.Bool
	preparedSeq atomic.Uint64
	appliedSeq  uint64
}

// OpenSQLite opens or resumes a persistent TypeScript session backed by SQLite.
func OpenSQLite(ctx context.Context, sqlitePath, currentDir string, cfgs ...SessionConfig) (*Session, error) {
	sqlitePath = strings.TrimSpace(sqlitePath)
	if sqlitePath == "" {
		sqlitePath = ".toolbox-session"
	}
	if !filepath.IsAbs(sqlitePath) && currentDir != "" {
		sqlitePath = filepath.Join(currentDir, sqlitePath)
	}
	if err := os.MkdirAll(filepath.Dir(sqlitePath), 0o755); err != nil {
		return nil, fmt.Errorf("create session storage dir: %w", err)
	}

	st, err := replsqlite.Open(ctx, sqlitePath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite store: %w", err)
	}

	cfg := firstConfig(cfgs)
	prepared := newPreparedState(cfg.PreparedTools)
	deps := sessionDeps(currentDir, st, prepared.Get)
	sess, resumed, err := openOrStartSQLiteSession(ctx, st, deps)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	if err := transitionSessionPreparedTools(ctx, sess, prepared.Get()); err != nil {
		_ = sess.Close()
		_ = st.Close()
		return nil, fmt.Errorf("apply prepared tools: %w", err)
	}
	return &Session{
		session:     sess,
		storeCloser: st,
		id:          sess.ID(),
		resumed:     resumed,
		prepared:    prepared,
		applied:     prepared.Get(),
	}, nil
}

// OpenMemory opens a new in-memory TypeScript session.
func OpenMemory(ctx context.Context, currentDir string, cfgs ...SessionConfig) (*Session, error) {
	st := storemem.New()
	cfg := firstConfig(cfgs)
	prepared := newPreparedState(cfg.PreparedTools)
	sess, err := repl.New().StartSession(ctx, repl.SessionConfig{
		Manifest: repl.Manifest{ID: manifestID},
	}, sessionDeps(currentDir, st, prepared.Get))
	if err != nil {
		return nil, fmt.Errorf("start session: %w", err)
	}

	return &Session{
		session:  sess,
		id:       sess.ID(),
		prepared: prepared,
		applied:  prepared.Get(),
	}, nil
}

func firstConfig(cfgs []SessionConfig) SessionConfig {
	if len(cfgs) == 0 {
		return SessionConfig{}
	}
	return cfgs[0]
}

func sessionDeps(currentDir string, st repl.Store, prepared func() toolset.PreparedToolset) repl.SessionDeps {
	return repl.SessionDeps{
		Store:             st,
		RuntimeMode:       repl.RuntimeModePersistent,
		VMDelegate:        newRuntimeDelegate(prepared),
		TypeScriptFactory: repl.NewTypeScriptFactory(),
		TypeScriptEnvProvider: func(_ context.Context, _ repl.TypeScriptEnvContext) (repl.TypeScriptEnv, error) {
			return typeScriptEnv(currentDir, prepared()), nil
		},
	}
}

func transitionSessionPreparedTools(ctx context.Context, sess repl.Session, prepared toolset.PreparedToolset) error {
	if sess == nil {
		return nil
	}
	state, err := runtimeStateJSON(prepared)
	if err != nil {
		return err
	}
	return sess.TransitionToState(ctx, state)
}

func openOrStartSQLiteSession(ctx context.Context, st *replsqlite.Store, deps repl.SessionDeps) (repl.Session, bool, error) {
	eng := repl.New()
	sessionID, err := st.LatestSessionID(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("load latest session: %w", err)
	}
	if sessionID != "" {
		sess, err := eng.OpenSession(ctx, sessionID, deps)
		if err != nil {
			return nil, false, fmt.Errorf("open session: %w", err)
		}
		return sess, true, nil
	}
	sess, err := eng.StartSession(ctx, repl.SessionConfig{
		Manifest: repl.Manifest{ID: manifestID},
	}, deps)
	if err != nil {
		return nil, false, fmt.Errorf("start session: %w", err)
	}
	return sess, false, nil
}

// ID returns the stable session identifier.
func (s *Session) ID() string {
	if s == nil {
		return ""
	}
	return string(s.id)
}

// Resumed reports whether the session reopened prior durable state.
func (s *Session) Resumed() bool {
	if s == nil {
		return false
	}
	return s.resumed
}

// SetPreparedTools stores the current prepared toolset for later use.
func (s *Session) SetPreparedTools(prepared toolset.PreparedToolset) {
	if s == nil {
		return
	}
	if s.submitting.Load() {
		if s.prepared == nil {
			s.mu.Lock()
			if s.prepared == nil {
				s.prepared = newPreparedState(prepared)
			}
			s.mu.Unlock()
		}
		if s.prepared != nil {
			s.prepared.Set(prepared)
		}
		s.preparedSeq.Add(1)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.setPreparedLocked(prepared)
	s.applyPendingPreparedToolsLocked()
}

// Instructions describes how to interact with the codemode session.
func (s *Session) Instructions() string {
	var prepared toolset.PreparedToolset
	if s != nil {
		s.mu.Lock()
		if s.prepared != nil {
			prepared = s.prepared.Get()
		}
		s.mu.Unlock()
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s submits a code cell to a notebook like environment. The notebook has globally installed tools which connect to outside systems.\n", SuperToolName)
	fmt.Fprintf(&b, "%s is much more efficient and effective than regular tool calling and should be used when ever possible.\n", SuperToolName)
	fmt.Fprintln(&b, "Important Notebook Usage Information:")
	fmt.Fprintln(&b, "- `console.log(inspect($last))` is automatically added when console.log is NOT in the source.")
	fmt.Fprintln(&b, "- Variables and state persist between cells. Promises must settle before the timeout or the cell will error.")
	fmt.Fprintln(&b, "	- Redeclaring const, let, classes, or functions with the same name in later cells causes an error (use var or leave global).")
	fmt.Fprintln(&b, "  - For long cells use unique variable names")
	fmt.Fprintln(&b, "  - For small cells use it'ss easier to use $last / $val(cell_index) to reuse prior results.")
	fmt.Fprintln(&b, "  - Cells ending with console.log, return undefined — end with the variable if you need to reference it later.")
	fmt.Fprintln(&b, "- There are no imports")
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "===")
	fmt.Fprintln(&b, "// Notebook Input")
	fmt.Fprintln(&b, `Object.entries($pkgMetadata).map(([name, meta]) => [`)
	fmt.Fprintln(&b, "  name, meta.toolCount, (meta.useWhenHint || \"\")")
	fmt.Fprintln(&b, "])")
	fmt.Fprintln(&b, "// Notebook Output ")
	var names []string
	var rows []string
	for _, row := range summarizePreparedTools(prepared) {
		rows = append(rows, fmt.Sprintf("[%q, %d, \"%s\"]", row.Name, row.ToolCount, row.UseWhenHint))
		names = append(names, row.Name)
	}
	fmt.Fprintln(&b, "[", strings.Join(rows, ", "), "]")
	fmt.Fprintln(&b, "===")
	fmt.Fprintln(&b, "")
	if lockedPackages := lockedOmittedPackageNames(prepared); len(lockedPackages) > 0 {
		fmt.Fprintf(&b, "Some tools are unavailable because the toolbox secret store is locked: %s.\n", strings.Join(lockedPackages, ", "))
		fmt.Fprintf(&b, "Unlock Toolbox at %s/ and reload to restore them.\n", daemon.BaseURL())
		fmt.Fprintln(&b, "")
	}
	fmt.Fprintln(&b, "Note:")
	fmt.Fprintln(&b, "`console.log(inspect(<last expression>))` is automatically added if console.log is NOT in the source.")
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "Notebook Env:")
	fmt.Fprintln(&b, "// value for a prior cell index")
	fmt.Fprintln(&b, "$val(index : number) : any")
	fmt.Fprintln(&b, "// $val(<last-cell>) / last value in an expression in prior cell")
	fmt.Fprintln(&b, "$last : any")
	fmt.Fprintln(&b, "// truncated object summary for inspecting data shape (limited depth and length traversal) ")
	fmt.Fprintln(&b, "inspect(x : any) : string")
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, packageDeclarationsDTS())
	fmt.Fprintln(&b, "")
	if len(names) > 0 {
		fmt.Fprintf(&b, "IMPORTANT: use %s when you want to access any of these: %s.\n", SuperToolName, strings.Join(names, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}

type toolSummaryRow struct {
	Name        string `json:"name"`
	ToolCount   int    `json:"toolCount"`
	UseWhenHint string `json:"useWhenHint,omitempty"`
	API         string `json:"api"`
}

func summarizePreparedTools(prepared toolset.PreparedToolset) []toolSummaryRow {
	grouped := map[string][]toolset.PreparedTool{}
	for _, tool := range prepared.Tools() {
		name := preparedToolPackageName(tool)
		grouped[name] = append(grouped[name], tool)
	}

	names := make([]string, 0, len(grouped))
	for name := range grouped {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := make([]toolSummaryRow, 0, len(names))
	for _, name := range names {
		pkgPrepared := prepared.FilterTools(func(tool toolset.PreparedTool) bool {
			return preparedToolPackageName(tool) == name
		})
		rows = append(rows, toolSummaryRow{
			Name:        name,
			ToolCount:   len(grouped[name]),
			UseWhenHint: packageUseWhenHint(grouped[name]),
			API:         codemodesdks.DeclarationSource(pkgPrepared),
		})
	}
	return rows
}

func preparedToolPackageName(tool toolset.PreparedTool) string {
	name := strings.TrimSpace(tool.Name)
	if tool.PackageMeta != nil {
		if pkgName := strings.TrimSpace(tool.PackageMeta.Name); pkgName != "" {
			return pkgName
		}
		if module := strings.TrimSpace(tool.PackageMeta.Module.String()); module != "" {
			return module
		}
	}
	if name == "" {
		return "<unknown>"
	}
	return name
}

func packageUseWhenHint(tools []toolset.PreparedTool) string {
	for _, tool := range tools {
		if tool.PackageMeta == nil {
			continue
		}
		if useWhenHint := strings.TrimSpace(tool.PackageMeta.UseWhenHint); useWhenHint != "" {
			return useWhenHint
		}
	}
	return ""
}

func typeScriptEnv(currentDir string, prepared toolset.PreparedToolset) repl.TypeScriptEnv {
	rows := summarizePreparedTools(prepared)
	packagesJSON, err := json.Marshal(pkgMetadataObject(rows))
	if err != nil {
		packagesJSON = []byte("{}")
	}
	epoch := checkerEpochTS(prepared, "var $pkgMetadata = "+string(packagesJSON)+" as const;")
	hashInput := currentDir + "\n" + epoch
	sum := sha256.Sum256([]byte(hashInput))
	return repl.TypeScriptEnv{
		Hash:       "toolbox-codemode-session:v2:" + hex.EncodeToString(sum[:]),
		EpochTS:    epoch,
		CurrentDir: currentDir,
	}
}

func checkerEpochTS(prepared toolset.PreparedToolset, pkgMetadataPrelude string) string {
	var parts []string
	if sdk := strings.TrimSpace(codemodesdks.DeclarationSource(prepared)); sdk != "" {
		parts = append(parts, sdk)
	}
	if prelude := strings.TrimSpace(pkgMetadataPrelude); prelude != "" {
		parts = append(parts, prelude)
	}
	return strings.Join(parts, "\n\n")
}

func packageDeclarationsDTS() string {
	return strings.TrimSpace(`
declare const $pkgMetadata: Record<string, {
  toolCount: number;
  // present when the package needs extra guidance
  useWhenHint?: string;
  /* .d.ts for package, always use console.log to view */
  api: string;
}>;
`)
}

func pkgMetadataObject(rows []toolSummaryRow) map[string]map[string]any {
	out := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		meta := map[string]any{
			"toolCount": row.ToolCount,
			"api":       row.API,
		}
		if row.UseWhenHint != "" {
			meta["useWhenHint"] = row.UseWhenHint
		}
		out[row.Name] = meta
	}
	return out
}

func lockedOmittedPackageNames(prepared toolset.PreparedToolset) []string {
	var names []string
	for _, omitted := range prepared.OmittedPackages() {
		if omitted.Reason != toolset.OmittedPackageReasonSecretStoreLocked {
			continue
		}
		names = append(names, omitted.Name)
	}
	sort.Strings(names)
	return names
}

// Submit evaluates a TypeScript cell and returns the shared REPL-formatted
// output text. Errors are rendered into that output instead of being returned.
func (s *Session) Submit(ctx context.Context, tsSource string) string {
	if s == nil || strings.TrimSpace(tsSource) == "" {
		return ""
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return formatSubmitError(repl.SubmitResult{}, errors.New("session closed"))
	}
	s.submitting.Store(true)
	defer s.submitting.Store(false)

	submitCtx, cancel := withSubmitTimeout(ctx)
	defer cancel()

	res, err := s.session.SubmitCell(submitCtx, repl.SubmitInput{
		Source:   tsSource,
		Language: repl.CellLanguageTypeScript,
	})
	s.applyPendingPreparedToolsLocked()
	if err != nil {
		return formatSubmitError(res, err)
	}
	return formatSubmitResult(ctx, s.session, res)
}

func withSubmitTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), DefaultSubmitTimeout)
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, DefaultSubmitTimeout)
}

func (s *Session) setPreparedLocked(prepared toolset.PreparedToolset) {
	if s == nil {
		return
	}
	if s.prepared == nil {
		s.prepared = newPreparedState(prepared)
	} else {
		s.prepared.Set(prepared)
	}
	s.preparedSeq.Add(1)
}

func (s *Session) applyPendingPreparedToolsLocked() {
	if s == nil || s.session == nil || s.prepared == nil {
		return
	}
	desiredSeq := s.preparedSeq.Load()
	if desiredSeq == s.appliedSeq {
		return
	}

	desired := s.prepared.Get()
	if err := transitionSessionPreparedTools(context.Background(), s.session, desired); err != nil {
		s.prepared.Set(s.applied)
		return
	}
	s.applied = desired
	s.appliedSeq = desiredSeq
}

// Close releases the session and any owned storage.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var errs []error
	if s.session != nil {
		errs = append(errs, s.session.Close())
		s.session = nil
	}
	if s.storeCloser != nil {
		errs = append(errs, s.storeCloser.Close())
		s.storeCloser = nil
	}
	return errors.Join(errs...)
}

func formatSubmitResult(ctx context.Context, sess repl.Session, res repl.SubmitResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cell %d\n", res.Index)
	if len(res.Warnings) > 0 {
		fmt.Fprintln(&b, "==")
		writeWarnings(&b, res.Warnings)
	}
	fmt.Fprintln(&b, "--")
	for _, line := range res.Log {
		fmt.Fprintln(&b, line)
	}
	if res.CompletionValue == nil {
		fmt.Fprintln(&b, "=> <nil>")
		return b.String()
	}
	fmt.Fprintf(&b, "=> %s\n", renderCompletion(ctx, sess, res.CompletionValue))
	return b.String()
}

func formatSubmitError(res repl.SubmitResult, err error) string {
	var b strings.Builder

	var submitErr *repl.SubmitFailure
	fmt.Fprintln(&b, "cell (failed to commit)")
	fmt.Fprintln(&b, "==")
	writeWarnings(&b, res.Warnings)

	var checkErr *repl.SubmitCheckFailure
	if errors.As(err, &checkErr) {
		fmt.Fprintln(&b, "failure: typecheck failed")
		writeDiagnostics(&b, checkErr.Diagnostics)
		return b.String()
	}

	if !errors.As(err, &submitErr) {
		fmt.Fprintf(&b, "failure: %s\n", humanFailureMessage(err.Error()))
		return b.String()
	}
	if submitErr.Phase != "" {
		fmt.Fprintf(&b, "failure: %s: %s\n", submitErr.Phase, submitErr.ErrorMessage)
	} else {
		fmt.Fprintf(&b, "failure: %s\n", submitErr.ErrorMessage)
	}
	writeEffects(&b, submitErr.LinkedEffects)
	if len(submitErr.Log) > 0 {
		fmt.Fprintln(&b, "--")
		for _, line := range submitErr.Log {
			fmt.Fprintln(&b, line)
		}
	}
	return b.String()
}

func writeWarnings(b *strings.Builder, warnings []repl.ReplWarning) {
	for _, warning := range warnings {
		fmt.Fprintf(b, "warn: %s\n", warning.Message)
	}
}

func writeDiagnostics(b *strings.Builder, diagnostics []repl.Diagnostic) {
	if len(diagnostics) == 0 {
		return
	}
	fmt.Fprintln(b, "diagnostics:")
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(b, "- %s\n", formatDiagnostic(diagnostic))
	}
}

func writeEffects(b *strings.Builder, effects []repl.EffectSummary) {
	if len(effects) == 0 {
		return
	}
	fmt.Fprintf(b, "side effects (%d):\n", len(effects))
	for _, effect := range effects {
		line := fmt.Sprintf("- %s [%s, %s]", effect.FunctionName, effect.Status, effect.ReplayPolicy)
		if effect.ErrorMessage != "" {
			line += ": " + effect.ErrorMessage
		}
		fmt.Fprintln(b, line)
	}
}

func renderCompletion(ctx context.Context, sess repl.Session, value *repl.ValueRef) string {
	if value == nil {
		return "<nil>"
	}
	rendered := value.Preview
	if sess != nil {
		if view, err := sess.Inspect(ctx, value.ID); err == nil {
			if strings.TrimSpace(view.Summary) != "" {
				rendered = view.Summary
			}
		}
	}
	return rendered
}

func formatDiagnostic(diagnostic repl.Diagnostic) string {
	var prefix strings.Builder
	if diagnostic.Line > 0 {
		prefix.WriteString(strconv.Itoa(diagnostic.Line))
		if diagnostic.Column > 0 {
			prefix.WriteString(":")
			prefix.WriteString(strconv.Itoa(diagnostic.Column))
		}
		prefix.WriteString(" ")
	}
	if diagnostic.Severity != "" {
		prefix.WriteString(diagnostic.Severity)
		prefix.WriteString(": ")
	}
	prefix.WriteString(diagnostic.Message)
	return prefix.String()
}

func humanFailureMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	msg = strings.TrimPrefix(msg, "submit error: ")
	msg = strings.TrimPrefix(msg, "session: Submit: ")
	return strings.TrimSpace(msg)
}
