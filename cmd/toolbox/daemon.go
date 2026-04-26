package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/solidarity-ai/toolbox/daemon"
)

const (
	defaultDaemonBindAddress = daemon.DefaultBindAddress
	daemonBindAddressEnv     = daemon.BindAddressEnv
	daemonAdminEndpointsEnv  = "TOOLBOX_DAEMON_ENABLE_ADMIN"
)

var daemonHardExit = func() {
	proc, err := os.FindProcess(os.Getpid())
	if err == nil {
		_ = proc.Kill()
	}
}

var daemonStopAll = func(logf func(string, ...any)) ([]int, error) {
	return daemon.StopAllWithProgress(logf)
}

var daemonBrowserLauncher browserLauncher = defaultBrowserLauncher{}

type daemonCmd struct {
	Serve daemonServeCmd `cmd:"" name:"serve" help:"Run the daemon server."`
	Ping  daemonPingCmd  `cmd:"" name:"ping" hidden:"" help:"Ping the daemon."`
	Stop  daemonStopCmd  `cmd:"" name:"stop" help:"Stop all running daemon servers."`
}

type daemonServeCmd struct{}

type daemonPingCmd struct {
	PID bool `name:"pid" hidden:"" help:"Include the daemon PID in the output."`
}

type daemonStopCmd struct{}

func runDaemonServe(stderr io.Writer) error {
	dir, err := daemon.Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create daemon dir: %w", err)
	}

	bindAddress, explicitDebugBind := daemonBindAddress()
	debugListener, err := net.Listen("tcp", bindAddress)
	if err != nil {
		if explicitDebugBind {
			return fmt.Errorf("listen on daemon debug address %s: %w", bindAddress, err)
		}
		if stderr != nil {
			_, _ = fmt.Fprintf(stderr, "toolbox daemon debug server disabled: listen on daemon debug address %s: %v\n", bindAddress, err)
		}
	}
	closeDebugListener := func() {
		if debugListener == nil {
			return
		}
		_ = debugListener.Close()
		debugListener = nil
	}

	socketPath, err := daemon.SocketPath()
	if err != nil {
		closeDebugListener()
		return err
	}
	pidPath, err := daemon.PIDPath()
	if err != nil {
		closeDebugListener()
		return err
	}

	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		closeDebugListener()
		return fmt.Errorf("remove stale daemon socket: %w", err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		closeDebugListener()
		return fmt.Errorf("listen on daemon socket: %w", err)
	}
	defer listener.Close()

	if err := os.Chmod(socketPath, 0o600); err != nil {
		closeDebugListener()
		return fmt.Errorf("chmod daemon socket: %w", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		closeDebugListener()
		return fmt.Errorf("write daemon pid: %w", err)
	}

	udsServer := daemon.NewServer(listener)

	closeDebugServer := func() error {
		if debugListener == nil {
			return nil
		}
		err := debugListener.Close()
		debugListener = nil
		return err
	}
	var shutdownOnce sync.Once
	shutdown := func() {
		shutdownOnce.Do(func() {
			if closeDebugServer != nil {
				_ = closeDebugServer()
			}
			_ = udsServer.Close()
		})
	}
	var debugAddr string
	if debugListener != nil {
		closeDebugServer, debugAddr, err = serveDaemonDebugServer(debugListener, stderr, shutdown, udsServer)
		if err != nil {
			return err
		}
		debugListener = nil
	}
	maybeAutoOpenDaemonBrowser(stderr, udsServer, debugAddr)

	defer func() {
		shutdown()
		_ = os.Remove(socketPath)
		_ = os.Remove(pidPath)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(sigCh)

	go func() {
		<-sigCh
		shutdown()
	}()

	return udsServer.Serve()
}

func daemonBindAddress() (string, bool) {
	return daemon.BindAddress()
}

func daemonAdminEndpointsEnabled() bool {
	switch os.Getenv(daemonAdminEndpointsEnv) {
	case "1", "true", "TRUE", "yes", "YES":
		return true
	default:
		return false
	}
}

func defaultDaemonBrowserAutoOpenEnabled() bool {
	return flag.Lookup("test.v") == nil
}

func defaultDaemonOpenBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return fmt.Errorf("browser launch is not supported on %s", runtime.GOOS)
	}
}

type browserLauncher interface {
	Enabled() bool
	Open(string) error
}

type defaultBrowserLauncher struct{}

func (defaultBrowserLauncher) Enabled() bool {
	return defaultDaemonBrowserAutoOpenEnabled()
}

func (defaultBrowserLauncher) Open(url string) error {
	return defaultDaemonOpenBrowser(url)
}

type daemonHTTPControl interface {
	Clients() []daemon.ClientSnapshot
	ApplyApprovals(context.Context, []daemon.ApprovalDecision) error
	UnlockSecretStore(context.Context, string) error
	LockSecretStore(context.Context) error
	SecretStoreLocked(context.Context) (bool, error)
}

type daemonHTTPRevisionControl interface {
	Revision() uint64
}

type secretStoreUnlockRequest struct {
	UnlockKey string `json:"unlock_key"`
}

type secretStoreStatusResponse struct {
	Locked bool `json:"locked"`
}

type approvalRejectRequest struct {
	Message string `json:"message"`
}

type approvalApplyDecisionRequest struct {
	ID       string `json:"id"`
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
}

type approvalApplyRequest struct {
	Approvals []approvalApplyDecisionRequest `json:"approvals"`
}

type approvalConsoleDecisionRequest struct {
	ObservedRevision uint64                               `json:"observed_revision"`
	ClientDecisionID string                               `json:"client_decision_id"`
	Decisions        []approvalConsoleDecisionRequestItem `json:"decisions"`
}

type approvalConsoleDecisionRequestItem struct {
	ToolCallID string `json:"tool_call_id"`
	Decision   string `json:"decision"`
	Reason     string `json:"reason,omitempty"`
}

