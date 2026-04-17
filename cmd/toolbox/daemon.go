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

type daemonHTTPApprovalGroup struct {
	ID        string                           `json:"id"`
	Label     string                           `json:"label,omitempty"`
	ToolCalls []daemon.PendingApprovalSnapshot `json:"tool_calls,omitempty"`
}

func pendingApprovalGroupsForHTTP(control daemonHTTPControl) []daemonHTTPApprovalGroup {
	if control == nil {
		return nil
	}
	clients := control.Clients()
	if len(clients) == 0 {
		return nil
	}

	out := make([]daemonHTTPApprovalGroup, 0, len(clients))
	for _, client := range clients {
		if len(client.PendingApprovals) == 0 {
			continue
		}
		connectedAt := int64(0)
		if !client.ConnectedAt.IsZero() {
			connectedAt = client.ConnectedAt.UnixNano()
		}
		group := daemonHTTPApprovalGroup{
			ID:        fmt.Sprintf("client:%d:%d", client.PID, connectedAt),
			ToolCalls: append([]daemon.PendingApprovalSnapshot(nil), client.PendingApprovals...),
		}
		switch {
		case strings.TrimSpace(client.Mode) != "" && strings.TrimSpace(client.WorkingDir) != "":
			group.Label = client.Mode + " " + client.WorkingDir
		case strings.TrimSpace(client.WorkingDir) != "":
			group.Label = client.WorkingDir
		case strings.TrimSpace(client.Mode) != "":
			group.Label = client.Mode
		}
		out = append(out, group)
	}
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

var daemonIndexTemplate = template.Must(template.New("daemon-index").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Toolbox</title>
  <style>
    body {
      font-family: sans-serif;
      margin: 2rem;
      max-width: 32rem;
      line-height: 1.4;
    }
    form {
      display: grid;
      gap: 0.75rem;
    }
    input, button {
      font: inherit;
      padding: 0.5rem 0.75rem;
    }
    button {
      width: fit-content;
      margin-right: 0.5rem;
      margin-top: 0.5rem;
    }
    code {
      background: #f4f4f4;
      padding: 0.1rem 0.3rem;
    }
    pre {
      background: #f4f4f4;
      padding: 0.75rem;
      overflow-x: auto;
      white-space: pre-wrap;
    }
    #message {
      min-height: 1.5rem;
      white-space: pre-wrap;
    }
  </style>
</head>
<body>
  <h1>Toolbox</h1>
  <p>Status: <strong id="status">{{.StatusText}}</strong></p>
  {{if .Available}}
    {{if .Locked}}
  <form id="unlock-form">
    <label for="unlock-key">Passcode</label>
    <input id="unlock-key" name="unlock_key" type="password" autocomplete="current-password">
    <button type="submit">Unlock</button>
  </form>
    {{else}}
  <form id="lock-form">
    <button type="submit">Lock</button>
  </form>
    {{end}}
  {{else}}
  <p>Secret store unavailable.</p>
  {{end}}
  <p id="message"></p>
  <section>
    <h2>Approvals</h2>
    <ul id="approvals"></ul>
  </section>
  <script>
    const messageEl = document.getElementById('message');
    const unlockForm = document.getElementById('unlock-form');
    const lockForm = document.getElementById('lock-form');
    const unlockInput = document.getElementById('unlock-key');
    const approvalsEl = document.getElementById('approvals');

    async function submitJSON(url, body) {
      const resp = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
      const text = await resp.text();
      if (!resp.ok) {
        throw new Error(text || 'request failed');
      }
    }

    async function submitNoBody(url) {
      const resp = await fetch(url, { method: 'POST' });
      const text = await resp.text();
      if (!resp.ok) {
        throw new Error(text || 'request failed');
      }
    }

    async function applyApprovals(approvals) {
      await submitJSON('/approvals/apply', { approvals });
    }

    function renderApprovals(groups) {
      approvalsEl.innerHTML = '';
      if (!groups || groups.length === 0) {
        const item = document.createElement('li');
        item.textContent = 'No pending approvals.';
        approvalsEl.appendChild(item);
      return;
      }
      for (const group of groups) {
        const item = document.createElement('li');
        const heading = document.createElement('div');
        const headingLabel = group.label || group.id;
        heading.textContent = headingLabel + ' (' + group.tool_calls.length + ' call' + (group.tool_calls.length === 1 ? '' : 's') + ')';
        item.appendChild(heading);

        const approveGroupButton = document.createElement('button');
        approveGroupButton.type = 'button';
        approveGroupButton.textContent = 'Approve All';
        approveGroupButton.addEventListener('click', async () => {
          messageEl.textContent = '';
          try {
            await applyApprovals((group.tool_calls || []).map((call) => ({
              id: call.tool_call_id,
              approved: true
            })));
          } catch (err) {
            messageEl.textContent = err.message || 'approve failed';
          }
        });
        item.appendChild(approveGroupButton);

        const rejectGroupButton = document.createElement('button');
        rejectGroupButton.type = 'button';
        rejectGroupButton.textContent = 'Reject All';
        rejectGroupButton.addEventListener('click', async () => {
          const message = window.prompt('Reject message', '');
          if (message === null) {
            return;
          }
          messageEl.textContent = '';
          try {
            await applyApprovals((group.tool_calls || []).map((call) => ({
              id: call.tool_call_id,
              approved: false,
              reason: message
            })));
          } catch (err) {
            messageEl.textContent = err.message || 'reject failed';
          }
        });
        item.appendChild(rejectGroupButton);

        const calls = document.createElement('ul');
        for (const call of group.tool_calls || []) {
          const callItem = document.createElement('li');
          const name = document.createElement('div');
          name.textContent = call.tool_name + ' [' + call.tool_call_id + ']';
          callItem.appendChild(name);
          if (call.params_inspect) {
            const params = document.createElement('pre');
            params.textContent = call.params_inspect;
            callItem.appendChild(params);
          }

          const approveCallButton = document.createElement('button');
          approveCallButton.type = 'button';
          approveCallButton.textContent = 'Approve';
          approveCallButton.addEventListener('click', async () => {
            messageEl.textContent = '';
            try {
              await applyApprovals([{ id: call.tool_call_id, approved: true }]);
            } catch (err) {
              messageEl.textContent = err.message || 'approve failed';
            }
          });
          callItem.appendChild(approveCallButton);

          const rejectCallButton = document.createElement('button');
          rejectCallButton.type = 'button';
          rejectCallButton.textContent = 'Reject';
          rejectCallButton.addEventListener('click', async () => {
            const message = window.prompt('Reject message', '');
            if (message === null) {
              return;
            }
            messageEl.textContent = '';
            try {
              await applyApprovals([{ id: call.tool_call_id, approved: false, reason: message }]);
            } catch (err) {
              messageEl.textContent = err.message || 'reject failed';
            }
          });
          callItem.appendChild(rejectCallButton);
          calls.appendChild(callItem);
        }
        item.appendChild(calls);
        approvalsEl.appendChild(item);
      }
    }

    async function loadApprovals() {
      const resp = await fetch('/approvals');
      const text = await resp.text();
      if (!resp.ok) {
        throw new Error(text || 'request failed');
      }
      renderApprovals(JSON.parse(text || '[]'));
    }

    if (unlockForm) {
      unlockForm.addEventListener('submit', async (event) => {
        event.preventDefault();
        messageEl.textContent = '';
        try {
          await submitJSON('/secret-store/unlock', { unlock_key: unlockInput.value });
          window.location.reload();
        } catch (err) {
          messageEl.textContent = err.message || 'unlock failed';
        }
      });
    }

    if (lockForm) {
      lockForm.addEventListener('submit', async (event) => {
        event.preventDefault();
        messageEl.textContent = '';
        try {
          await submitNoBody('/secret-store/lock');
          window.location.reload();
        } catch (err) {
          messageEl.textContent = err.message || 'lock failed';
        }
      });
    }

    loadApprovals().catch((err) => {
      messageEl.textContent = err.message || 'failed to load approvals';
    });

    const approvalEvents = new EventSource('/approvals/events');
    approvalEvents.addEventListener('approvals', (event) => {
      try {
        renderApprovals(JSON.parse(event.data || '[]'));
      } catch (err) {
        messageEl.textContent = 'failed to update approvals';
      }
    });
  </script>
</body>
</html>
`))

func renderDaemonIndex(w io.Writer, page daemonIndexPageData) error {
	return daemonIndexTemplate.Execute(w, page)
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
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
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
