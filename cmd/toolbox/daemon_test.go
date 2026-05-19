package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/daemon"
	"github.com/solidarity-ai/toolbox/secrets"
)

type stubDaemonHTTPControl struct {
	mu                sync.Mutex
	locked            bool
	unlockKey         string
	unlockErr         error
	setupErr          error
	lockErr           error
	statusErr         error
	setupRequired     bool
	recoveryUnlocked  bool
	recoveryUnlockKey string
	recoveryGenerated int
	snapshots         []daemon.ClientSnapshot
	decisions         []daemon.ApprovalDecision
}

type stubBrowserLauncher struct {
	enabled bool
	open    func(string) error
}

func (s stubBrowserLauncher) Enabled() bool {
	return s.enabled
}

func (s stubBrowserLauncher) Open(url string) error {
	if s.open == nil {
		return nil
	}
	return s.open(url)
}

func (s *stubDaemonHTTPControl) Clients() []daemon.ClientSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]daemon.ClientSnapshot(nil), s.snapshots...)
}

func (s *stubDaemonHTTPControl) UnlockSecretStore(_ context.Context, unlockKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unlockErr != nil {
		return s.unlockErr
	}
	s.unlockKey = unlockKey
	s.locked = false
	return nil
}

func (s *stubDaemonHTTPControl) SetupSecretStore(_ context.Context, unlockKey string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setupErr != nil {
		return nil, s.setupErr
	}
	s.unlockKey = unlockKey
	s.locked = false
	s.setupRequired = false
	return []string{"AAAAA-BBBBB-CCCCC-DDDDD", "EEEEE-FFFFF-GGGGG-HHHHH"}, nil
}

func (s *stubDaemonHTTPControl) GenerateSecretStoreRecoveryCodes(_ context.Context, unlockKey string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setupErr != nil {
		return nil, s.setupErr
	}
	if unlockKey == "" && !s.recoveryUnlocked {
		return nil, secrets.ErrLocked
	}
	s.recoveryUnlockKey = unlockKey
	s.recoveryGenerated++
	return []string{"IIIII-JJJJJ-KKKKK-LLLLL", "MMMMM-NNNNN-OOOOO-PPPPP"}, nil
}

func (s *stubDaemonHTTPControl) SecretStoreRecoveryUnlocked(context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recoveryUnlocked, nil
}

func (s *stubDaemonHTTPControl) LockSecretStore(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lockErr != nil {
		return s.lockErr
	}
	s.locked = true
	return nil
}

func (s *stubDaemonHTTPControl) SecretStoreLocked(context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statusErr != nil {
		return false, s.statusErr
	}
	return s.locked, nil
}

func (s *stubDaemonHTTPControl) SecretStoreSetupRequired(context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setupRequired, nil
}

func (s *stubDaemonHTTPControl) ApplyApprovals(_ context.Context, decisions []daemon.ApprovalDecision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, decision := range decisions {
		s.decisions = append(s.decisions, decision)
		for i := range s.snapshots {
			filtered := s.snapshots[i].PendingApprovals[:0]
			for _, approval := range s.snapshots[i].PendingApprovals {
				if approval.ToolCallID != decision.ToolCallID {
					filtered = append(filtered, approval)
				}
			}
			s.snapshots[i].PendingApprovals = filtered
		}
	}
	return nil
}