type approvalConsoleDecisionResponse struct {
	Accepted []approvalConsoleAcceptedDecision `json:"accepted"`
	Errors   []approvalConsoleDecisionError    `json:"errors"`
	Revision uint64                            `json:"revision"`
}

type approvalConsoleAcceptedDecision struct {
	ToolCallID string `json:"tool_call_id"`
	Decision   string `json:"decision"`
	QueuedAt   string `json:"queued_at"`
}

type approvalConsoleDecisionError struct {
	ToolCallID string `json:"tool_call_id"`
	Error      string `json:"error"`
}

type daemonHTTPApprovalGroup struct {
	ID        string                           `json:"id"`
	Label     string                           `json:"label,omitempty"`
	ToolCalls []daemon.PendingApprovalSnapshot `json:"tool_calls,omitempty"`
}

type approvalConsoleState struct {
	Revision    uint64                     `json:"revision"`
	ServerTime  string                     `json:"server_time"`
	SecretStore approvalConsoleSecretStore `json:"secret_store"`
	Summary     approvalConsoleSummary     `json:"summary"`
	Sessions    []approvalConsoleSession   `json:"sessions"`
}

type approvalConsoleSecretStore struct {
	Status string `json:"status"`
}

type approvalConsoleSummary struct {
	ActiveClients    int `json:"active_clients"`
	PendingApprovals int `json:"pending_approvals"`
	QueuedDecisions  int `json:"queued_decisions"`
}

type approvalConsoleSession struct {
	TBSession     string                        `json:"tb_session"`
	Active        bool                          `json:"active"`
	UpdatedAt     string                        `json:"updated_at,omitempty"`
	Intent        approvalConsoleIntent         `json:"intent"`
	Details       approvalConsoleSessionDetails `json:"details"`
	PackageGroups []approvalConsolePackageGroup `json:"package_groups"`
}

