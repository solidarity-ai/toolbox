package main

import (
	"errors"
	"io"

	"github.com/solidarity-ai/toolbox/daemon"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetctl"
)

type combinedPreparedToolConsumer []toolsetctl.PreparedToolConsumer

var ensureSessionDaemon = func(mode, cwd string, stderr io.Writer) (daemon.SessionDelegate, error) {
	delegate, err := daemon.OpenSessionDelegate(mode, cwd, stderr)
	if err == nil {
		return delegate, nil
	}
	if errors.Is(err, daemon.ErrUnsupportedPlatform) {
		return daemon.NewNoopSessionDelegate(), nil
	}
	return nil, err
}

func (c combinedPreparedToolConsumer) SetPreparedTools(prepared toolset.PreparedToolset) {
	for _, consumer := range c {
		if consumer == nil {
			continue
		}
		consumer.SetPreparedTools(prepared)
	}
}