func TestMaybeAutoOpenDaemonBrowserLocked(t *testing.T) {
	prevLauncher := daemonBrowserLauncher
	defer func() {
		daemonBrowserLauncher = prevLauncher
	}()

	opened := make(chan string, 1)
	daemonBrowserLauncher = stubBrowserLauncher{
		enabled: true,
		open: func(url string) error {
			opened <- url
			return nil
		},
	}

	maybeAutoOpenDaemonBrowser(io.Discard, &stubDaemonHTTPControl{locked: true}, "127.0.0.1:7113")

	select {
	case got := <-opened:
		if got != "http://127.0.0.1:7113/" {
			t.Fatalf("opened url = %q, want %q", got, "http://127.0.0.1:7113/")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("daemon browser was not opened")
	}
}

func TestDefaultDaemonBrowserAutoOpenEnabledIsFalseInTests(t *testing.T) {
	if defaultDaemonBrowserAutoOpenEnabled() {
		t.Fatal("defaultDaemonBrowserAutoOpenEnabled() = true in tests, want false")
	}
}

func TestMaybeAutoOpenDaemonBrowserSkipsWhenDisabled(t *testing.T) {
	prevLauncher := daemonBrowserLauncher
	defer func() {
		daemonBrowserLauncher = prevLauncher
	}()

	called := make(chan struct{}, 1)
	daemonBrowserLauncher = stubBrowserLauncher{
		enabled: false,
		open: func(string) error {
			called <- struct{}{}
			return nil
		},
	}

	maybeAutoOpenDaemonBrowser(io.Discard, &stubDaemonHTTPControl{locked: true}, "127.0.0.1:7113")

	select {
	case <-called:
		t.Fatal("daemon browser opened even though auto-open was disabled")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestMaybeAutoOpenDaemonBrowserSkipsWhenUnlocked(t *testing.T) {
	prevLauncher := daemonBrowserLauncher
	defer func() {
		daemonBrowserLauncher = prevLauncher
	}()

	called := make(chan struct{}, 1)
	daemonBrowserLauncher = stubBrowserLauncher{
		enabled: true,
		open: func(string) error {
			called <- struct{}{}
			return nil
		},
	}

	maybeAutoOpenDaemonBrowser(io.Discard, &stubDaemonHTTPControl{locked: false}, "127.0.0.1:7113")

	select {
	case <-called:
		t.Fatal("daemon browser opened even though secret store was unlocked")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestMaybeAutoOpenDaemonBrowserLogsFailureWithoutReturningError(t *testing.T) {
	prevLauncher := daemonBrowserLauncher
	defer func() {
		daemonBrowserLauncher = prevLauncher
	}()

	var stderr lockedBuffer
	daemonBrowserLauncher = stubBrowserLauncher{
		enabled: true,
		open: func(string) error {
			return errors.New("boom")
		},
	}

	maybeAutoOpenDaemonBrowser(&stderr, &stubDaemonHTTPControl{locked: true}, "127.0.0.1:7113")

	if got := waitForBufferSubstring(t, &stderr, "toolbox daemon browser launch error: boom"); !strings.Contains(got, "toolbox daemon browser launch error: boom") {
		t.Fatalf("stderr = %q, want browser launch error", got)
	}
}

func TestDaemonBindAddress(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "")

	if got, explicit := daemonBindAddress(); got != defaultDaemonBindAddress || explicit {
		t.Fatalf("daemonBindAddress() = (%q, %t), want (%q, false)", got, explicit, defaultDaemonBindAddress)
	}

	t.Setenv(daemonBindAddressEnv, "127.0.0.1:9555")
	if got, explicit := daemonBindAddress(); got != "127.0.0.1:9555" || !explicit {
		t.Fatalf("daemonBindAddress() with env = (%q, %t), want (%q, true)", got, explicit, "127.0.0.1:9555")
	}
}

func TestStartDaemonDebugServerServesPingAndEcho(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	var stderr bytes.Buffer
	closeServer, addr, err := startDaemonDebugServer(&stderr, nil, nil)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	if closeServer == nil {
		t.Fatal("closeServer = nil, want close function")
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()
	if addr == "" {
		t.Fatal("addr = empty, want listener address")
	}

	resp, err := http.Get("http://" + addr + "/ping")
	if err != nil {
		t.Fatalf("GET /ping: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/ping): %v", err)
	}
	if string(body) != "pong" {
		t.Fatalf("/ping body = %q, want pong", string(body))
	}

	resp, err = http.Get("http://" + addr + "/echo?payload=hello")
	if err != nil {
		t.Fatalf("GET /echo: %v", err)
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/echo): %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("/echo body = %q, want hello", string(body))
	}
}

func TestStartDaemonDebugServerServesApprovalSnapshotAndApproveEndpoint(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	control := &stubDaemonHTTPControl{
		snapshots: []daemon.ClientSnapshot{{
			PID:        41,
			Mode:       "codemode_repl",
			WorkingDir: "/tmp/work",
			PendingApprovals: []daemon.PendingApprovalSnapshot{
				{ToolCallID: "tc-1", TBSession: "abc123", ToolName: "issues.get", ParamsInspect: `{id: "I-1", meta: {team: {owner: {name: "alpha"}}}}`},
				{ToolCallID: "tc-2", TBSession: "abc123", ToolName: "issues.get", ParamsInspect: `{id: "I-2", meta: {team: {owner: {name: "beta"}}}}`},
			},
		}},
	}

	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, control)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	resp, err := http.Get("http://" + addr + "/approvals")
	if err != nil {
		t.Fatalf("GET /approvals: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.StatusCode; got != http.StatusOK {
		t.Fatalf("GET /approvals status = %d, want %d", got, http.StatusOK)
	}
	var groups []daemonHTTPApprovalGroup
	if err := json.NewDecoder(resp.Body).Decode(&groups); err != nil {
		t.Fatalf("Decode(/approvals): %v", err)
	}
	if len(groups) != 1 || groups[0].ID != "tb_session:abc123" {
		t.Fatalf("GET /approvals groups = %#v", groups)
	}
	if !strings.Contains(groups[0].ToolCalls[0].ParamsInspect, `owner: {name: "alpha"}`) {
		t.Fatalf("GET /approvals params = %#v, want deep params", groups[0].ToolCalls)
	}

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/approval-tool-calls/tc-1/approve", nil)
	if err != nil {
		t.Fatalf("NewRequest(approve call): %v", err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /approval-tool-calls/.../approve: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.StatusCode; got != http.StatusNoContent {
		t.Fatalf("POST /approval-tool-calls/.../approve status = %d, want %d", got, http.StatusNoContent)
	}

	req, err = http.NewRequest(http.MethodPost, "http://"+addr+"/approvals/tb_session:abc123/reject", strings.NewReader(`{"message":"blocked"}`))
	if err != nil {
		t.Fatalf("NewRequest(reject group): %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /approvals/.../reject: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.StatusCode; got != http.StatusNoContent {
		t.Fatalf("POST /approvals/.../reject status = %d, want %d", got, http.StatusNoContent)
	}

	control.mu.Lock()
	defer control.mu.Unlock()
	if len(control.decisions) != 2 {
		t.Fatalf("decisions = %#v, want 2 decisions", control.decisions)
	}
	if control.decisions[0].Action != daemon.ApprovalActionApprove || control.decisions[0].ToolCallID != "tc-1" {
		t.Fatalf("first decision = %#v, want approve tc-1", control.decisions[0])
	}
	if control.decisions[1].Action != daemon.ApprovalActionReject || control.decisions[1].ToolCallID != "tc-2" || control.decisions[1].Message != "blocked" {
		t.Fatalf("second decision = %#v, want reject tc-2 with message", control.decisions[1])
	}
	if len(control.snapshots) != 1 || len(control.snapshots[0].PendingApprovals) != 0 {
		t.Fatalf("remaining approvals = %#v, want none", control.snapshots)
	}
}

func TestApprovalConsoleEventsUseDatastarPatches(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	control := &stubDaemonHTTPControl{
		locked: false,
		snapshots: []daemon.ClientSnapshot{{
			PID:              41,
			Mode:             "codemode",
			BoundTBSession:   "abc123",
			IntentText:       "Review pending mail",
			IntentSource:     "user",
			IntentUpdatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
			WorkingDir:       "/tmp/work",
			PendingApprovals: []daemon.PendingApprovalSnapshot{{ToolCallID: "tc-1", TBSession: "abc123", ToolName: "gmail.messages.send", ParamsInspect: `{to: "joe@example.com"}`}},
		}},
	}

	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, control)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/approval-console/events", nil)
	if err != nil {
		t.Fatalf("NewRequest(/approval-console/events): %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /approval-console/events: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}

	reader := bufio.NewReader(resp.Body)
	var lines []string
	for len(lines) < 40 {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("ReadString(/approval-console/events): %v", err)
		}
		lines = append(lines, line)
		if strings.Contains(strings.Join(lines, ""), "event: datastar-patch-elements") {
			break
		}
	}
	payload := strings.Join(lines, "")
	if strings.Contains(payload, "event: html") {
		t.Fatalf("events used old html event: %q", payload)
	}
	if !strings.Contains(payload, "event: datastar-patch-signals") {
		t.Fatalf("events missing Datastar signal patch: %q", payload)
	}
	if !strings.Contains(payload, "event: datastar-patch-elements") {
		t.Fatalf("events missing Datastar element patch: %q", payload)
	}
}

func TestApprovalConsoleDecisionsSubmitDraftBatch(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	control := &stubDaemonHTTPControl{
		snapshots: []daemon.ClientSnapshot{{
			PID:            41,
			Mode:           "codemode",
			BoundTBSession: "abc123",
			WorkingDir:     "/tmp/work",
			PendingApprovals: []daemon.PendingApprovalSnapshot{
				{ToolCallID: "tc-1", TBSession: "abc123", ToolName: "gmail.messages.send", ParamsInspect: `{to: "joe@example.com"}`},
				{ToolCallID: "tc-2", TBSession: "abc123", ToolName: "gmail.messages.send", ParamsInspect: `{to: "ann@example.com"}`},
				{ToolCallID: "tc-3", TBSession: "abc123", ToolName: "gmail.messages.search", ParamsInspect: `{query: "from:ann"}`},
			},
		}},
	}

	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, control)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	body := strings.NewReader(`{"session":"abc123","drafts":{"tc-1":"approve","tc-2":"reject","tc-3":"leave"},"reason":"blocked"}`)
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/approval-console/decisions", body)
	if err != nil {
		t.Fatalf("NewRequest(/approval-console/decisions): %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Datastar-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /approval-console/decisions: %v", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/approval-console/decisions): %v", err)
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if !strings.Contains(string(payload), "event: datastar-patch-signals") || !strings.Contains(string(payload), "event: datastar-patch-elements") {
		t.Fatalf("decision response missing Datastar patches: %q", string(payload))
	}
	if !strings.Contains(string(payload), `"liveState":"Connected"`) {
		t.Fatalf("decision response did not restore connected state: %q", string(payload))
	}
	if !strings.Contains(string(payload), `"drafts":{"tc-1":"leave","tc-2":"leave","tc-3":"leave"}`) {
		t.Fatalf("decision response did not reset submitted draft keys to the neutral state: %q", string(payload))
	}

	control.mu.Lock()
	defer control.mu.Unlock()
	if len(control.decisions) != 2 {
		t.Fatalf("decisions = %#v, want 2 decisions", control.decisions)
	}
	if control.decisions[0].Action != daemon.ApprovalActionApprove || control.decisions[0].ToolCallID != "tc-1" {
		t.Fatalf("first decision = %#v, want approve tc-1", control.decisions[0])
	}
	if control.decisions[1].Action != daemon.ApprovalActionReject || control.decisions[1].ToolCallID != "tc-2" || control.decisions[1].Message != "blocked" {
		t.Fatalf("second decision = %#v, want reject tc-2 with reason", control.decisions[1])
	}
}

func TestApprovalConsoleBlocksCredentialApprovalWhenSecretStoreLocked(t *testing.T) {
	control := &stubDaemonHTTPControl{
		locked: true,
		snapshots: []daemon.ClientSnapshot{{
			BoundTBSession: "abc123",
			PendingApprovals: []daemon.PendingApprovalSnapshot{{
				ToolCallID:          "tc-1",
				TBSession:           "abc123",
				ToolName:            "gmail.send",
				FullToolName:        "google-workspace.gmail.send",
				RequiresCredentials: true,
			}},
		}},
	}

	result := applyApprovalConsoleDrafts(context.Background(), control, approvalConsoleSubmitRequest{
		Session: "abc123",
		Drafts:  map[string]string{"tc-1": "approve"},
	})
	if result.Accepted != 0 {
		t.Fatalf("accepted = %d, want 0", result.Accepted)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "secret store locked") {
		t.Fatalf("errors = %#v, want secret-store locked error", result.Errors)
	}
	if len(control.decisions) != 0 {
		t.Fatalf("decisions = %#v, want none", control.decisions)
	}

	result = applyApprovalConsoleDrafts(context.Background(), control, approvalConsoleSubmitRequest{
		Session: "abc123",
		Drafts:  map[string]string{"tc-1": "reject"},
		Reason:  "blocked",
	})
	if result.Accepted != 1 || len(result.Errors) != 0 {
		t.Fatalf("reject result = %#v, want accepted reject", result)
	}
	if len(control.decisions) != 1 || control.decisions[0].Action != daemon.ApprovalActionReject {
		t.Fatalf("decisions = %#v, want one reject", control.decisions)
	}
}

func TestApprovalConsoleDetailsPreserveOpenAttribute(t *testing.T) {
	state := approvalConsoleState{
		Summary: approvalConsoleSummary{ActiveClients: 1, PendingApprovals: 1},
		Sessions: []approvalConsoleSession{{
			TBSession: "abc123",
			Active:    true,
			Intent:    approvalConsoleIntent{Text: "Review pending mail"},
			PackageGroups: []approvalConsolePackageGroup{{
				PackageKey:   "gmail",
				PackageLabel: "Gmail",
				ToolCalls: []daemon.PendingApprovalSnapshot{{
					ToolCallID:    "tc-1",
					ToolName:      "gmail.messages.send",
					ToolLabel:     "messages.send",
					Description:   "Send Gmail message.",
					ParamsInspect: `{to: "joe@example.com"}`,
				}},
			}},
		}},
	}
	html := componentHTML(ApprovalMain(state))
	if !strings.Contains(html, `data-preserve-attr="open"`) {
		t.Fatalf("approval main missing details open preservation: %q", html)
	}
	if !strings.Contains(html, `id="approval-row-`) || !strings.Contains(html, `id="approval-details-`) {
		t.Fatalf("approval main missing stable row/details ids: %q", html)
	}
	if !strings.Contains(html, `<pre>{to: &#34;joe@example.com&#34;}</pre>`) {
		t.Fatalf("approval main missing escaped raw params: %q", html)
	}
}

func TestApprovalConsoleUsesPresentationTitleAndDescription(t *testing.T) {
	const testPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII="
	state := approvalConsoleState{
		Summary: approvalConsoleSummary{ActiveClients: 1, PendingApprovals: 1},
		Sessions: []approvalConsoleSession{{
			TBSession: "abc123",
			Active:    true,
			Intent:    approvalConsoleIntent{Text: "Review pending mail"},
			PackageGroups: []approvalConsolePackageGroup{{
				PackageKey:   "gmail",
				PackageLabel: "Gmail",
				ToolCalls: []daemon.PendingApprovalSnapshot{{
					ToolCallID:  "tc-1",
					ToolName:    "gmail.send",
					ToolLabel:   "gmail.send",
					Description: "Package fallback description.",
					Presentation: `{
						"schema":"toolbox.approval.presentation.v1",
						"title":"Send email",
						"description":"Send Gmail message.",
						"icon":{"type":"image","mime_type":"image/png","data_base64":"` + testPNGBase64 + `","alt":"Gmail"},
						"blocks":[{"type":"fields","fields":[{"label":"To","value":"joe@example.com"}]}]
					}`,
				}},
			}},
		}},
	}
	html := componentHTML(ApprovalMain(state))
	if !strings.Contains(html, `<span class="tool-name-text">Send email</span>`) {
		t.Fatalf("approval main missing presentation title: %q", html)
	}
	if strings.Contains(html, `>gmail.send</span>`) {
		t.Fatalf("approval main used tool label instead of presentation title: %q", html)
	}
	if !strings.Contains(html, ">Send Gmail message.</div>") {
		t.Fatalf("approval main missing presentation description: %q", html)
	}
	if !strings.Contains(html, `class="tool-icon"`) ||
		!strings.Contains(html, `src="data:image/png;base64,`+testPNGBase64+`"`) ||
		!strings.Contains(html, `alt="Gmail"`) {
		t.Fatalf("approval main missing presentation icon: %q", html)
	}
}

func TestApprovalConsoleIgnoresInvalidPresentationIcon(t *testing.T) {
	call := daemon.PendingApprovalSnapshot{
		ToolCallID: "tc-1",
		ToolName:   "gmail.send",
		Presentation: `{
			"schema":"toolbox.approval.presentation.v1",
			"title":"Send email",
			"icon":{"type":"image","mime_type":"text/html","data_base64":"PGltZyBvbmxvYWQ9YWxlcnQoMSk+"}
		}`,
	}
	if got := approvalDisplayIconDataURI(call); got != "" {
		t.Fatalf("approval display icon accepted invalid icon: %q", got)
	}
}

func TestApprovalConsoleDecisionControlIsTwoWayToggle(t *testing.T) {
	state := approvalConsoleState{
		Summary: approvalConsoleSummary{ActiveClients: 1, PendingApprovals: 1},
		Sessions: []approvalConsoleSession{{
			TBSession: "abc123",
			Active:    true,
			Intent:    approvalConsoleIntent{Text: "Review pending mail"},
			PackageGroups: []approvalConsolePackageGroup{{
				PackageKey:   "gmail",
				PackageLabel: "Gmail",
				ToolCalls: []daemon.PendingApprovalSnapshot{{
					ToolCallID:    "tc-1",
					ToolName:      "gmail.messages.send",
					ToolLabel:     "messages.send",
					Description:   "Send Gmail message.",
					ParamsInspect: `{to: "joe@example.com"}`,
				}},
			}},
		}},
	}
	html := componentHTML(ApprovalMain(state))
	if strings.Contains(html, ">Leave</button>") {
		t.Fatalf("approval row still renders Leave decision: %q", html)
	}
	if !strings.Contains(html, ">Reject</button>") || !strings.Contains(html, ">Approve</button>") {
		t.Fatalf("approval row missing reject/approve toggles: %q", html)
	}
	if !strings.Contains(html, `? &#39;leave&#39; : &#34;reject&#34;`) || !strings.Contains(html, `? &#39;leave&#39; : &#34;approve&#34;`) {
		t.Fatalf("approval row missing stable neutral toggle state: %q", html)
	}
	if strings.Contains(html, `data-class:active`) {
		t.Fatalf("approval row should use documented Datastar data-class object syntax, not keyed shortcut syntax: %q", html)
	}
	if !strings.Contains(html, `data-class="{&#39;active&#39;:`) {
		t.Fatalf("approval row missing documented Datastar active class binding: %q", html)
	}
	if !strings.Contains(html, `Mark all approved`) {
		t.Fatalf("approval session missing draft-only mark-all link: %q", html)
	}
	if !strings.Contains(html, `Approve all`) || !strings.Contains(html, `@post(&#39;/approval-console/decisions&#39;`) {
		t.Fatalf("approval session missing direct approve-all menu action: %q", html)
	}
	if !strings.Contains(html, `Reject all`) || !strings.Contains(html, `$rejectDialogOpen = true`) {
		t.Fatalf("approval session missing reject-all dialog action: %q", html)
	}
}

func TestApprovalConsoleTopbarUsesCompactControls(t *testing.T) {
	state := approvalConsoleState{
		SecretStore: approvalConsoleSecretStore{Status: "locked"},
		Summary:     approvalConsoleSummary{ActiveClients: 1, PendingApprovals: 2},
		Clients: []approvalConsoleClient{{
			PID:              41,
			Mode:             "codemode",
			Host:             "Claude Code",
			ParentPID:        40,
			ParentCommand:    "claude",
			WorkingDir:       "/tmp/work",
			BoundTBSession:   "abc123",
			PendingApprovals: 2,
			PreparedTools:    17,
			ConnectedAt:      time.Now().UTC().Format(time.RFC3339Nano),
			LastSyncAt:       time.Now().UTC().Format(time.RFC3339Nano),
			Active:           true,
		}},
	}
	html := componentHTML(ApprovalTopbar(state))
	if strings.Contains(html, "Toolbox Approvals") {
		t.Fatalf("topbar should not render Approvals in the navbar: %q", html)
	}
	if !strings.Contains(html, `<svg class="brand-logo"`) {
		t.Fatalf("topbar missing compact Toolbox brand logo: %q", html)
	}
	if strings.Contains(html, "Settings") || strings.Contains(html, `id="secret-state"`) {
		t.Fatalf("topbar still renders old settings/secret text controls: %q", html)
	}
	if !strings.Contains(html, `id="live-state"`) || !strings.Contains(html, `data-text="$liveState"`) {
		t.Fatalf("topbar missing live dot tooltip state: %q", html)
	}
	if !strings.Contains(html, `class="topbar-menu client-popover"`) ||
		!strings.Contains(html, `aria-label="Connected clients"`) ||
		!strings.Contains(html, `Claude Code - PID 41`) ||
		!strings.Contains(html, `Host</div><div class="value">Claude Code`) ||
		!strings.Contains(html, `Parent</div><div class="value">claude - PID 40`) ||
		!strings.Contains(html, `Mode</div><div class="value">codemode`) ||
		!strings.Contains(html, `/tmp/work`) ||
		!strings.Contains(html, `abc123`) ||
		!strings.Contains(html, `2 pending`) ||
		!strings.Contains(html, `17 prepared`) {
		t.Fatalf("topbar missing client popover data: %q", html)
	}
	if !strings.Contains(html, `class="brand-pending"`) ||
		!strings.Contains(html, `aria-label="Pending approvals"`) ||
		!strings.Contains(html, `>2</span>`) {
		t.Fatalf("topbar missing brand pending count pill: %q", html)
	}
	if strings.Contains(html, `id="pending-count"`) {
		t.Fatalf("topbar still renders separate right-side pending count: %q", html)
	}
	if !strings.Contains(html, `class="icon-button lock-button locked"`) ||
		!strings.Contains(html, `aria-label="Secret store locked"`) ||
		!strings.Contains(html, `$secretOpen = !$secretOpen`) {
		t.Fatalf("topbar missing lock-state icon control: %q", html)
	}
}

func TestApprovalConsoleTopbarHidesZeroPendingPill(t *testing.T) {
	html := componentHTML(ApprovalTopbar(approvalConsoleState{
		Summary: approvalConsoleSummary{ActiveClients: 1, PendingApprovals: 0},
	}))
	if strings.Contains(html, `class="brand-pending"`) {
		t.Fatalf("topbar rendered pending pill for zero pending approvals: %q", html)
	}
	if strings.Contains(html, `id="pending-count"`) {
		t.Fatalf("topbar rendered separate right-side pending count: %q", html)
	}
}

func TestApprovalClientHostClassifier(t *testing.T) {
	cases := map[string]string{
		"claude":                                "Claude Code",
		"claude --dangerously-skip-permissions": "Claude Code",
		"/opt/homebrew/bin/codex":               "Codex",
		"/Applications/Cursor.app/Cursor":       "Cursor",
		"node /tmp/something":                   "",
	}
	for command, want := range cases {
		if got := classifyApprovalClientHost(command); got != want {
			t.Fatalf("classifyApprovalClientHost(%q) = %q, want %q", command, got, want)
		}
	}
}

func TestApprovalConsoleStateIncludesClientPopoverData(t *testing.T) {
	now := time.Now().UTC()
	control := &stubDaemonHTTPControl{
		snapshots: []daemon.ClientSnapshot{{
			PID:            41,
			Mode:           "codemode",
			WorkingDir:     "/tmp/work",
			BoundTBSession: "abc123",
			PreparedTools:  []string{"gmail.send", "gmail.search"},
			PendingApprovals: []daemon.PendingApprovalSnapshot{{
				ToolCallID: "tc-1",
				TBSession:  "abc123",
				ToolName:   "gmail.send",
			}},
			ConnectedAt: now.Add(-time.Minute),
			LastSyncAt:  now,
		}},
	}
	state := approvalConsoleStateForHTTP(control)
	if len(state.Clients) != 1 {
		t.Fatalf("clients = %#v, want one client", state.Clients)
	}
	client := state.Clients[0]
	if client.PID != 41 ||
		client.Mode != "codemode" ||
		client.WorkingDir != "/tmp/work" ||
		client.BoundTBSession != "abc123" ||
		client.PendingApprovals != 1 ||
		client.PreparedTools != 2 ||
		client.ConnectedAt == "" ||
		client.LastSyncAt == "" ||
		!client.Active {
		t.Fatalf("client = %#v, want populated popover data", client)
	}
}

func TestApprovalConsoleUsesHyphenatedDatastarBindSignals(t *testing.T) {
	html := componentHTML(ApprovalConsolePage(daemonIndexPageData{StatusText: "locked", Available: true, Locked: true}, approvalConsoleState{
		SecretStore: approvalConsoleSecretStore{Status: "locked"},
	}))
	if !strings.Contains(html, `data-bind:unlock-key`) {
		t.Fatalf("console missing Datastar-safe unlock key binding: %q", html)
	}
	if !strings.Contains(html, `data-bind:reject-reason`) {
		t.Fatalf("console missing Datastar-safe reject reason binding: %q", html)
	}
	if strings.Contains(html, `data-bind:rejectReason`) || strings.Contains(html, `data-bind:unlockKey`) {
		t.Fatalf("console used camelCase data-bind attributes that browsers lowercase: %q", html)
	}
	if !strings.Contains(html, `method="post" action="/secret-store/unlock"`) {
		t.Fatalf("unlock form must post so passphrases cannot leak into the URL: %q", html)
	}
	if !strings.Contains(html, `id="secret-panel" class="secret-panel open" data-class="{&#39;open&#39;: true}"`) {
		t.Fatalf("locked secret store should show the unlock panel without toolbar toggle: %q", html)
	}
	if !strings.Contains(html, `Secret Store Locked`) {
		t.Fatalf("locked secret panel should use the capitalized onboarding title: %q", html)
	}
	if !strings.Contains(html, `data-on:submit__prevent`) {
		t.Fatalf("console missing Datastar v1 modifier syntax for submit prevention: %q", html)
	}
	if strings.Contains(html, `data-on:submit.prevent`) {
		t.Fatalf("console used dot modifier syntax that v1.0.1 treats as a literal event name: %q", html)
	}
	if !strings.Contains(html, `.topbar-menu[open] > summary::after`) ||
		!strings.Contains(html, `.intent-menu[open] > summary::after`) {
		t.Fatalf("console missing menu caret override for details-based popovers: %q", html)
	}

	unlockedHTML := componentHTML(SecretPanel(daemonIndexPageData{StatusText: "unlocked", Available: true, Locked: false}))
	if !strings.Contains(unlockedHTML, `data-class="{&#39;open&#39;: $secretOpen}"`) {
		t.Fatalf("unlocked secret panel should still use the toolbar toggle: %q", unlockedHTML)
	}
}

func TestApprovalConsoleInitialSetupRequiredSignal(t *testing.T) {
	state := approvalConsoleStateForHTTP(&stubDaemonHTTPControl{locked: true, setupRequired: true})
	if state.SecretStore.Status != "locked" || !state.SecretStore.SetupRequired {
		t.Fatalf("SecretStore = %#v, want locked setup-required state", state.SecretStore)
	}
	html := componentHTML(ApprovalConsolePage(approvalConsolePageDataFromState(state), state))
	if !strings.Contains(html, `&#34;setupRequired&#34;:true`) {
		t.Fatalf("initial console signals should keep create-store controls visible: %q", html)
	}
	if !strings.Contains(html, `Create your toolbox secret store`) ||
		!strings.Contains(html, `Create Secret Store`) ||
		!strings.Contains(html, `Choose a passphrase`) {
		t.Fatalf("setup-required panel should look like secret-store onboarding: %q", html)
	}
	if strings.Contains(html, `type="checkbox"`) || strings.Contains(html, `Set up new store`) {
		t.Fatalf("setup-required panel should not expose the setup checkbox: %q", html)
	}
	if strings.Contains(html, `Passphrase or backup code`) {
		t.Fatalf("setup-required panel should not mention backup codes in the passphrase placeholder: %q", html)
	}
	if !strings.Contains(html, `setup:true`) {
		t.Fatalf("setup-required form should still submit setup=true under the hood: %q", html)
	}
}

func TestApprovalConsolePatchesDoNotEmitQueuedBanner(t *testing.T) {
	state := approvalConsoleState{
		Summary: approvalConsoleSummary{ActiveClients: 1, PendingApprovals: 1, QueuedDecisions: 1},
		Sessions: []approvalConsoleSession{{
			TBSession: "abc123",
			Active:    true,
			Intent:    approvalConsoleIntent{Text: "Review pending mail"},
			PackageGroups: []approvalConsolePackageGroup{{
				PackageKey:   "gmail",
				PackageLabel: "Gmail",
				ToolCalls: []daemon.PendingApprovalSnapshot{{
					ToolCallID:     "tc-1",
					ToolName:       "gmail.messages.send",
					ToolLabel:      "messages.send",
					Description:    "Send Gmail message.",
					ParamsInspect:  `{to: "joe@example.com"}`,
					QueuedDecision: &daemon.QueuedApprovalDecision{Action: daemon.ApprovalActionReject},
				}},
			}},
		}},
	}
	var out bytes.Buffer
	writeApprovalConsolePatches(&out, nil, state)
	if strings.Contains(out.String(), "decision queued") {
		t.Fatalf("console patches emitted stale global queued banner: %q", out.String())
	}
	if !strings.Contains(out.String(), "Decision sent") {
		t.Fatalf("console patches missing row-level queued state: %q", out.String())
	}
}

func TestStartDaemonDebugServerServesIndex(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, &stubDaemonHTTPControl{locked: true})
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/): %v", err)
	}
	page := string(body)
	if !strings.Contains(page, `<strong id="status">locked</strong>`) {
		t.Fatalf("index missing locked status: %q", page)
	}
	if !strings.Contains(page, `id="unlock-form"`) {
		t.Fatalf("index missing unlock form: %q", page)
	}
	if !strings.Contains(page, `/secret-store/unlock`) {
		t.Fatalf("index missing unlock endpoint: %q", page)
	}
	if !strings.Contains(page, `/assets/datastar-v1.0.1.js`) {
		t.Fatalf("index missing vendored Datastar asset: %q", page)
	}
	if !strings.Contains(page, `data-init="@get('/approval-console/events', {payload:{}})"`) {
		t.Fatalf("index missing Datastar event stream init: %q", page)
	}
	if strings.Contains(page, `id="lock-form"`) {
		t.Fatalf("index unexpectedly rendered lock form while locked: %q", page)
	}
}

func TestStartDaemonDebugServerServesUnlockedIndex(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, &stubDaemonHTTPControl{locked: false})
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/): %v", err)
	}
	page := string(body)
	if !strings.Contains(page, `<strong id="status">unlocked</strong>`) {
		t.Fatalf("index missing unlocked status: %q", page)
	}
	if !strings.Contains(page, `id="lock-form"`) {
		t.Fatalf("index missing lock form: %q", page)
	}
	if !strings.Contains(page, `/secret-store/lock`) {
		t.Fatalf("index missing lock endpoint: %q", page)
	}
	if !strings.Contains(page, `id="recovery-codes-form"`) || !strings.Contains(page, `/secret-store/recovery-codes`) {
		t.Fatalf("index missing recovery-code generation controls while unlocked: %q", page)
	}
	if strings.Contains(page, `id="unlock-form"`) {
		t.Fatalf("index unexpectedly rendered unlock form while unlocked: %q", page)
	}
	if !strings.Contains(page, `id="recovery-unlock-key"`) || !strings.Contains(page, `Confirm passphrase`) {
		t.Fatalf("index missing recovery-code passphrase confirmation while unlocked: %q", page)
	}
}

func TestStartDaemonDebugServerServesRecoveryUnlockedIndex(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	control := &stubDaemonHTTPControl{locked: false, recoveryUnlocked: true}
	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, control)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/): %v", err)
	}
	page := string(body)
	if !strings.Contains(page, `id="recovery-codes-form"`) || !strings.Contains(page, `{payload:{}}`) {
		t.Fatalf("recovery-unlocked index missing unauthenticated recovery-code form: %q", page)
	}
	if strings.Contains(page, `id="recovery-unlock-key"`) {
		t.Fatalf("recovery-unlocked index should not prompt for passphrase: %q", page)
	}
}

