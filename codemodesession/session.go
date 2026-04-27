package codemodesession

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	repl "github.com/mackross/repljs"
	storemem "github.com/mackross/repljs/store/mem"
	"github.com/solidarity-ai/toolbox/codemodesdks"
	"github.com/solidarity-ai/toolbox/daemon"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/toolset"
)

const manifestID = "toolbox-codemode-session-ts-v1"

const (
	SuperToolName               = "super_tool"
	AwaitSuperToolApprovalsName = "await_super_tool_approvals"
	TypeScriptCellSourceParam   = "typescript_cell_source"
	TimeoutSecsParam            = "timeout_secs"
)

var DefaultSubmitTimeout = 30 * time.Second

type storeCloser interface {
	Close() error
}

type preparedState struct {
	mu       sync.RWMutex
	prepared toolset.PreparedToolset
}

type toolRunState struct {
	mu      sync.Mutex
	current *toolRunGeneration
}

type toolRunGeneration struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func newPreparedState(prepared toolset.PreparedToolset) *preparedState {
	return &preparedState{prepared: prepared}
}

func newToolRunState() *toolRunState {
	state := &toolRunState{}
	state.Reset()
	return state
}

func (s *toolRunState) Acquire(parent context.Context) (context.Context, func()) {
	if parent == nil {
		parent = context.Background()
	}
	if s == nil {
		return parent, func() {}
	}
	s.mu.Lock()
	current := s.current
	s.mu.Unlock()
	if current == nil {
		return parent, func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(current.ctx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func (s *toolRunState) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	old := s.current
	ctx, cancel := context.WithCancel(context.Background())
	s.current = &toolRunGeneration{ctx: ctx, cancel: cancel}
	s.mu.Unlock()
	if old != nil {
		old.cancel()
	}
}

func (s *toolRunState) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	old := s.current
	s.current = nil
	s.mu.Unlock()
	if old != nil {
		old.cancel()
	}
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
	Executor      *invoke.Executor
}

// Session is the shared TypeScript submit boundary used by the CLI repl and
// codemode MCP surface.
type Session struct {
	mu              sync.Mutex
	session         repl.Session
	store           repl.Store
	storeCloser     storeCloser
	id              repl.SessionID
	tbSession       string
	intentText      string
	intentSource    string
	intentUpdatedAt time.Time
	resumed         bool
	prepared        *preparedState
	applied         toolset.PreparedToolset
	toolCalls       toolCallJournal
	approvals       approvalStore
	approvalAwaits  *approvalAwaitDelegate
	toolRuns        *toolRunState
	executor        *invoke.Executor
	ownExecutor     bool
	lease           *sessionLease
	terminalErr     error
	submitting      atomic.Bool
	preparedSeq     atomic.Uint64
	appliedSeq      uint64
}

// OpenMemory opens a new in-memory TypeScript session.
func OpenMemory(ctx context.Context, currentDir string, cfgs ...SessionConfig) (*Session, error) {
	cfg := firstConfig(cfgs)
	executor, ownExecutor := resolveExecutor(cfg.Executor, cfg.PreparedTools)

	st := storemem.New()
	toolCalls := newMemoryToolCallJournal(st)
	approvals := newMemoryApprovalStore()
	prepared := newPreparedState(cfg.PreparedTools)
	toolRuns := newToolRunState()
	sess, err := repl.New().StartSession(ctx, repl.SessionConfig{
		Manifest: repl.Manifest{ID: manifestID},
	}, sessionDeps(currentDir, st, prepared.Get, toolCalls, approvals, executor, toolRuns.Acquire))
	if err != nil {
		if ownExecutor {
			_ = executor.Close()
		}
		return nil, fmt.Errorf("start session: %w", err)
	}

	return &Session{
		session:         sess,
		store:           st,
		id:              sess.ID(),
		intentText:      defaultSessionIntent,
		intentSource:    "fallback",
		intentUpdatedAt: time.Now().UTC(),
		prepared:        prepared,
		applied:         prepared.Get(),
		toolCalls:       toolCalls,
		approvals:       approvals,
		approvalAwaits:  newApprovalAwaitDelegate(),
		toolRuns:        toolRuns,
		executor:        executor,
		ownExecutor:     ownExecutor,
	}, nil
}

func firstConfig(cfgs []SessionConfig) SessionConfig {
	if len(cfgs) == 0 {
		return SessionConfig{}
	}
	return cfgs[0]
}

func resolveExecutor(existing *invoke.Executor, prepared toolset.PreparedToolset) (*invoke.Executor, bool) {
	if existing != nil {
		return existing, false
	}
	return invoke.NewExecutor(prepared), true
}

func sessionDeps(currentDir string, st repl.Store, prepared func() toolset.PreparedToolset, toolCalls toolCallJournal, approvals approvalStore, executor *invoke.Executor, toolContext func(context.Context) (context.Context, func())) repl.SessionDeps {
	return repl.SessionDeps{
		Store:             st,
		RuntimeMode:       repl.RuntimeModePersistent,
		VMDelegate:        newRuntimeDelegate(prepared, toolCalls, approvals, executor, toolContext),
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

// ID returns the stable session identifier.
func (s *Session) ID() string {
	if s == nil {
		return ""
	}
	return string(s.id)
}

// TBSession returns the durable notebook identifier for persistent sessions.
func (s *Session) TBSession() string {
	if s == nil {
		return ""
	}
	return s.tbSession
}

func (s *Session) Intent() (text, source string, updatedAt time.Time) {
	if s == nil {
		return defaultSessionIntent, "fallback", time.Time{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	text = normalizeSessionIntent(s.intentText)
	source = strings.TrimSpace(s.intentSource)
	if source == "" {
		source = "fallback"
	}
	return text, source, s.intentUpdatedAt
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
	if s.ownExecutor && s.executor != nil {
		s.executor.SetPrepared(prepared)
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
	return s.instructionsForSurface(ToolSurfaceModeLocked, false)
}

type ToolSurfaceMode int

const (
	ToolSurfaceModeLocked ToolSurfaceMode = iota
	ToolSurfaceModeUnlocked
)

// InstructionsForMode describes how to interact with the codemode session in
// either locked or unlocked routing mode.
func (s *Session) InstructionsForMode(mode ToolSurfaceMode) string {
	return s.instructionsForSurface(mode, false)
}

// InstructionsForSurface describes how to interact with the codemode session
// for one tool surface, including whether approval waiting is available on that
// surface.
func (s *Session) InstructionsForSurface(mode ToolSurfaceMode, awaitAvailable bool) string {
	return s.instructionsForSurface(mode, awaitAvailable)
}

func (s *Session) instructionsForSurface(mode ToolSurfaceMode, awaitAvailable bool) string {
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
	fmt.Fprintf(&b, "%s is much more efficient and effective than regular tool calling and should be used whenever possible.\n", SuperToolName)
	fmt.Fprintln(&b, "Important Notebook Usage Information:")
	fmt.Fprintln(&b, "- `console.log(inspect($last))` is automatically added when console.log is NOT in the source.")
	fmt.Fprintln(&b, "- Variables and state persist between cells. Promises must settle before the timeout or the cell will error.")
	fmt.Fprintln(&b, "	- Redeclaring const, let, classes, or functions with the same name in later cells causes an error (use var or leave global).")
	fmt.Fprintln(&b, "  - For long cells use unique variable names")
	fmt.Fprintln(&b, "  - For small cells it's easier to use $last / $val(cell_index) to reuse prior results.")
	fmt.Fprintln(&b, "  - Cells ending with console.log, return undefined — end with the variable if you need to reference it later.")
	fmt.Fprintln(&b, "- There are no imports")
	if mode == ToolSurfaceModeUnlocked {
		fmt.Fprintf(&b, "- Start a notebook with `%s`, then pass the same `%s` on every `%s` call to continue that notebook.\n", NewSessionToolName, TBSessionParam, SuperToolName)
	}
	if awaitAvailable {
		if mode == ToolSurfaceModeUnlocked {
			fmt.Fprintf(&b, "- If a cell pauses for approvals, call `%s` with the same `%s` to wait for the next approval result.\n", AwaitSuperToolApprovalsName, TBSessionParam)
		} else {
			fmt.Fprintf(&b, "- If a cell pauses for approvals, call `%s` to wait for the next approval result.\n", AwaitSuperToolApprovalsName)
		}
	}
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "===")
	fmt.Fprintln(&b, "// Notebook Input")
	fmt.Fprintln(&b, `Object.entries($pkgMetadata).map(([name, meta]) => [`)
	fmt.Fprintln(&b, "  name, meta.toolCount, (meta.useWhenHint || \"\")")
	fmt.Fprintln(&b, "])")
	fmt.Fprintln(&b, "// Notebook Output")
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
	fmt.Fprintln(&b, "// truncated object summary for inspecting data shape (limited depth and length traversal)")
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
type ToolCallTask<T = unknown> = {
  toolCallId: string;
};

type ToolCallPromise<T> = Promise<T> & {
  toolCallTask: ToolCallTask<T>;
};

type ToolCallView<T = unknown> =
  | {
      toolCallId: string;
      toolName: string;
      status: "started";
      params?: unknown;
    }
  | {
      toolCallId: string;
      toolName: string;
      status: "needsApproval";
      params?: unknown;
      approval: { approvalId?: string };
    }
  | {
      toolCallId: string;
      toolName: string;
      status: "success";
      params?: unknown;
      result: T;
    }
  | {
      toolCallId: string;
      toolName: string;
      status: "failed";
      params?: unknown;
      error: unknown;
    }
  | {
      toolCallId: string;
      toolName: string;
      status: "cancelled";
      params?: unknown;
    }
  | {
      toolCallId: string;
      toolName: string;
      status: "unknown";
      params?: unknown;
    };

declare function $tool_call<T>(refOrId: ToolCallTask<T> | string): ToolCallView<T>;
declare function $tool_call<T>(ref: ToolCallPromise<T>): ToolCallView<T>;

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
	if err := s.requireLease(ctx); err != nil {
		return formatSubmitError(repl.SubmitResult{}, err, s.id, s.toolCalls)
	}

	s.mu.Lock()
	if err := s.activeErrorLocked(); err != nil {
		s.mu.Unlock()
		return formatSubmitError(repl.SubmitResult{}, err, s.id, s.toolCalls)
	}
	if s.toolRuns != nil {
		s.toolRuns.Reset()
	}
	s.cancelApprovalAwaitLocked()
	s.submitting.Store(true)
	if s.approvals != nil {
		s.approvals.BeginSubmit(s.id)
	}

	submitCtx, cancel := withSubmitTimeout(ctx)
	defer cancel()

	res, err := s.session.SubmitCell(submitCtx, repl.SubmitInput{
		Source:   tsSource,
		Language: repl.CellLanguageTypeScript,
	})
	s.applyPendingPreparedToolsLocked()
	if err != nil {
		if s.approvals != nil {
			s.approvals.AbortSubmit(s.id)
		}
		s.submitting.Store(false)
		s.mu.Unlock()
		return formatSubmitError(res, err, s.id, s.toolCalls)
	}
	if s.approvals != nil {
		_ = s.approvals.CommitSubmit(s.id, res.Cell)
	}
	s.submitting.Store(false)
	s.mu.Unlock()
	return formatSubmitResult(ctx, s.session, res)
}

func (s *Session) PendingApprovals(ctx context.Context) ([]PendingApproval, error) {
	if s == nil || s.approvals == nil {
		return nil, nil
	}
	if err := s.requireLease(ctx); err != nil {
		return nil, err
	}
	return s.pendingApprovalsLocked(ctx)
}

func (s *Session) ApplyApprovals(ctx context.Context, decisions []ApprovalDecision) error {
	if s == nil {
		return nil
	}
	if err := s.requireLease(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.activeErrorLocked(); err != nil {
		return err
	}
	if s.approvals == nil {
		return fmt.Errorf("approvals are unavailable")
	}
	results, err := s.approvals.ApplyDecisions(ctx, s.id, decisions, s.prepared.Get(), s.store, s.toolCalls, s.executor)
	if err != nil {
		return err
	}
	for i := range results {
		results[i].ToolCall.TBSession = s.tbSession
	}
	if len(results) > 0 {
		s.publishApprovalAwaitLocked(approvalAwaitResultFromBatch(results, s.pendingApprovalCountLocked(ctx)))
	}
	return nil
}

func (s *Session) cancelApprovalAwaitLocked() {
	remaining := s.pendingApprovalCountLocked(context.Background())
	if remaining == 0 {
		return
	}
	s.publishApprovalAwaitLocked(ApprovalAwaitResult{
		Status:    ApprovalAwaitStatusCancelled,
		Remaining: remaining,
		Message:   "approval wait cancelled by new super_tool submit.",
	})
}

func (s *Session) publishApprovalAwaitLocked(result ApprovalAwaitResult) {
	if s == nil || s.approvalAwaits == nil {
		return
	}
	s.approvalAwaits.Publish(result)
}

func (s *Session) pendingApprovalCountLocked(ctx context.Context) int {
	approvals, err := s.pendingApprovalsLocked(ctx)
	if err != nil {
		return 0
	}
	return len(approvals)
}

func (s *Session) pendingApprovalsLocked(ctx context.Context) ([]PendingApproval, error) {
	if s == nil || s.approvals == nil {
		return nil, nil
	}
	approvals, err := s.approvals.PendingApprovals(ctx, s.id)
	if err != nil {
		return nil, err
	}
	for i := range approvals {
		approvals[i].TBSession = s.tbSession
		approvals[i].IntentText = normalizeSessionIntent(s.intentText)
		approvals[i].IntentSource = strings.TrimSpace(s.intentSource)
		approvals[i].IntentUpdatedAt = s.intentUpdatedAt
	}
	return approvals, nil
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
	session := s.session
	storeCloser := s.storeCloser
	lease := s.lease
	toolRuns := s.toolRuns
	executor := s.executor
	ownExecutor := s.ownExecutor
	s.session = nil
	s.storeCloser = nil
	s.lease = nil
	s.toolRuns = nil
	s.executor = nil
	s.ownExecutor = false
	s.markTerminalLocked(errors.New("session closed"))
	s.mu.Unlock()
	var errs []error
	if toolRuns != nil {
		toolRuns.Close()
	}
	if lease != nil {
		errs = append(errs, lease.Close())
	}
	if session != nil {
		errs = append(errs, session.Close())
	}
	if storeCloser != nil {
		errs = append(errs, storeCloser.Close())
	}
	if ownExecutor && executor != nil {
		errs = append(errs, executor.Close())
	}
	return errors.Join(errs...)
}

func (s *Session) requireLease(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	err := s.activeErrorLocked()
	lease := s.lease
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if lease == nil {
		return nil
	}
	if err := lease.ensureOwned(ctx); err != nil {
		s.markTerminal(err)
		return err
	}
	return nil
}

func (s *Session) activeErrorLocked() error {
	if s == nil {
		return nil
	}
	if s.terminalErr != nil {
		return s.terminalErr
	}
	if s.session == nil {
		return errors.New("session closed")
	}
	return nil
}

func (s *Session) terminalError() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminalErr
}

func (s *Session) markTerminal(err error) {
	if s == nil || err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markTerminalLocked(err)
}

func (s *Session) markTerminalLocked(err error) {
	if s == nil || err == nil || s.terminalErr != nil {
		return
	}
	s.terminalErr = err
	if s.approvalAwaits != nil {
		s.approvalAwaits.PublishError(err)
	}
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

func formatSubmitError(res repl.SubmitResult, err error, sessionID repl.SessionID, toolCalls toolCallJournal) string {
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
	writeToolCallRecovery(&b, sessionID, toolCalls, submitErr.LinkedEffects)
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

func writeToolCallRecovery(b *strings.Builder, sessionID repl.SessionID, toolCalls toolCallJournal, effects []repl.EffectSummary) {
	if len(effects) == 0 {
		return
	}
	fmt.Fprintf(b, "tool call completion status in failed cell (%d):\n", len(effects))
	for i, effect := range effects {
		call := toolCallRecoveryForEffect(sessionID, toolCalls, effect)
		ref := fmt.Sprintf("%q", call.ToolCallID)
		if call.ToolCallID == "" {
			ref = "<unavailable>"
		}
		fmt.Fprintf(b, "%d. %s\n", i+1, ref)
		fmt.Fprintf(b, "   tool: %s\n", call.ToolName)
		fmt.Fprintf(b, "   status: %s\n", call.Status)
		if len(call.Params) > 0 {
			fmt.Fprintf(b, "   params: %s\n", formatToolCallPayload(call.Params))
		}
		if call.Error != "" {
			fmt.Fprintf(b, "   error: %s\n", compactSubmitLine(call.Error))
		}
	}
	fmt.Fprintln(b, "Recovery: use $tool_call(\"<tool-call-id>\") in a new cell to recover data or status.")
	fmt.Fprintln(b, "A success view includes result; a failed view includes error; needsApproval waits for approval; started may still finish; unknown means the in-flight result could not be recovered.")
}

type toolCallRecovery struct {
	ToolCallID string
	ToolName   string
	Status     string
	Params     []byte
	Error      string
}

func toolCallRecoveryForEffect(sessionID repl.SessionID, toolCalls toolCallJournal, effect repl.EffectSummary) toolCallRecovery {
	toolCallID := strings.TrimSpace(string(effect.Effect))
	call := toolCallRecovery{
		ToolCallID: toolCallID,
		ToolName:   displayEffectToolName(effect.FunctionName),
		Status:     toolCallStatusFromEffect(effect.Status),
		Params:     effect.Params,
		Error:      effect.ErrorMessage,
	}
	if toolCallID == "" || sessionID == "" || toolCalls == nil {
		return call
	}
	snapshot, ok, err := toolCalls.Snapshot(sessionID, toolCallID)
	if err != nil || !ok {
		return call
	}
	if strings.TrimSpace(snapshot.ToolName) != "" {
		call.ToolName = snapshot.ToolName
	}
	call.Status = string(snapshot.Status)
	call.Params = snapshot.Params
	call.Error = snapshot.Error
	return call
}

func displayEffectToolName(name string) string {
	name = strings.TrimSpace(name)
	if toolName := strings.TrimPrefix(name, pendingApprovalEffectName("")); toolName != name {
		return toolName
	}
	if name == "" {
		return "<unknown>"
	}
	return name
}

func toolCallStatusFromEffect(status repl.EffectStatus) string {
	switch status {
	case repl.EffectStatusCompleted:
		return string(toolCallStatusSuccess)
	case repl.EffectStatusFailed:
		return string(toolCallStatusFailed)
	case repl.EffectStatusPending:
		return string(toolCallStatusStarted)
	default:
		if text := strings.TrimSpace(string(status)); text != "" {
			return text
		}
		return string(toolCallStatusUnknown)
	}
}

func formatToolCallPayload(raw []byte) string {
	value, err := decodeStoredJSWireValue(raw)
	if err != nil {
		return compactSubmitLine(fmt.Sprintf("<unreadable: %s>", err))
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return compactSubmitLine(fmt.Sprintf("%v", value))
	}
	return compactSubmitLine(string(encoded))
}

func compactSubmitLine(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	const max = 900
	if len(text) <= max {
		return text
	}
	return text[:max-3] + "..."
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