type approvalConsoleIntent struct {
	Text      string `json:"text"`
	Source    string `json:"source,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type approvalConsoleSessionDetails struct {
	Mode       string `json:"mode,omitempty"`
	PID        int    `json:"pid,omitempty"`
	WorkingDir string `json:"working_dir,omitempty"`
	LastSyncAt string `json:"last_sync_at,omitempty"`
}

type approvalConsolePackageGroup struct {
	PackageKey   string                           `json:"package_key"`
	PackageLabel string                           `json:"package_label"`
	ToolCalls    []daemon.PendingApprovalSnapshot `json:"tool_calls"`
}

func approvalConsoleStateForHTTP(control daemonHTTPControl) approvalConsoleState {
	now := time.Now().UTC()
	state := approvalConsoleState{
		ServerTime:  now.Format(time.RFC3339Nano),
		SecretStore: approvalConsoleSecretStore{Status: "unavailable"},
	}
	if revControl, ok := control.(daemonHTTPRevisionControl); ok {
		state.Revision = revControl.Revision()
	}
	if control == nil {
		return state
	}
	if locked, err := control.SecretStoreLocked(context.Background()); err == nil {
		if locked {
			state.SecretStore.Status = "locked"
		} else {
			state.SecretStore.Status = "unlocked"
		}
	}
	clients := control.Clients()
	state.Summary.ActiveClients = len(clients)
	grouped := make(map[string]*approvalConsoleSession)
	for _, client := range clients {
		for _, approval := range client.PendingApprovals {
			state.Summary.PendingApprovals++
			if approval.QueuedDecision != nil {
				state.Summary.QueuedDecisions++
			}
			tbSession := strings.TrimSpace(approval.TBSession)
			if tbSession == "" {
				tbSession = strings.TrimSpace(client.BoundTBSession)
			}
			if tbSession == "" {
				tbSession = "unknown"
			}
			session := grouped[tbSession]
			if session == nil {
				intentText := firstNonEmpty(approval.IntentText, client.IntentText, "Manual Toolbox session")
				intentSource := firstNonEmpty(approval.IntentSource, client.IntentSource, "fallback")
				intentUpdatedAt := firstNonEmpty(approval.IntentUpdatedAt, client.IntentUpdatedAt)
				updatedAt := latestTimeString(approval.UpdatedAt, client.LastSyncAt)
				session = &approvalConsoleSession{
					TBSession: tbSession,
					Active:    !isStaleClient(client.LastSyncAt, now),
					UpdatedAt: updatedAt,
					Intent: approvalConsoleIntent{
						Text:      intentText,
						Source:    intentSource,
						UpdatedAt: intentUpdatedAt,
					},
					Details: approvalConsoleSessionDetails{
						Mode:       client.Mode,
						PID:        client.PID,
						WorkingDir: client.WorkingDir,
						LastSyncAt: formatHTTPTime(client.LastSyncAt),
					},
				}
				grouped[tbSession] = session
			}
			addApprovalToConsoleSession(session, approval)
		}
	}
	for _, session := range grouped {
		sort.Slice(session.PackageGroups, func(i, j int) bool {
			return session.PackageGroups[i].PackageLabel < session.PackageGroups[j].PackageLabel
		})
		state.Sessions = append(state.Sessions, *session)
	}
	sort.Slice(state.Sessions, func(i, j int) bool {
		return state.Sessions[i].TBSession < state.Sessions[j].TBSession
	})
	return state
}

func addApprovalToConsoleSession(session *approvalConsoleSession, approval daemon.PendingApprovalSnapshot) {
	pkgKey := firstNonEmpty(approval.PackageKey, packageKeyFromToolName(approval.FullToolName), packageKeyFromToolName(approval.ToolName), "unknown")
	pkgLabel := firstNonEmpty(approval.PackageLabel, pkgKey)
	toolLabel := firstNonEmpty(approval.ToolLabel, shortToolLabel(approval.ToolName, pkgKey), approval.ToolName)
	approval.PackageKey = pkgKey
	approval.PackageLabel = pkgLabel
	approval.ToolLabel = toolLabel
	if approval.FullToolName == "" {
		approval.FullToolName = approval.ToolName
	}
	for i := range session.PackageGroups {
		if session.PackageGroups[i].PackageKey == pkgKey {
			session.PackageGroups[i].ToolCalls = append(session.PackageGroups[i].ToolCalls, approval)
			return
		}
	}
	session.PackageGroups = append(session.PackageGroups, approvalConsolePackageGroup{
		PackageKey:   pkgKey,
		PackageLabel: pkgLabel,
		ToolCalls:    []daemon.PendingApprovalSnapshot{approval},
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func packageKeyFromToolName(toolName string) string {
	parts := strings.Split(strings.TrimSpace(toolName), ".")
	if len(parts) > 1 {
		return parts[0]
	}
	return ""
}

func shortToolLabel(toolName, pkgKey string) string {
	toolName = strings.TrimSpace(toolName)
	pkgKey = strings.TrimSpace(pkgKey)
	prefix := pkgKey + "."
	if pkgKey != "" && strings.HasPrefix(toolName, prefix) {
		return strings.TrimPrefix(toolName, prefix)
	}
	return toolName
}

func formatHTTPTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func latestTimeString(raw string, fallback time.Time) string {
	if strings.TrimSpace(raw) != "" {
		return strings.TrimSpace(raw)
	}
	return formatHTTPTime(fallback)
}

func isStaleClient(lastSync, now time.Time) bool {
	if lastSync.IsZero() {
		return false
	}
	return now.Sub(lastSync) > 30*time.Second
}

func pendingApprovalGroupsForHTTP(control daemonHTTPControl) []daemonHTTPApprovalGroup {
	if control == nil {
		return nil
	}
	clients := control.Clients()
	if len(clients) == 0 {
		return nil
	}

	grouped := make(map[string]*daemonHTTPApprovalGroup)
	for _, client := range clients {
		for _, approval := range client.PendingApprovals {
			tbSession := strings.TrimSpace(approval.TBSession)
			if tbSession == "" {
				tbSession = "unknown"
			}
			group := grouped[tbSession]
			if group == nil {
				group = &daemonHTTPApprovalGroup{
					ID:    "tb_session:" + tbSession,
					Label: tbSession,
				}
				grouped[tbSession] = group
			}
			group.ToolCalls = append(group.ToolCalls, approval)
		}
	}
	out := make([]daemonHTTPApprovalGroup, 0, len(grouped))
	for _, group := range grouped {
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func findPendingApprovalGroup(control daemonHTTPControl, groupID string) (daemonHTTPApprovalGroup, bool) {
	for _, group := range pendingApprovalGroupsForHTTP(control) {
		if group.ID == groupID {
			return group, true
		}
	}
	return daemonHTTPApprovalGroup{}, false
}

func toDaemonApprovalDecisions(decisions []approvalApplyDecisionRequest) []daemon.ApprovalDecision {
	if len(decisions) == 0 {
		return nil
	}
	out := make([]daemon.ApprovalDecision, 0, len(decisions))
	for _, decision := range decisions {
		action := daemon.ApprovalActionReject
		if decision.Approved {
			action = daemon.ApprovalActionApprove
		}
		out = append(out, daemon.ApprovalDecision{
			Action:     action,
			ToolCallID: decision.ID,
			Message:    decision.Reason,
		})
	}
	return out
}

func applyApprovalConsoleDecisions(ctx context.Context, control daemonHTTPControl, req approvalConsoleDecisionRequest) approvalConsoleDecisionResponse {
	resp := approvalConsoleDecisionResponse{}
	if control == nil {
		resp.Errors = append(resp.Errors, approvalConsoleDecisionError{Error: "approvals unavailable"})
		return resp
	}
	pending := make(map[string]struct{})
	for _, group := range pendingApprovalGroupsForHTTP(control) {
		for _, call := range group.ToolCalls {
			pending[call.ToolCallID] = struct{}{}
		}
	}
	var decisions []daemon.ApprovalDecision
	queuedAt := time.Now().UTC()
	for _, item := range req.Decisions {
		id := strings.TrimSpace(item.ToolCallID)
		decision := strings.TrimSpace(item.Decision)
		if id == "" {
			resp.Errors = append(resp.Errors, approvalConsoleDecisionError{ToolCallID: id, Error: "missing tool_call_id"})
			continue
		}
		if _, ok := pending[id]; !ok {
			resp.Errors = append(resp.Errors, approvalConsoleDecisionError{ToolCallID: id, Error: "approval no longer pending"})
			continue
		}
		action := ""
		switch decision {
		case "approve":
			action = daemon.ApprovalActionApprove
		case "reject":
			action = daemon.ApprovalActionReject
		default:
			resp.Errors = append(resp.Errors, approvalConsoleDecisionError{ToolCallID: id, Error: "decision must be approve or reject"})
			continue
		}
		decisions = append(decisions, daemon.ApprovalDecision{
			Action:           action,
			ToolCallID:       id,
			Message:          strings.TrimSpace(item.Reason),
			ClientDecisionID: strings.TrimSpace(req.ClientDecisionID),
			QueuedAt:         queuedAt,
		})
		resp.Accepted = append(resp.Accepted, approvalConsoleAcceptedDecision{
			ToolCallID: id,
			Decision:   decision,
			QueuedAt:   queuedAt.Format(time.RFC3339Nano),
		})
	}
	if len(decisions) > 0 {
		if err := control.ApplyApprovals(ctx, decisions); err != nil {
			resp.Errors = append(resp.Errors, approvalConsoleDecisionError{Error: err.Error()})
		}
	}
	if revControl, ok := control.(daemonHTTPRevisionControl); ok {
		resp.Revision = revControl.Revision()
	}
	return resp
}

func maybeAutoOpenDaemonBrowser(stderr io.Writer, control daemonHTTPControl, addr string) {
	launcher := daemonBrowserLauncher
	if control == nil || launcher == nil || !launcher.Enabled() {
		return
	}

	if strings.TrimSpace(addr) == "" {
		return
	}

	locked, err := control.SecretStoreLocked(context.Background())
	if err != nil || !locked {
		return
	}

	go func(url string, launcher browserLauncher, stderr io.Writer) {
		if err := launcher.Open(url); err != nil && stderr != nil {
			_, _ = fmt.Fprintf(stderr, "toolbox daemon browser launch error: %v\n", err)
		}
	}("http://"+addr+"/", launcher, stderr)
}

type daemonIndexPageData struct {
	StatusText string
	Available  bool
	Locked     bool
}

var daemonIndexTemplate = template.Must(template.New("daemon-index").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Toolbox Approvals</title>
  <style>
    :root { color-scheme: light; --border:#d8dde3; --muted:#5d6673; --bg:#f7f8fa; --text:#15181d; --accent:#1967d2; --ok:#16833a; --danger:#b42318; }
    * { box-sizing: border-box; }
    body { margin: 0; background: var(--bg); color: var(--text); font: 14px/1.45 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    button, input, textarea { font: inherit; }
    button { border: 1px solid var(--border); background: #fff; border-radius: 6px; padding: 0.42rem 0.7rem; cursor: pointer; }
    button.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
    button:disabled { opacity: .5; cursor: not-allowed; }
    .topbar { position: sticky; top: 0; z-index: 10; display: flex; align-items: center; gap: 1rem; padding: .8rem 1rem; background: #fff; border-bottom: 1px solid var(--border); }
    .brand { font-weight: 700; font-size: 16px; margin-right: auto; }
    .pill { color: var(--muted); white-space: nowrap; }
    .dot { display:inline-block; width:.55rem; height:.55rem; border-radius:999px; background: var(--ok); margin-right:.35rem; }
    .dot.warn { background:#c77700; } .dot.off { background:#9aa2ad; }
    main { max-width: 1100px; margin: 1rem auto 3rem; padding: 0 1rem; }
    .message { min-height: 1.3rem; color: var(--danger); white-space: pre-wrap; }
    .secret-panel { display:none; margin:.75rem 0; padding:.75rem; border:1px solid var(--border); background:#fff; border-radius:8px; }
    .secret-panel.open { display:block; }
    .intent { background:#fff; border:1px solid var(--border); border-radius:8px; margin:1rem 0; overflow:hidden; }
    .intent-head { display:flex; align-items:flex-start; gap:.75rem; padding:1rem 1rem .65rem; border-bottom:1px solid var(--border); }
    .intent-title { font-size:16px; font-weight:650; flex:1; }
    .intent-meta { color:var(--muted); font-size:13px; margin-top:.2rem; }
    .session-info { color:var(--muted); font-size:12px; white-space:pre-line; text-align:right; }
    .package { padding: .7rem 1rem 0; }
    .package-title { display:flex; align-items:center; gap:.45rem; font-weight:700; padding-bottom:.45rem; border-bottom:1px solid var(--border); }
    .row { padding:.75rem 0; border-bottom:1px solid #edf0f3; }
    .row-head { display:grid; grid-template-columns:auto minmax(0,1fr) auto; gap:.8rem; align-items:center; }
    .tool-name { font-weight:650; white-space:nowrap; }
    .desc { color:var(--muted); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
    .seg { display:inline-flex; border:1px solid var(--border); border-radius:7px; overflow:hidden; height:32px; background:#fff; }
    .seg button { border:0; border-right:1px solid var(--border); border-radius:0; padding:0 .55rem; font-size:13px; }
    .seg button:last-child { border-right:0; }
    .seg button.active { background:#eaf2ff; color:#174ea6; font-weight:650; }
    .seg button.reject.active { background:#fff0ee; color:var(--danger); }
    .summary { display:grid; grid-template-columns:minmax(7rem, 12rem) minmax(0,1fr); gap:.25rem 1rem; margin-top:.55rem; }
    .label { color:var(--muted); }
    .value { overflow-wrap:anywhere; }
    details { margin-top:.45rem; }
    summary { color:var(--accent); cursor:pointer; width:fit-content; }
    pre { margin:.35rem 0 0; padding:.75rem; background:#f4f6f8; border:1px solid var(--border); border-radius:6px; overflow:auto; white-space:pre-wrap; }
    .details-grid { display:grid; grid-template-columns:9rem minmax(0,1fr); gap:.35rem .8rem; margin-top:.6rem; }
    .footer { position:sticky; bottom:0; display:flex; align-items:center; gap:.75rem; padding:.75rem 1rem; background:#fbfcfd; border-top:1px solid var(--border); }
    .footer .counts { margin-right:auto; color:var(--muted); }
    .empty { text-align:center; color:var(--muted); padding:4rem 1rem; border:1px dashed var(--border); background:#fff; border-radius:8px; }
    dialog { border:1px solid var(--border); border-radius:8px; padding:1rem; max-width:420px; width:calc(100% - 2rem); }
    dialog textarea { width:100%; min-height:5rem; margin:.5rem 0; }
    @media (max-width: 700px) { .topbar { flex-wrap:wrap; } .row-head { grid-template-columns:1fr; } .desc { display:none; } .summary,.details-grid { grid-template-columns:1fr; } .session-info { text-align:left; } .footer { flex-wrap:wrap; } }
  </style>
</head>
<body>
  <header class="topbar">
    <div class="brand">Toolbox Approvals</div>
    <div class="pill" id="live-state"><span class="dot warn"></span>Connecting</div>
    <div class="pill" id="secret-state">{{.StatusText}}</div>
    <div class="pill" id="client-count">0 clients</div>
    <div class="pill" id="pending-count">0 pending</div>
    <button id="settings-button" type="button" title="Settings">Settings</button>
  </header>
  <main>
    <p style="position:absolute;left:-10000px;">Status: <strong id="status">{{.StatusText}}</strong></p>
    <section id="secret-panel" class="secret-panel">
      {{if .Available}}{{if .Locked}}
      <form id="unlock-form">
        <label for="unlock-key">Secret store locked</label>
        <input id="unlock-key" name="unlock_key" type="password" autocomplete="current-password" placeholder="Passcode">
        <button type="submit" class="primary">Unlock</button>
      </form>
      {{else}}
      <form id="lock-form"><span>Secret store unlocked</span> <button type="submit">Lock</button></form>
      {{end}}{{else}}Secret store unavailable.{{end}}
    </section>
    <p class="message" id="message"></p>
    <section id="app" class="empty">Loading approvals...</section>
  </main>
  <dialog id="reject-dialog">
    <form method="dialog">
      <h3 id="reject-title">Reject approvals?</h3>
      <label for="reject-reason">Reason</label>
      <textarea id="reject-reason" placeholder="Optional reason"></textarea>
      <div style="display:flex;justify-content:flex-end;gap:.5rem;">
        <button value="cancel">Cancel</button>
        <button value="submit" class="primary">Submit decisions</button>
      </div>
    </form>
  </dialog>
  <script>
    const state = { revision:0, drafts:new Map(), submitted:new Set(), source:null };
    const app = document.getElementById('app'), message = document.getElementById('message');
    const live = document.getElementById('live-state'), secret = document.getElementById('secret-state');
    const clients = document.getElementById('client-count'), pending = document.getElementById('pending-count');
    document.getElementById('settings-button').onclick = () => document.getElementById('secret-panel').classList.toggle('open');
    const unlockForm = document.getElementById('unlock-form'), lockForm = document.getElementById('lock-form');

    function setLive(text, cls) { live.textContent = ''; const d=document.createElement('span'); d.className='dot '+(cls||''); live.append(d, document.createTextNode(text)); }
    async function postJSON(url, body) { const r=await fetch(url,{method:'POST',headers:{'Content-Type': 'application/json'},body:JSON.stringify(body)}); const t=await r.text(); if(!r.ok) throw new Error(t || 'request failed'); return t ? JSON.parse(t) : null; }
    async function postNoBody(url) { const r=await fetch(url,{method:'POST'}); const t=await r.text(); if(!r.ok) throw new Error(t || 'request failed'); }
    function swapFragment(html) { app.innerHTML = html; syncHeader(); applyDrafts(); }
    function syncHeader() { const f=document.getElementById('approval-fragment'); if(!f) return; secret.textContent=f.dataset.secretStatus||'Unavailable'; clients.textContent=(f.dataset.activeClients||'0')+' clients'; pending.textContent=(f.dataset.pendingApprovals||'0')+' pending'; state.revision=Number(f.dataset.revision||0); for (const id of Array.from(state.submitted)) if (!document.querySelector('[data-tool-call-id="'+CSS.escape(id)+'"]')) state.submitted.delete(id); }
    function applyDrafts() { for (const row of app.querySelectorAll('[data-tool-call-id]')) { const id=row.dataset.toolCallId; const draft=state.submitted.has(id) || row.dataset.queued === 'true' ? 'sent' : (state.drafts.get(id)||'leave'); row.dataset.draft=draft; for (const b of row.querySelectorAll('[data-decision]')) b.classList.toggle('active', b.dataset.decision === draft); const sent=row.querySelector('[data-sent]'); if(sent) sent.hidden = draft !== 'sent'; const seg=row.querySelector('.seg'); if(seg) seg.hidden = draft === 'sent'; } updateFooters(); }
    function updateFooters() { for (const session of app.querySelectorAll('[data-session]')) { const rows=[...session.querySelectorAll('[data-tool-call-id]')].filter(r=>r.dataset.draft!=='sent'); const approve=rows.filter(r=>r.dataset.draft==='approve').length, reject=rows.filter(r=>r.dataset.draft==='reject').length, unchanged=rows.length-approve-reject; const counts=session.querySelector('[data-counts]'); if(counts) counts.textContent=approve+' approve - '+reject+' reject - '+unchanged+' unchanged'; const submit=session.querySelector('[data-submit]'); if(submit) { submit.textContent='Submit '+(approve+reject)+' decisions'; submit.disabled=approve+reject===0; } } }
    function markSession(sessionEl, value) { for (const row of sessionEl.querySelectorAll('[data-tool-call-id]')) if(row.dataset.draft!=='sent') { value==='leave' ? state.drafts.delete(row.dataset.toolCallId) : state.drafts.set(row.dataset.toolCallId, value); } applyDrafts(); }
    async function submitSession(sessionEl) {
      const decisions=[]; for(const row of sessionEl.querySelectorAll('[data-tool-call-id]')) { const d=state.drafts.get(row.dataset.toolCallId); if(d==='approve'||d==='reject') decisions.push({tool_call_id:row.dataset.toolCallId, decision:d}); }
      const rejects=decisions.filter(d=>d.decision==='reject'); let reason='';
      if (rejects.length) { reason = await rejectReason(rejects.length); if (reason === null) return; for(const d of rejects) d.reason = reason; }
      message.textContent='';
      const res=await postJSON('/approval-console/decisions',{observed_revision:state.revision||0,client_decision_id:String(Date.now()),decisions});
      for(const a of res.accepted||[]) { state.submitted.add(a.tool_call_id); state.drafts.delete(a.tool_call_id); }
      if (res.errors?.length) message.textContent = res.errors.map(e => (e.tool_call_id || 'decision') + ': ' + e.error).join('\n');
      applyDrafts();
    }
    function rejectReason(count) { return new Promise(resolve => { const dlg=document.getElementById('reject-dialog'), title=document.getElementById('reject-title'), reason=document.getElementById('reject-reason'); title.textContent='Reject '+String(count)+' approval'+(count===1?'':'s')+'?'; reason.value=''; dlg.onclose=()=>resolve(dlg.returnValue==='submit'?reason.value:null); dlg.showModal(); }); }
    async function loadHTML() { const r=await fetch('/approval-console/html'); if(!r.ok) throw new Error(await r.text() || 'failed to load approvals'); swapFragment(await r.text()); }
    function connectEvents() { setLive('Connecting','warn'); const es=new EventSource('/approval-console/events'); state.source=es; es.onopen=()=>setLive('Live',''); es.onerror=()=>setLive('Disconnected','off'); es.addEventListener('html', e=>swapFragment(e.data)); }
    app.addEventListener('click', e => { const b=e.target.closest('button'); if(!b) return; const row=b.closest('[data-tool-call-id]'); if(b.dataset.decision && row) { const id=row.dataset.toolCallId, d=b.dataset.decision; d==='leave' ? state.drafts.delete(id) : state.drafts.set(id,d); applyDrafts(); } const session=b.closest('[data-session]'); if(b.dataset.mark && session) markSession(session,b.dataset.mark); if(b.dataset.clear && session) markSession(session,'leave'); if(b.dataset.submit && session) submitSession(session); });
    if (unlockForm) unlockForm.onsubmit=async e=>{ e.preventDefault(); await postJSON('/secret-store/unlock',{unlock_key:document.getElementById('unlock-key').value}); location.reload(); };
    if (lockForm) lockForm.onsubmit=async e=>{ e.preventDefault(); await postNoBody('/secret-store/lock'); location.reload(); };
    loadHTML().then(connectEvents).catch(err=>{ message.textContent=err.message || 'failed to load approvals'; setLive('Disconnected','off'); });
  </script>
</body>
</html>
`))