func TestStartDaemonDebugServerServesClients(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	want := []daemon.ClientSnapshot{{
		PID:           123,
		Mode:          "codemode_repl",
		WorkingDir:    "/tmp/work",
		PreparedTools: []string{"example.com/pkg@v1.2.3/calc.add"},
	}}
	control := &stubDaemonHTTPControl{snapshots: want}

	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, control)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	resp, err := http.Get("http://" + addr + "/clients")
	if err != nil {
		t.Fatalf("GET /clients: %v", err)
	}
	defer resp.Body.Close()

	var got []daemon.ClientSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("Decode(/clients): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("/clients len = %d, want 1", len(got))
	}
	if got[0].PID != want[0].PID || got[0].Mode != want[0].Mode || got[0].WorkingDir != want[0].WorkingDir {
		t.Fatalf("/clients[0] = %#v, want %#v", got[0], want[0])
	}
	if len(got[0].PreparedTools) != 1 || got[0].PreparedTools[0] != want[0].PreparedTools[0] {
		t.Fatalf("/clients[0].PreparedTools = %#v, want %#v", got[0].PreparedTools, want[0].PreparedTools)
	}
}

func TestStartDaemonDebugServerSecretStoreEndpoints(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	control := &stubDaemonHTTPControl{locked: true}
	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, control)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	assertStatus := func(path string, wantLocked bool) {
		t.Helper()
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, resp.StatusCode)
		}
		var payload secretStoreStatusResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode(%s): %v", path, err)
		}
		if payload.Locked != wantLocked {
			t.Fatalf("%s locked = %t, want %t", path, payload.Locked, wantLocked)
		}
	}

	assertStatus("/secret-store/status", true)

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/secret-store/unlock", strings.NewReader(`{"unlock_key":"hunter2"}`))
	if err != nil {
		t.Fatalf("NewRequest(/secret-store/unlock): %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /secret-store/unlock: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /secret-store/unlock status = %d, want 200: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var unlockPayload secretStoreStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&unlockPayload); err != nil {
		t.Fatalf("Decode(/secret-store/unlock): %v", err)
	}
	if unlockPayload.Locked {
		t.Fatal("/secret-store/unlock reported locked=true, want false")
	}
	if control.unlockKey != "hunter2" {
		t.Fatalf("unlock key = %q, want hunter2", control.unlockKey)
	}

	assertStatus("/secret-store/status", false)

	lockReq, err := http.NewRequest(http.MethodPost, "http://"+addr+"/secret-store/lock", nil)
	if err != nil {
		t.Fatalf("NewRequest(/secret-store/lock): %v", err)
	}
	lockResp, err := http.DefaultClient.Do(lockReq)
	if err != nil {
		t.Fatalf("POST /secret-store/lock: %v", err)
	}
	defer lockResp.Body.Close()
	if lockResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /secret-store/lock status = %d, want 200", lockResp.StatusCode)
	}
	var lockPayload secretStoreStatusResponse
	if err := json.NewDecoder(lockResp.Body).Decode(&lockPayload); err != nil {
		t.Fatalf("Decode(/secret-store/lock): %v", err)
	}
	if !lockPayload.Locked {
		t.Fatal("/secret-store/lock reported locked=false, want true")
	}
}

