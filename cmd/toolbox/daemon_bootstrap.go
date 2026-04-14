package main

import (
	"errors"
	"fmt"
	"io"
	"sync"

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

func bindSecretEpochReload(delegate daemon.SessionDelegate, stderr io.Writer, reload func() error) {
	if delegate == nil || reload == nil {
		return
	}

	var (
		mu      sync.Mutex
		running bool
		pending bool
	)

	delegate.SetSecretEpochHandler(func() {
		mu.Lock()
		if running {
			pending = true
			mu.Unlock()
			return
		}
		running = true
		mu.Unlock()

		go func() {
			for {
				if err := reload(); err != nil && stderr != nil {
					_, _ = fmt.Fprintf(stderr, "toolbox daemon reload error: %v\n", err)
				}

				mu.Lock()
				if !pending {
					running = false
					mu.Unlock()
					return
				}
				pending = false
				mu.Unlock()
			}
		}()
	})
}