func renderDaemonIndex(w io.Writer, page daemonIndexPageData) error {
	return daemonIndexTemplate.Execute(w, page)
}

func renderApprovalConsoleHTML(state approvalConsoleState) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<div id="approval-fragment" data-revision="%d" data-secret-status="%s" data-active-clients="%d" data-pending-approvals="%d">`,
		state.Revision,
		escapeAttr(titleCase(state.SecretStore.Status)),
		state.Summary.ActiveClients,
		state.Summary.PendingApprovals,
	)
	if state.Summary.ActiveClients == 0 {
		b.WriteString(`<div class="empty"><h2>No active Toolbox sessions</h2><p>Start a codemode session to review tool approvals here.</p></div></div>`)
		return b.String()
	}
	if len(state.Sessions) == 0 {
		b.WriteString(`<div class="empty"><h2>No pending approvals</h2><p>Connected Toolbox sessions will appear here when they need approval.</p></div></div>`)
		return b.String()
	}
	for _, session := range state.Sessions {
		renderApprovalConsoleSessionHTML(&b, session)
	}
	b.WriteString(`</div>`)
	return b.String()
}

func renderApprovalConsoleSessionHTML(b *strings.Builder, session approvalConsoleSession) {
	fmt.Fprintf(b, `<section class="intent" data-session="%s">`, escapeAttr(session.TBSession))
	b.WriteString(`<div class="intent-head">`)
	dotClass := "dot"
	if !session.Active {
		dotClass += " off"
	}
	fmt.Fprintf(b, `<span class="%s"></span>`, dotClass)
	fmt.Fprintf(b, `<div class="intent-title">"%s"<div class="intent-meta">%d pending - updated %s</div></div>`,
		escapeText(session.Intent.Text),
		countConsoleSessionCalls(session),
		escapeText(displayTime(session.UpdatedAt)),
	)
	fmt.Fprintf(b, `<button type="button" data-mark="approve">Mark all approve</button>`)
	fmt.Fprintf(b, `<div class="session-info">Session %s&#10;%s - PID %d&#10;%s&#10;Synced %s</div>`,
		escapeText(session.TBSession),
		escapeText(firstNonEmpty(session.Details.Mode, "codemode")),
		session.Details.PID,
		escapeText(session.Details.WorkingDir),
		escapeText(displayTime(session.Details.LastSyncAt)),
	)
	b.WriteString(`</div>`)
	for _, group := range session.PackageGroups {
		renderApprovalConsolePackageHTML(b, group)
	}
	b.WriteString(`<div class="footer"><div class="counts" data-counts>0 approve - 0 reject - 0 unchanged</div><button type="button" data-clear="true">Clear choices</button><button type="button" class="primary" data-submit="true" disabled>Submit 0 decisions</button></div>`)
	b.WriteString(`</section>`)
}