func TestStartDaemonDebugServerSecretStoreUnlockAcceptsDatastarForm(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	control := &stubDaemonHTTPControl{locked: true}
	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, control)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/secret-store/unlock", strings.NewReader("unlock_key=hunter2"))
	if err != nil {
		t.Fatalf("NewRequest(/secret-store/unlock): %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Datastar-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /secret-store/unlock: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/secret-store/unlock): %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /secret-store/unlock status = %d, want 200: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if control.unlockKey != "hunter2" {
		t.Fatalf("unlock key = %q, want hunter2", control.unlockKey)
	}
	if !strings.Contains(string(body), "event: datastar-patch-elements") {
		t.Fatalf("Datastar unlock response missing element patches: %q", string(body))
	}
}

func TestStartDaemonDebugServerSecretStoreSetupKeepsRecoveryCodesVisible(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	control := &stubDaemonHTTPControl{
		locked:        true,
		setupRequired: true,
		unlockErr:     secrets.ErrNotInitialized,
	}
	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, control)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/secret-store/unlock", strings.NewReader(`{"unlock_key":"hunter2","setup":true}`))
	if err != nil {
		t.Fatalf("NewRequest(/secret-store/unlock): %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Datastar-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /secret-store/unlock: %v", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/secret-store/unlock): %v", err)
	}
	body := string(bodyBytes)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /secret-store/unlock status = %d, want 200: %s", resp.StatusCode, strings.TrimSpace(body))
	}
	if !strings.Contains(body, `"secretOpen":true`) {
		t.Fatalf("setup response should keep the secret panel open for recovery codes: %q", body)
	}
	if !strings.Contains(body, "AAAAA-BBBBB-CCCCC-DDDDD") {
		t.Fatalf("setup response missing recovery codes: %q", body)
	}
	if !strings.Contains(body, "Secret Store Unlocked") {
		t.Fatalf("setup response should patch in the unlocked secret panel: %q", body)
	}
}

