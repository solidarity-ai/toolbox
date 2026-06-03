package oauth2flow

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// callbackResult carries the code and state from an OAuth callback.
type callbackResult struct {
	code  string
	state string
	err   error
}

// CallbackReceiver starts an ephemeral HTTP server on 127.0.0.1 and
// waits for the OAuth provider to redirect with ?code=...&state=...
type CallbackReceiver struct {
	port     int
	resultCh chan callbackResult
	server   *http.Server
}

// NewCallbackReceiver creates and starts a CallbackReceiver on a random port.
func NewCallbackReceiver() (*CallbackReceiver, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("oauth2flow: listen: %w", err)
	}

	ch := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code, matched, err := parseAuthorizationResponseValues(r.URL.Query(), "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			select {
			case ch <- callbackResult{state: r.URL.Query().Get("state"), err: err}:
			default:
			}
			return
		}
		if !matched || code == "" {
			http.Error(w, "missing code parameter", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><h1>Authorization successful!</h1><p>You can close this tab.</p></body></html>")
		select {
		case ch <- callbackResult{code: code, state: r.URL.Query().Get("state")}:
		default:
		}
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(listener)

	port := listener.Addr().(*net.TCPAddr).Port
	return &CallbackReceiver{port: port, resultCh: ch, server: srv}, nil
}

// RedirectURI returns the local callback URL.
func (r *CallbackReceiver) RedirectURI() string {
	return fmt.Sprintf("http://127.0.0.1:%d/callback", r.port)
}

// ReceiveCode blocks until a code arrives on the callback or ctx is cancelled.
// It validates that the callback state matches expectedState.
func (r *CallbackReceiver) ReceiveCode(ctx context.Context, expectedState string) (string, error) {
	select {
	case result := <-r.resultCh:
		if result.state != expectedState {
			return "", fmt.Errorf("oauth2flow: state mismatch (possible CSRF)")
		}
		if result.err != nil {
			return "", result.err
		}
		return result.code, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Close shuts down the HTTP server.
func (r *CallbackReceiver) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return r.server.Shutdown(ctx)
}
