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

	"github.com/dop251/goja"
	repl "github.com/solidarity-ai/repl"
	storemem "github.com/solidarity-ai/repl/store/mem"
	replsqlite "github.com/solidarity-ai/repl/store/sqlite"
	"github.com/solidarity-ai/toolbox/codemodesdks"
	"github.com/solidarity-ai/toolbox/toolset"
)

const manifestID = "toolbox-codemode-session-ts-v1"

const (
	SuperToolName             = "super_tool"
	TypeScriptCellSourceParam = "typescript_cell_source"
)

type storeCloser interface {
	Close() error
}

type submissionFrame struct {
	original             string
	submitted            string
	wrappedObjectLiteral bool
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
	prepared    toolset.PreparedToolset
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
	deps := sessionDeps(currentDir, st, func() toolset.PreparedToolset { return cfg.PreparedTools })
	sess, resumed, err := openOrStartSQLiteSession(ctx, st, deps)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	return &Session{
		session:     sess,
		storeCloser: st,
		id:          sess.ID(),
		resumed:     resumed,
		prepared:    cfg.PreparedTools,
	}, nil
}

// OpenMemory opens a new in-memory TypeScript session.
func OpenMemory(ctx context.Context, currentDir string, cfgs ...SessionConfig) (*Session, error) {
	st := storemem.New()
	cfg := firstConfig(cfgs)
	sess, err := repl.New().StartSession(ctx, repl.SessionConfig{
		Manifest: repl.Manifest{ID: manifestID},
	}, sessionDeps(currentDir, st, func() toolset.PreparedToolset { return cfg.PreparedTools }))
	if err != nil {
		return nil, fmt.Errorf("start session: %w", err)
	}

	return &Session{
		session:  sess,
		id:       sess.ID(),
		prepared: cfg.PreparedTools,
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prepared = prepared
}

// Instructions describes how to interact with the codemode session.
func (s *Session) Instructions() string {
	var prepared toolset.PreparedToolset
	if s != nil {
		s.mu.Lock()
		prepared = s.prepared
		s.mu.Unlock()
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s submits a code cell to a REPL. The sandboxed environment has installed tools which connect to outside systems.\n", SuperToolName)
	fmt.Fprintf(&b, "%s is much more efficient and effective than regular tool calling and should be used when ever possible.\n", SuperToolName)
	fmt.Fprintln(&b, "===")
	fmt.Fprintln(&b, "// REPL input")
	fmt.Fprintln(&b, `Object.entries($pkgMetadata).map(([name, meta]) => [`)
	fmt.Fprintln(&b, "  name, meta.toolCount, (meta.useWhenHint || \"\")")
	fmt.Fprintln(&b, "])")
	fmt.Fprintln(&b, "// REPL output ")
	var names []string
	var rows []string
	for _, row := range summarizePreparedTools(prepared) {
		rows = append(rows, fmt.Sprintf("[%q, %d, \"%s\"]", row.Name, row.ToolCount, row.UseWhenHint))
		names = append(names, row.Name)
	}
	fmt.Fprintln(&b, "[", strings.Join(rows, ", "), "]")
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "Note:")
	fmt.Fprintln(&b, "`console.log(inspect(<last expression>))` is automatically added if console.log is NOT in the source.")
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "REPL Env:")
	fmt.Fprintln(&b, "// last cell value")
	fmt.Fprintln(&b, "$last : any")
	fmt.Fprintln(&b, "// value for a prior cell index")
	fmt.Fprintln(&b, "$val(index : number) : any")
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
  /* .d.ts for package */
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

	frame := newSubmissionFrame(tsSource)

	res, err := s.session.SubmitCell(ctx, repl.SubmitInput{
		Source:   frame.submitted,
		Language: repl.CellLanguageTypeScript,
	})
	res = frame.translateSubmitResult(res)
	if err != nil {
		err = frame.translateSubmitError(err)
		return formatSubmitError(res, err)
	}
	return formatSubmitResult(ctx, s.session, res)
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

func rewriteTopLevelObjectLiteral(trimmed string) (string, bool) {
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return "", false
	}
	wrapped := "(" + trimmed + ")"
	if _, err := goja.Parse("cell.ts", wrapped); err != nil {
		return "", false
	}
	return wrapped, true
}

func newSubmissionFrame(tsSource string) submissionFrame {
	frame := submissionFrame{
		original:  tsSource,
		submitted: tsSource,
	}
	if rewritten, ok := rewriteTopLevelObjectLiteral(strings.TrimSpace(tsSource)); ok {
		frame.submitted = rewritten
		frame.wrappedObjectLiteral = true
	}
	return frame
}

func (f submissionFrame) translateSubmitResult(res repl.SubmitResult) repl.SubmitResult {
	if !f.wrappedObjectLiteral || len(res.Diagnostics) == 0 {
		return res
	}
	res.Diagnostics = translateDiagnosticsToOriginalSource(res.Diagnostics)
	return res
}

func (f submissionFrame) translateSubmitError(err error) error {
	if !f.wrappedObjectLiteral || err == nil {
		return err
	}
	var checkErr *repl.SubmitCheckFailure
	if !errors.As(err, &checkErr) {
		return err
	}
	copyErr := *checkErr
	copyErr.Diagnostics = translateDiagnosticsToOriginalSource(checkErr.Diagnostics)
	return &copyErr
}

func translateDiagnosticsToOriginalSource(in []repl.Diagnostic) []repl.Diagnostic {
	out := make([]repl.Diagnostic, len(in))
	copy(out, in)
	for i := range out {
		if out[i].Line == 1 && out[i].Column > 1 {
			out[i].Column--
		}
	}
	return out
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