func TestStartDaemonDebugServerSecretStoreRecoveryCodesEndpoint(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	control := &stubDaemonHTTPControl{locked: false}
	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, control)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/secret-store/recovery-codes", strings.NewReader(`{"unlock_key":"hunter2"}`))
	if err != nil {
		t.Fatalf("NewRequest(/secret-store/recovery-codes): %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Datastar-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /secret-store/recovery-codes: %v", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/secret-store/recovery-codes): %v", err)
	}
	body := string(bodyBytes)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /secret-store/recovery-codes status = %d, want 200: %s", resp.StatusCode, strings.TrimSpace(body))
	}
	if !strings.Contains(body, `"secretOpen":true`) {
		t.Fatalf("recovery-code response should keep the secret panel open: %q", body)
	}
	if !strings.Contains(body, "IIIII-JJJJJ-KKKKK-LLLLL") {
		t.Fatalf("recovery-code response missing generated recovery codes: %q", body)
	}
	if !strings.Contains(body, "Toolbox does not store them") {
		t.Fatalf("recovery-code response should explain backup-code handling: %q", body)
	}
	if control.recoveryUnlockKey != "hunter2" {
		t.Fatalf("recovery-code endpoint used unlock key %q, want hunter2", control.recoveryUnlockKey)
	}
}

