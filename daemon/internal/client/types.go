package client

import (
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/solidarity-ai/toolbox/daemon/apiv1/daemonv1connect"
	daemonplatform "github.com/solidarity-ai/toolbox/daemon/internal/processctl/platform"
	daemonserver "github.com/solidarity-ai/toolbox/daemon/internal/server"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetctl"
)

const sessionServiceBaseURL = "http://toolbox-daemon"

var ErrUnsupportedPlatform = daemonplatform.ErrUnsupportedPlatform

type Client struct {
	mu         sync.Mutex
	socketPath string
	httpClient *http.Client
	service    daemonv1connect.SessionServiceClient
}

type PingResult struct {
	Payload string
	PID     int
}

type SessionDelegate interface {
	toolsetctl.PreparedToolConsumer
	Close() error
}

type sessionDelegate struct {
	reg    *SessionRegistration
	mode   string
	cwd    string
	stderr io.Writer
}

type noopSessionDelegate struct{}

func OpenSessionDelegate(mode, cwd string, stderr io.Writer) (SessionDelegate, error) {
	reg, err := OpenSessionRegistration(daemonserver.SessionState{
		Mode:       mode,
		WorkingDir: cwd,
	})
	if err != nil {
		return nil, err
	}
	return &sessionDelegate{
		reg:    reg,
		mode:   mode,
		cwd:    cwd,
		stderr: stderr,
	}, nil
}

func NewNoopSessionDelegate() SessionDelegate {
	return noopSessionDelegate{}
}

func (d *sessionDelegate) SetPreparedTools(prepared toolset.PreparedToolset) {
	if d == nil || d.reg == nil {
		return
	}
	if err := d.reg.Update(daemonserver.SessionState{
		Mode:          d.mode,
		WorkingDir:    d.cwd,
		PreparedTools: toolset.PreparedToolRefs(prepared),
	}); err != nil && d.stderr != nil {
		_, _ = fmt.Fprintf(d.stderr, "toolbox daemon sync error: %v\n", err)
	}
}

func (d *sessionDelegate) Close() error {
	if d == nil || d.reg == nil {
		return nil
	}
	return d.reg.Close()
}

func (noopSessionDelegate) SetPreparedTools(toolset.PreparedToolset) {}

func (noopSessionDelegate) Close() error { return nil }