func renderApprovalConsolePackageHTML(b *strings.Builder, group approvalConsolePackageGroup) {
	b.WriteString(`<section class="package">`)
	fmt.Fprintf(b, `<div class="package-title">%s %d</div>`, escapeText(firstNonEmpty(group.PackageLabel, group.PackageKey)), len(group.ToolCalls))
	for _, call := range group.ToolCalls {
		renderApprovalConsoleCallHTML(b, call)
	}
	b.WriteString(`</section>`)
}

func renderApprovalConsoleCallHTML(b *strings.Builder, call daemon.PendingApprovalSnapshot) {
	queued := "false"
	if call.QueuedDecision != nil {
		queued = "true"
	}
	fmt.Fprintf(b, `<div class="row" data-tool-call-id="%s" data-queued="%s" data-draft="leave">`, escapeAttr(call.ToolCallID), queued)
	b.WriteString(`<div class="row-head">`)
	fmt.Fprintf(b, `<div class="tool-name">%s</div>`, escapeText(firstNonEmpty(call.ToolLabel, call.ToolName)))
	fmt.Fprintf(b, `<div class="desc">%s</div>`, escapeText(call.Description))
	b.WriteString(`<div class="seg"><button type="button" data-decision="leave">Leave</button><button type="button" class="reject" data-decision="reject">Reject</button><button type="button" data-decision="approve">Approve</button></div>`)
	b.WriteString(`<div class="pill" data-sent hidden>Decision sent - Waiting for session...</div>`)
	b.WriteString(`</div>`)
	b.WriteString(`<div class="summary">`)
	fields := approvalSummaryFields(call)
	if len(fields) == 0 && strings.TrimSpace(call.ParamsInspect) != "" {
		fields = [][2]string{{"Params", call.ParamsInspect}}
	}
	for _, field := range fields {
		fmt.Fprintf(b, `<div class="label">%s</div><div class="value">%s</div>`, escapeText(field[0]), escapeText(field[1]))
	}
	b.WriteString(`</div>`)
	b.WriteString(`<details><summary>Details</summary><div class="details-grid">`)
	detail := func(label, value string) {
		fmt.Fprintf(b, `<div class="label">%s</div><div>%s</div>`, escapeText(label), escapeText(value))
	}
	detail("Tool call", firstNonEmpty(call.FullToolName, call.ToolName))
	detail("Call ID", call.ToolCallID)
	detail("Cell", call.CellID)
	detail("Requested", displayTime(call.CreatedAt))
	b.WriteString(`</div><div class="label">Raw params</div><pre>`)
	b.WriteString(escapeText(call.ParamsInspect))
	b.WriteString(`</pre></details></div>`)
}