func TestStartDaemonDebugServerSecretStoreRecoveryCodesEndpointRequiresAuth(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	control := &stubDaemonHTTPControl{locked: false}
	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, control)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/secret-store/recovery-codes", nil)
	if err != nil {
		t.Fatalf("NewRequest(/secret-store/recovery-codes): %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /secret-store/recovery-codes: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /secret-store/recovery-codes status = %d, want 400: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if control.recoveryGenerated != 0 {
		t.Fatalf("recovery-code endpoint generated %d code sets without auth", control.recoveryGenerated)
	}
}

func TestStartDaemonDebugServerSecretStoreRecoveryCodesEndpointAllowsRecoveryUnlock(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	control := &stubDaemonHTTPControl{locked: false, recoveryUnlocked: true}
	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, control)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/secret-store/recovery-codes", nil)
	if err != nil {
		t.Fatalf("NewRequest(/secret-store/recovery-codes): %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /secret-store/recovery-codes: %v", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/secret-store/recovery-codes): %v", err)
	}
	body := string(bodyBytes)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /secret-store/recovery-codes status = %d, want 200: %s", resp.StatusCode, strings.TrimSpace(body))
	}
	if !strings.Contains(body, "IIIII-JJJJJ-KKKKK-LLLLL") {
		t.Fatalf("recovery-code response missing generated recovery codes: %q", body)
	}
}

