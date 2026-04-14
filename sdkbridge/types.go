package sdkbridge

import (
	"encoding/json"

	"github.com/solidarity-ai/toolbox/registry"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolsetfile"
)

// ComposeMode selects which tool surface the bridge should expose for one
// composed toolset handle.
type ComposeMode string

const (
	ComposeModeDirect   ComposeMode = "direct"
	ComposeModeCodemode ComposeMode = "codemode"
)

const CodeModeToolName = "super_tool"

// ToolDescriptor is the host-facing tool shape returned by toolset.compose.
type ToolDescriptor struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	ParamsSchema map[string]any `json:"params_schema,omitempty"`
}

type SystemVersionResult struct {
	Version string `json:"version"`
}

type ToolInvokeResult struct {
	Content string `json:"content"`
}

type ToolsetUpdateResult struct {
	Tools []ToolDescriptor `json:"tools"`
}

type ToolsetCloseParams struct {
	ToolsetID string `json:"toolset_id"`
}

type ToolsetSearchParams struct {
	ToolsetID string `json:"toolset_id"`
	Query     string `json:"query"`
	Tools     bool   `json:"tools,omitempty"`
	Packages  bool   `json:"packages,omitempty"`
	Runtime   string `json:"runtime,omitempty"`
	Effect    string `json:"effect,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

type ToolsetSearchResult struct {
	Packages []registry.PackageSearchHit `json:"packages,omitempty"`
	Tools    []registry.ToolSearchHit    `json:"tools,omitempty"`
}

type ToolsetInspectParams struct {
	ToolsetID string `json:"toolset_id"`
	Target    string `json:"target"`
}

type ToolsetInspectResult struct {
	Target  string          `json:"target,omitempty"`
	Version string          `json:"version,omitempty"`
	Source  string          `json:"source"`
	Package tooldef.Package `json:"package"`
}

type ToolsetInstallParams struct {
	ToolsetID string `json:"toolset_id"`
	Package   string `json:"package"`
}

type ToolsetUninstallParams struct {
	ToolsetID string `json:"toolset_id"`
	Target    string `json:"target"`
}

type ToolsetAuthParams struct {
	ToolsetID         string `json:"toolset_id"`
	Target            string `json:"target"`
	Account           string `json:"account,omitempty"`
	Credential        string `json:"credential,omitempty"`
	Check             bool   `json:"check,omitempty"`
	DeleteCredential  bool   `json:"deleteCredential,omitempty"`
	RenameAccountFrom string `json:"renameAccountFrom,omitempty"`
	RenameAccountTo   string `json:"renameAccountTo,omitempty"`
	DeleteAccount     string `json:"deleteAccount,omitempty"`
}

type ToolInvokeParams struct {
	ToolsetID string         `json:"toolset_id"`
	ToolName  string         `json:"tool_name"`
	Params    map[string]any `json:"params"`
}

type ToolsetFileLoadParams struct {
	Path string `json:"path"`
}

type ToolsetFileWriteParams struct {
	Path    string          `json:"path"`
	Toolset json.RawMessage `json:"toolset"`
}

type ComposeParams struct {
	Mode        ComposeMode     `json:"mode"`
	ToolsetFile string          `json:"toolset_file,omitempty"`
	Toolset     json.RawMessage `json:"toolset,omitempty"`
	Config      *ComposeConfig  `json:"config,omitempty"`
}

type ComposeConfig struct {
	Tools            []BoundToolDTO        `json:"tools,omitempty"`
	ResourceBindings map[string]BindingDTO `json:"resource_bindings,omitempty"`
	EnvContext       map[string]any        `json:"env_context,omitempty"`
}

type BoundToolDTO struct {
	ToolRef  string                `json:"tool_ref"`
	Bindings map[string]BindingDTO `json:"bindings,omitempty"`
}

type BindingDTO struct {
	Value  string `json:"value,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
	Check  string `json:"check,omitempty"`
}

type ComposeResult struct {
	ToolsetID string           `json:"toolset_id"`
	Tools     []ToolDescriptor `json:"tools"`
}

type ToolsetDocument = toolsetfile.ToolsetFile
