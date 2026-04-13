package toolsetctl

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/solidarity-ai/toolbox/toolpkgdiscovery"
	"github.com/solidarity-ai/toolbox/toolset"
)

// ErrManagementToolsUnsupported is returned when a backend exposes prepared
// toolsets but does not support agent-facing runtime toolset management.
var ErrManagementToolsUnsupported = errors.New("toolset management tools are not supported by this backend")

// PreparedBackend stores a host-controlled prepared toolset snapshot and
// declines agent-facing runtime management operations. Callers can replace the
// base prepared snapshot over time through SetPrepared(); Prepared() merges in
// any enabled builtin package-discovery tools before returning the effective
// agent-visible toolset.
type PreparedBackend struct {
	mu                      sync.RWMutex
	prepared                toolset.PreparedToolset
	enablePackageDiscovery  bool
	enableToolsetManagement bool
	builtinBackend          ToolsetBackend
	consumer                PreparedToolConsumer
}

func NewPreparedBackend(prepared toolset.PreparedToolset, consumer PreparedToolConsumer) *PreparedBackend {
	backend := &PreparedBackend{
		prepared: prepared,
		consumer: consumer,
	}
	backend.notifyConsumer()
	return backend
}

func (b *PreparedBackend) Prepared(context.Context) (toolset.PreparedToolset, error) {
	if b == nil {
		return toolset.PreparedToolset{}, nil
	}
	b.mu.RLock()
	prepared := b.prepared
	enablePackageDiscovery := b.enablePackageDiscovery
	enableToolsetManagement := b.enableToolsetManagement
	builtinBackend := b.builtinBackend
	b.mu.RUnlock()

	if builtinBackend == nil {
		builtinBackend = b
	}

	parts := []toolset.PreparedToolset{prepared}
	if enablePackageDiscovery {
		discovery, err := toolpkgdiscovery.BuiltinPreparedToolset(builtinBackend)
		if err != nil {
			return toolset.PreparedToolset{}, err
		}
		parts = append(parts, discovery)
	}
	if enableToolsetManagement {
		management, err := toolsetManagementPreparedToolset(builtinBackend)
		if err != nil {
			return toolset.PreparedToolset{}, err
		}
		parts = append(parts, management)
	}
	if len(parts) == 1 {
		return prepared, nil
	}
	return toolset.JoinPreparedToolsets(parts...)
}

func (b *PreparedBackend) EnableToolsForPackageDiscovery() bool {
	if b == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.enablePackageDiscovery
}

// SetPrepared replaces the stored prepared toolset snapshot.
func (b *PreparedBackend) SetPrepared(prepared toolset.PreparedToolset) {
	if b == nil {
		return
	}
	b.SetSnapshot(prepared, b.EnableToolsForPackageDiscovery(), b.EnableToolsForToolsetManagement())
}

// SetEnableToolsForPackageDiscovery replaces the stored package-discovery flag.
func (b *PreparedBackend) SetEnableToolsForPackageDiscovery(enable bool) {
	if b == nil {
		return
	}
	b.SetSnapshot(b.basePrepared(), enable, b.EnableToolsForToolsetManagement())
}

func (b *PreparedBackend) SetEnableToolsForToolsetManagement(enable bool) {
	if b == nil {
		return
	}
	b.SetSnapshot(b.basePrepared(), b.EnableToolsForPackageDiscovery(), enable)
}

// SetSnapshot replaces the stored base prepared toolset and both builtin flags
// in one notification-producing update.
func (b *PreparedBackend) SetSnapshot(prepared toolset.PreparedToolset, enablePackageDiscovery, enableToolsetManagement bool) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.prepared = prepared
	b.enablePackageDiscovery = enablePackageDiscovery
	b.enableToolsetManagement = enableToolsetManagement
	b.mu.Unlock()
	b.notifyConsumer()
}

// SetBuiltinBackend replaces the backend used by any builtin management or
// discovery tools added to this prepared snapshot.
func (b *PreparedBackend) SetBuiltinBackend(backend ToolsetBackend) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.builtinBackend = backend
	b.mu.Unlock()
}

func (b *PreparedBackend) EnableToolsForToolsetManagement() bool {
	if b == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.enableToolsetManagement
}

func (*PreparedBackend) Search(context.Context, toolpkgdiscovery.SearchRequest) (toolpkgdiscovery.SearchResult, error) {
	return toolpkgdiscovery.SearchResult{}, unsupportedManagementOp("search")
}

func (*PreparedBackend) Inspect(context.Context, toolpkgdiscovery.InspectRequest) (toolpkgdiscovery.InspectResult, error) {
	return toolpkgdiscovery.InspectResult{}, unsupportedManagementOp("inspect")
}

func (*PreparedBackend) Install(context.Context, InstallRequest) (toolset.PreparedToolset, error) {
	return toolset.PreparedToolset{}, unsupportedManagementOp("install")
}

func (*PreparedBackend) Uninstall(context.Context, UninstallRequest) (toolset.PreparedToolset, error) {
	return toolset.PreparedToolset{}, unsupportedManagementOp("uninstall")
}

func (*PreparedBackend) Auth(context.Context, AuthRequest) (toolset.PreparedToolset, error) {
	return toolset.PreparedToolset{}, unsupportedManagementOp("auth")
}

func unsupportedManagementOp(name string) error {
	return fmt.Errorf("%s: %w", name, ErrManagementToolsUnsupported)
}

func (b *PreparedBackend) basePrepared() toolset.PreparedToolset {
	if b == nil {
		return toolset.PreparedToolset{}
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.prepared
}

func (b *PreparedBackend) notifyConsumer() {
	if b == nil || b.consumer == nil {
		return
	}
	prepared, err := b.Prepared(context.Background())
	if err != nil {
		return
	}
	b.consumer.SetPreparedTools(prepared)
}