func TestStartDaemonDebugServerSecretStoreUnlockAcceptsDatastarCamelJSON(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	control := &stubDaemonHTTPControl{locked: true}
	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, control)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/secret-store/unlock", strings.NewReader(`{"unlockKey":"hunter2"}`))
	if err != nil {
		t.Fatalf("NewRequest(/secret-store/unlock): %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Datastar-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /secret-store/unlock: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/secret-store/unlock): %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /secret-store/unlock status = %d, want 200: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if control.unlockKey != "hunter2" {
		t.Fatalf("unlock key = %q, want hunter2", control.unlockKey)
	}
}

func TestStartDaemonDebugServerSecretStoreUnlockRejectsEmptyDatastarKey(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, &stubDaemonHTTPControl{locked: true})
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/secret-store/unlock", strings.NewReader(`{"unlockKey":""}`))
	if err != nil {
		t.Fatalf("NewRequest(/secret-store/unlock): %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Datastar-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /secret-store/unlock: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/secret-store/unlock): %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /secret-store/unlock status = %d, want Datastar 200: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if !strings.Contains(string(body), "unlock key is empty") {
		t.Fatalf("empty Datastar unlock response = %q, want explicit empty-key error", string(body))
	}
}

func TestStartDaemonDebugServerSecretStoreUnlockRequiresJSON(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, &stubDaemonHTTPControl{locked: true})
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/secret-store/unlock", strings.NewReader(`{"unlock_key":"hunter2"}`))
	if err != nil {
		t.Fatalf("NewRequest(/secret-store/unlock): %v", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /secret-store/unlock: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("POST /secret-store/unlock status = %d, want 415", resp.StatusCode)
	}
}

