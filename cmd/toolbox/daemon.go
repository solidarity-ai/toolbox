package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
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

type approvalConsoleSubmitRequest struct {
	Session string            `json:"session"`
	Drafts  map[string]string `json:"drafts"`
	Reason  string            `json:"reason"`
}

type approvalConsoleSubmitResult struct {
	Accepted int
	Errors   []string
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

func renderDaemonIndex(w io.Writer, state approvalConsoleState) error {
	return ApprovalConsolePage(approvalConsolePageDataFromState(state), state).Render(context.Background(), w)
}

func approvalSummaryFields(call daemon.PendingApprovalSnapshot) [][2]string {
	presentation, ok := approvalPresentationForConsole(call)
	if !ok {
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

type approvalConsolePresentation struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Icon        *struct {
		Type            string `json:"type"`
		MIMEType        string `json:"mime_type"`
		MIMETypeCamel   string `json:"mimeType"`
		DataBase64      string `json:"data_base64"`
		DataBase64Camel string `json:"dataBase64"`
		Alt             string `json:"alt"`
	} `json:"icon"`
	Blocks []struct {
		Type   string `json:"type"`
		Fields []struct {
			Label string `json:"label"`
			Value string `json:"value"`
		} `json:"fields"`
	} `json:"blocks"`
}

func approvalPresentationForConsole(call daemon.PendingApprovalSnapshot) (approvalConsolePresentation, bool) {
	var presentation approvalConsolePresentation
	if strings.TrimSpace(call.Presentation) == "" {
		return presentation, false
	}
	if err := json.Unmarshal([]byte(call.Presentation), &presentation); err != nil {
		return presentation, false
	}
	return presentation, true
}

func approvalDisplayTitle(call daemon.PendingApprovalSnapshot) string {
	if presentation, ok := approvalPresentationForConsole(call); ok {
		if title := strings.TrimSpace(presentation.Title); title != "" {
			return title
		}
	}
	return firstNonEmpty(call.ToolLabel, call.ToolName)
}

func approvalDisplayDescription(call daemon.PendingApprovalSnapshot) string {
	if presentation, ok := approvalPresentationForConsole(call); ok {
		if description := strings.TrimSpace(presentation.Description); description != "" {
			return description
		}
	}
	return call.Description
}

func approvalDisplayIconDataURI(call daemon.PendingApprovalSnapshot) string {
	presentation, ok := approvalPresentationForConsole(call)
	if !ok || presentation.Icon == nil {
		return ""
	}
	if iconType := strings.TrimSpace(presentation.Icon.Type); iconType != "" && iconType != "image" {
		return ""
	}
	mimeType := strings.TrimSpace(firstNonEmpty(presentation.Icon.MIMEType, presentation.Icon.MIMETypeCamel))
	if !strings.EqualFold(mimeType, "image/png") {
		return ""
	}
	raw := strings.TrimSpace(firstNonEmpty(presentation.Icon.DataBase64, presentation.Icon.DataBase64Camel))
	if raw == "" || len(raw) > 64*1024 {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return ""
	}
	pngSignature := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if len(decoded) < len(pngSignature) || !bytes.Equal(decoded[:len(pngSignature)], pngSignature) {
		return ""
	}
	return "data:image/png;base64," + raw
}

func approvalDisplayIconAlt(call daemon.PendingApprovalSnapshot) string {
	if presentation, ok := approvalPresentationForConsole(call); ok && presentation.Icon != nil {
		return strings.TrimSpace(presentation.Icon.Alt)
	}
	return ""
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
	mux.HandleFunc("/assets/datastar-v1.0.1.js", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write(datastarJS)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" && r.URL.Path != "/approval-console" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := renderDaemonIndex(w, approvalConsoleStateForHTTP(control)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("/approval-console/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		writeDatastarHeaders(w)

		writeDatastarPatchSignals(w, map[string]any{"liveState": "Live"})
		var previous *approvalConsoleFragments
		sendPatches := func() {
			next := writeApprovalConsolePatches(w, previous, approvalConsoleStateForHTTP(control))
			previous = &next
			flusher.Flush()
		}
		sendPatches()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				sendPatches()
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
		var req approvalConsoleSubmitRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeDatastarHeaders(w)
		result := applyApprovalConsoleDrafts(r.Context(), control, req)
		writeDatastarPatchSignals(w, map[string]any{
			"liveState":        "Live",
			"drafts":           approvalConsoleDraftResetPatch(req.Drafts),
			"rejectReason":     "",
			"rejectDialogOpen": false,
			"rejectSession":    "",
			"message":          approvalConsoleErrorMessage(result),
		})
		writeApprovalConsolePatches(w, nil, approvalConsoleStateForHTTP(control))
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
			if isDatastarRequest(r) {
				writeApprovalConsoleDatastarResponse(w, control, map[string]any{"message": "secret store unavailable"})
				return
			}
			http.Error(w, "secret store unavailable", http.StatusServiceUnavailable)
			return
		}
		req, status, err := readSecretStoreUnlockRequest(r)
		if err != nil {
			if isDatastarRequest(r) {
				writeApprovalConsoleDatastarResponse(w, control, map[string]any{"message": err.Error()})
				return
			}
			http.Error(w, err.Error(), status)
			return
		}
		if err := control.UnlockSecretStore(r.Context(), req.UnlockKey); err != nil {
			if isDatastarRequest(r) {
				writeApprovalConsoleDatastarResponse(w, control, map[string]any{"message": err.Error()})
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if isDatastarRequest(r) {
			writeApprovalConsoleDatastarResponse(w, control, map[string]any{"message": "", "unlockKey": ""})
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
			if isDatastarRequest(r) {
				writeApprovalConsoleDatastarResponse(w, control, map[string]any{"message": "secret store unavailable"})
				return
			}
			http.Error(w, "secret store unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := control.LockSecretStore(r.Context()); err != nil {
			if isDatastarRequest(r) {
				writeApprovalConsoleDatastarResponse(w, control, map[string]any{"message": err.Error()})
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if isDatastarRequest(r) {
			writeApprovalConsoleDatastarResponse(w, control, map[string]any{"message": ""})
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
	return requestMediaType(r) == "application/json"
}

func requestMediaType(r *http.Request) string {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return mediaType
}

func readSecretStoreUnlockRequest(r *http.Request) (secretStoreUnlockRequest, int, error) {
	req := secretStoreUnlockRequest{}
	if isJSONRequest(r) {
		var payload struct {
			UnlockKey      string `json:"unlock_key"`
			CamelUnlockKey string `json:"unlockKey"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); err != nil {
			return secretStoreUnlockRequest{}, http.StatusBadRequest, err
		}
		req.UnlockKey = payload.UnlockKey
		if req.UnlockKey == "" {
			req.UnlockKey = payload.CamelUnlockKey
		}
		return validateSecretStoreUnlockRequest(req)
	}
	if isDatastarRequest(r) {
		switch requestMediaType(r) {
		case "application/x-www-form-urlencoded":
			if err := r.ParseForm(); err != nil {
				return secretStoreUnlockRequest{}, http.StatusBadRequest, err
			}
			req.UnlockKey = r.FormValue("unlock_key")
			return validateSecretStoreUnlockRequest(req)
		case "multipart/form-data":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				return secretStoreUnlockRequest{}, http.StatusBadRequest, err
			}
			req.UnlockKey = r.FormValue("unlock_key")
			return validateSecretStoreUnlockRequest(req)
		}
	}
	return secretStoreUnlockRequest{}, http.StatusUnsupportedMediaType, fmt.Errorf("%s", http.StatusText(http.StatusUnsupportedMediaType))
}

func validateSecretStoreUnlockRequest(req secretStoreUnlockRequest) (secretStoreUnlockRequest, int, error) {
	if req.UnlockKey == "" {
		return secretStoreUnlockRequest{}, http.StatusBadRequest, errors.New("unlock key is empty")
	}
	return req, http.StatusOK, nil
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