func approvalSummaryFields(call daemon.PendingApprovalSnapshot) [][2]string {
	if strings.TrimSpace(call.Presentation) == "" {
		return nil
	}
	var presentation struct {
		Blocks []struct {
			Type   string `json:"type"`
			Fields []struct {
				Label string `json:"label"`
				Value string `json:"value"`
			} `json:"fields"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal([]byte(call.Presentation), &presentation); err != nil {
		return nil
	}
	var out [][2]string
	for _, block := range presentation.Blocks {
		if block.Type != "fields" {
			continue
		}
		for _, field := range block.Fields {
			out = append(out, [2]string{field.Label, field.Value})
			if len(out) == 4 {
				return out
			}
		}
	}
	return out
}

func countConsoleSessionCalls(session approvalConsoleSession) int {
	total := 0
	for _, group := range session.PackageGroups {
		total += len(group.ToolCalls)
	}
	return total
}

func displayTime(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "recently"
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed.Local().Format("Jan 2 15:04:05")
	}
	return raw
}

func titleCase(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func escapeText(s string) string {
	return template.HTMLEscapeString(s)
}

func escapeAttr(s string) string {
	return template.HTMLEscapeString(s)
}

func writeSSEHTML(w io.Writer, html string) {
	_, _ = io.WriteString(w, "event: html\n")
	for _, line := range strings.Split(html, "\n") {
		_, _ = fmt.Fprintf(w, "data: %s\n", line)
	}
	_, _ = io.WriteString(w, "\n")
}

func startDaemonDebugServer(stderr io.Writer, shutdown func(), control daemonHTTPControl) (func() error, string, error) {
	listener, err := listenDaemonDebugListener()
	if err != nil {
		return nil, "", err
	}
	return serveDaemonDebugServer(listener, stderr, shutdown, control)
}

func listenDaemonDebugListener() (net.Listener, error) {
	bindAddress, _ := daemonBindAddress()
	listener, err := net.Listen("tcp", bindAddress)
	if err != nil {
		return nil, fmt.Errorf("listen on daemon debug address %s: %w", bindAddress, err)
	}
	return listener, nil
}

func serveDaemonDebugServer(listener net.Listener, stderr io.Writer, shutdown func(), control daemonHTTPControl) (func() error, string, error) {
	if listener == nil {
		return nil, "", errors.New("daemon debug listener is nil")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" && r.URL.Path != "/approval-console" {
			http.NotFound(w, r)
			return
		}

		page := daemonIndexPageData{StatusText: "unavailable"}
		if control != nil {
			locked, err := control.SecretStoreLocked(r.Context())
			if err == nil {
				page.Available = true
				page.Locked = locked
				if locked {
					page.StatusText = "locked"
				} else {
					page.StatusText = "unlocked"
				}
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := renderDaemonIndex(w, page); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("/approval-console/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(approvalConsoleStateForHTTP(control)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("/approval-console/html", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, renderApprovalConsoleHTML(approvalConsoleStateForHTTP(control)))
	})
	mux.HandleFunc("/approval-console/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		last := ""
		sendHTML := func() {
			state := approvalConsoleStateForHTTP(control)
			stable := state
			stable.ServerTime = ""
			stablePayload, err := json.Marshal(stable)
			if err != nil {
				return
			}
			if string(stablePayload) == last {
				return
			}
			last = string(stablePayload)
			writeSSEHTML(w, renderApprovalConsoleHTML(state))
			flusher.Flush()
		}
		sendHTML()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				sendHTML()
			}
		}
	})
	mux.HandleFunc("/approval-console/decisions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if !isJSONRequest(r) {
			http.Error(w, http.StatusText(http.StatusUnsupportedMediaType), http.StatusUnsupportedMediaType)
			return
		}
		var req approvalConsoleDecisionRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(applyApprovalConsoleDecisions(r.Context(), control, req)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "pong")
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		payload := r.URL.Query().Get("payload")
		if payload == "" && r.Body != nil {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			payload = string(body)
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, payload)
	})
	mux.HandleFunc("/clients", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		snapshot := []daemon.ClientSnapshot(nil)
		if control != nil {
			snapshot = control.Clients()
		}
		if err := json.NewEncoder(w).Encode(snapshot); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("/approvals", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		groups := pendingApprovalGroupsForHTTP(control)
		if err := json.NewEncoder(w).Encode(groups); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("/approvals/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		last := ""
		sendSnapshot := func() {
			groups := pendingApprovalGroupsForHTTP(control)
			payload, err := json.Marshal(groups)
			if err != nil {
				return
			}
			if string(payload) == last {
				return
			}
			last = string(payload)
			_, _ = fmt.Fprintf(w, "event: approvals\ndata: %s\n\n", payload)
			flusher.Flush()
		}

		sendSnapshot()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				sendSnapshot()
			}
		}
	})
	mux.HandleFunc("/approvals/apply", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if control == nil {
			http.Error(w, "approvals unavailable", http.StatusServiceUnavailable)
			return
		}
		if !isJSONRequest(r) {
			http.Error(w, http.StatusText(http.StatusUnsupportedMediaType), http.StatusUnsupportedMediaType)
			return
		}
		var req approvalApplyRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := control.ApplyApprovals(r.Context(), toDaemonApprovalDecisions(req.Approvals)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/approvals/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/approve") && !strings.HasSuffix(r.URL.Path, "/reject") {
			http.NotFound(w, r)
			return
		}
		reject := strings.HasSuffix(r.URL.Path, "/reject")
		suffix := "/approve"
		if reject {
			suffix = "/reject"
		}
		groupID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/approvals/"), suffix)
		groupID = strings.Trim(groupID, "/")
		if groupID == "" {
			http.Error(w, "missing approval group id", http.StatusBadRequest)
			return
		}
		decodedGroupID, err := url.PathUnescape(groupID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if control == nil {
			http.Error(w, "approvals unavailable", http.StatusServiceUnavailable)
			return
		}
		group, ok := findPendingApprovalGroup(control, decodedGroupID)
		if !ok {
			http.Error(w, "approval group not found", http.StatusNotFound)
			return
		}
		decisions := make([]approvalApplyDecisionRequest, 0, len(group.ToolCalls))
		if reject {
			if !isJSONRequest(r) {
				http.Error(w, http.StatusText(http.StatusUnsupportedMediaType), http.StatusUnsupportedMediaType)
				return
			}
			var req approvalRejectRequest
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			for _, call := range group.ToolCalls {
				decisions = append(decisions, approvalApplyDecisionRequest{
					ID:       call.ToolCallID,
					Approved: false,
					Reason:   req.Message,
				})
			}
		} else {
			for _, call := range group.ToolCalls {
				decisions = append(decisions, approvalApplyDecisionRequest{
					ID:       call.ToolCallID,
					Approved: true,
				})
			}
		}
		if err := control.ApplyApprovals(r.Context(), toDaemonApprovalDecisions(decisions)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/approval-tool-calls/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/approve") && !strings.HasSuffix(r.URL.Path, "/reject") {
			http.NotFound(w, r)
			return
		}
		reject := strings.HasSuffix(r.URL.Path, "/reject")
		suffix := "/approve"
		if reject {
			suffix = "/reject"
		}
		toolCallID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/approval-tool-calls/"), suffix)
		toolCallID = strings.Trim(toolCallID, "/")
		if toolCallID == "" {
			http.Error(w, "missing approval tool call id", http.StatusBadRequest)
			return
		}
		decodedToolCallID, err := url.PathUnescape(toolCallID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if control == nil {
			http.Error(w, "approvals unavailable", http.StatusServiceUnavailable)
			return
		}
		decision := approvalApplyDecisionRequest{ID: decodedToolCallID, Approved: !reject}
		if reject {
			if !isJSONRequest(r) {
				http.Error(w, http.StatusText(http.StatusUnsupportedMediaType), http.StatusUnsupportedMediaType)
				return
			}
			var req approvalRejectRequest
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			decision.Reason = req.Message
		}
		if err := control.ApplyApprovals(r.Context(), toDaemonApprovalDecisions([]approvalApplyDecisionRequest{decision})); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/secret-store/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if control == nil {
			http.Error(w, "secret store unavailable", http.StatusServiceUnavailable)
			return
		}
		locked, err := control.SecretStoreLocked(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeSecretStoreStatus(w, locked)
	})
	mux.HandleFunc("/secret-store/unlock", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if control == nil {
			http.Error(w, "secret store unavailable", http.StatusServiceUnavailable)
			return
		}
		if !isJSONRequest(r) {
			http.Error(w, http.StatusText(http.StatusUnsupportedMediaType), http.StatusUnsupportedMediaType)
			return
		}
		var req secretStoreUnlockRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := control.UnlockSecretStore(r.Context(), req.UnlockKey); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeSecretStoreStatus(w, false)
	})
	mux.HandleFunc("/secret-store/lock", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if control == nil {
			http.Error(w, "secret store unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := control.LockSecretStore(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeSecretStoreStatus(w, true)
	})
	if daemonAdminEndpointsEnabled() {
		mux.HandleFunc("/exit", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "shutting down")
			if shutdown != nil {
				go shutdown()
			}
		})
		mux.HandleFunc("/kill", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "killing process")
			go func() {
				time.Sleep(50 * time.Millisecond)
				daemonHardExit()
			}()
		})
	}

	httpServer := &http.Server{Handler: mux}
	var closeOnce sync.Once
	closeServer := func() error {
		var closeErr error
		closeOnce.Do(func() {
			closeErr = httpServer.Close()
		})
		return closeErr
	}
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && stderr != nil {
			_, _ = fmt.Fprintf(stderr, "toolbox daemon debug server error: %v\n", err)
		}
	}()

	return closeServer, listener.Addr().String(), nil
}

func isJSONRequest(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

func writeSecretStoreStatus(w http.ResponseWriter, locked bool) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(secretStoreStatusResponse{Locked: locked})
}

func runDaemonPing(cmd daemonPingCmd, stdout io.Writer) error {
	client, err := daemon.EnsureConnection()
	if err != nil {
		return err
	}
	defer client.Close()

	resp, err := client.Ping()
	if err != nil {
		return err
	}

	if !cmd.PID {
		_, err = fmt.Fprintln(stdout, resp.Payload)
		return err
	}

	_, err = fmt.Fprintf(stdout, "%s %d\n", resp.Payload, resp.PID)
	return err
}

func runDaemonStop(stdout, stderr io.Writer) error {
	pids, err := daemonStopAll(func(format string, args ...any) {
		if stderr == nil {
			return
		}
		_, _ = fmt.Fprintf(stderr, format+"\n", args...)
	})
	if err != nil {
		return err
	}
	if len(pids) == 0 {
		_, err = fmt.Fprintln(stdout, "no running toolbox daemons")
		return err
	}

	parts := make([]string, 0, len(pids))
	for _, pid := range pids {
		parts = append(parts, strconv.Itoa(pid))
	}
	_, err = fmt.Fprintf(stdout, "stopped toolbox daemons: %s\n", strings.Join(parts, " "))
	return err
}
