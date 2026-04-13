package toolsetctl

import (
	"context"

	"github.com/solidarity-ai/toolbox/toolpkgdiscovery"
	"github.com/solidarity-ai/toolbox/toolset"
)

// PreparedToolConsumer accepts effective prepared-tool snapshots pushed by a
// backend as its active tool surface changes over time, for example to update
// a running MCP server or codemode session.
type PreparedToolConsumer interface {
	SetPreparedTools(toolset.PreparedToolset)
}

// ToolsetBackend exists so that callers can support dynamic installation and
// removal of tools on a running MCP server or codemode session without baking
// those lifecycle operations into one persistence model such as toolset files.
type ToolsetBackend interface {
	Prepared(ctx context.Context) (toolset.PreparedToolset, error)
	toolpkgdiscovery.Backend
	// EnableToolsForToolsetManagement reports whether unsafe runtime toolset
	// management tools such as install, uninstall, and auth are enabled for the
	// current backend snapshot. Callers use this for instruction shaping and
	// similar guidance.
	EnableToolsForToolsetManagement() bool
	Install(ctx context.Context, req InstallRequest) (toolset.PreparedToolset, error)
	Uninstall(ctx context.Context, req UninstallRequest) (toolset.PreparedToolset, error)
	Auth(ctx context.Context, req AuthRequest) (toolset.PreparedToolset, error)
}

// InstallRequest asks the backend to add or update one package in the active
// toolset.
type InstallRequest struct {
	Package string
}

// UninstallRequest asks the backend to remove one package from the active
// toolset.
type UninstallRequest struct {
	Target string
}

// AuthRequest mirrors the current credential-management operations exposed by
// the CLI auth surface.
type AuthRequest struct {
	Target            string
	Account           string
	Credential        string
	Check             bool
	DeleteCredential  bool
	RenameAccountFrom string
	RenameAccountTo   string
	DeleteAccount     string
}