func TestStartDaemonDebugServerExitTriggersShutdown(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")
	t.Setenv(daemonAdminEndpointsEnv, "1")

	var shutdownCalls atomic.Int32
	shutdownCh := make(chan struct{}, 1)
	closeServer, addr, err := startDaemonDebugServer(io.Discard, func() {
		shutdownCalls.Add(1)
		select {
		case shutdownCh <- struct{}{}:
		default:
		}
	}, nil)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/exit", nil)
	if err != nil {
		t.Fatalf("NewRequest(/exit): %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /exit: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/exit): %v", err)
	}
	if string(body) != "shutting down" {
		t.Fatalf("/exit body = %q, want shutting down", string(body))
	}

	select {
	case <-shutdownCh:
	case <-time.After(2 * time.Second):
		t.Fatal("/exit did not trigger shutdown")
	}
	if shutdownCalls.Load() != 1 {
		t.Fatalf("shutdown calls = %d, want 1", shutdownCalls.Load())
	}
}

func TestStartDaemonDebugServerKillTriggersHardExit(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")
	t.Setenv(daemonAdminEndpointsEnv, "1")

	var killCalls atomic.Int32
	killCh := make(chan struct{}, 1)
	prev := daemonHardExit
	daemonHardExit = func() {
		killCalls.Add(1)
		select {
		case killCh <- struct{}{}:
		default:
		}
	}
	defer func() {
		daemonHardExit = prev
	}()

	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, nil)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/kill", nil)
	if err != nil {
		t.Fatalf("NewRequest(/kill): %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /kill: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/kill): %v", err)
	}
	if string(body) != "killing process" {
		t.Fatalf("/kill body = %q, want killing process", string(body))
	}

	select {
	case <-killCh:
	case <-time.After(2 * time.Second):
		t.Fatal("/kill did not trigger hard exit")
	}
	if killCalls.Load() != 1 {
		t.Fatalf("hard-exit calls = %d, want 1", killCalls.Load())
	}
}

func TestStartDaemonDebugServerAdminEndpointsDisabledByDefault(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")
	t.Setenv(daemonAdminEndpointsEnv, "")

	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, nil)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	for _, path := range []string{"/exit", "/kill"} {
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("%s status = %d body=%q, want 404", path, resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
}

func TestStartDaemonDebugServerExplicitBusyAddressFails(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(): %v", err)
	}
	defer listener.Close()

	t.Setenv(daemonBindAddressEnv, listener.Addr().String())
	if _, _, err := startDaemonDebugServer(io.Discard, nil, nil); err == nil {
		t.Fatal("startDaemonDebugServer() error = nil, want busy-address error")
	}
}

func TestRunDaemonServeDoesNotTouchSocketWhenHTTPBindFails(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tbx-daemon-")
	if err != nil {
		t.Fatalf("MkdirTemp(): %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	t.Setenv("TOOLBOX_DAEMON_DIR", dir)

	socketPath, err := daemon.SocketPath()
	if err != nil {
		t.Fatalf("SocketPath(): %v", err)
	}
	socketListener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen(%q): %v", socketPath, err)
	}
	defer socketListener.Close()
	if err := os.Chmod(socketPath, 0o600); err != nil {
		t.Fatalf("Chmod(%q): %v", socketPath, err)
	}

	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(http): %v", err)
	}
	defer httpListener.Close()
	t.Setenv(daemonBindAddressEnv, httpListener.Addr().String())

	err = runDaemonServe(io.Discard)
	if err == nil {
		t.Fatal("runDaemonServe() error = nil, want busy-address error")
	}
	if !strings.Contains(err.Error(), "listen on daemon debug address") {
		t.Fatalf("runDaemonServe() error = %v, want debug-listener bind failure", err)
	}

	info, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatalf("Lstat(%q): %v", socketPath, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("%s mode = %v, want socket", socketPath, info.Mode())
	}
}

func TestRunDaemonStopReportsNoRunningDaemons(t *testing.T) {
	prev := daemonStopAll
	daemonStopAll = func(func(string, ...any)) ([]int, error) {
		return nil, nil
	}
	defer func() {
		daemonStopAll = prev
	}()

	var stdout, stderr bytes.Buffer
	if err := runDaemonStop(&stdout, &stderr); err != nil {
		t.Fatalf("runDaemonStop() error: %v", err)
	}
	if got := stdout.String(); got != "no running toolbox daemons\n" {
		t.Fatalf("stdout = %q, want %q", got, "no running toolbox daemons\n")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunDaemonStopReportsStoppedPIDs(t *testing.T) {
	prev := daemonStopAll
	daemonStopAll = func(func(string, ...any)) ([]int, error) {
		return []int{12, 34}, nil
	}
	defer func() {
		daemonStopAll = prev
	}()

	var stdout, stderr bytes.Buffer
	if err := runDaemonStop(&stdout, &stderr); err != nil {
		t.Fatalf("runDaemonStop() error: %v", err)
	}
	if got := stdout.String(); got != "stopped toolbox daemons: 12 34\n" {
		t.Fatalf("stdout = %q, want %q", got, "stopped toolbox daemons: 12 34\n")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunDaemonStopWritesProgressToStderr(t *testing.T) {
	prev := daemonStopAll
	daemonStopAll = func(logf func(string, ...any)) ([]int, error) {
		logf("sent SIGTERM to toolbox daemons: %s; waiting up to %s before SIGKILL", "12 34", "15s")
		logf("toolbox daemons still running after %s: %s; sending SIGKILL", "15s", "34")
		return []int{12, 34}, nil
	}
	defer func() {
		daemonStopAll = prev
	}()

	var stdout, stderr bytes.Buffer
	if err := runDaemonStop(&stdout, &stderr); err != nil {
		t.Fatalf("runDaemonStop() error: %v", err)
	}
	if got := stdout.String(); got != "stopped toolbox daemons: 12 34\n" {
		t.Fatalf("stdout = %q, want %q", got, "stopped toolbox daemons: 12 34\n")
	}
	if got := stderr.String(); got != "sent SIGTERM to toolbox daemons: 12 34; waiting up to 15s before SIGKILL\ntoolbox daemons still running after 15s: 34; sending SIGKILL\n" {
		t.Fatalf("stderr = %q", got)
	}
}
