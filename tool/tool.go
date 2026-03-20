package tool

import "io/fs"

// Package is the smallest useful static package shape for the first package-loading seam.
type Package struct {
	Name                      string        `json:"name"`
	Runtime                   ToolRuntime   `json:"runtime"`
	AdditionalTypeScriptGlobs []string      `json:"additionalTypeScriptGlobs,omitempty"`
	Tools                     []PackageTool `json:"tools"`
}

type ToolRuntime string

const RuntimeTypeScriptSandbox ToolRuntime = "typescript-sandbox"

const RuntimeTypeScriptWasmerSandbox ToolRuntime = "typescript+wasmer-sandbox"

type AccessMode string

const (
	AccessModeReadOnly    AccessMode = "readOnly"
	AccessModeAppendOnly  AccessMode = "appendOnly"
	AccessModeCanDestruct AccessMode = "canDestruct"
)

type PackageTool struct {
	EntryTS      string         `json:"entry_ts"`
	Idempotent   *bool          `json:"idempotent,omitempty"`
	AccessMode   AccessMode     `json:"accessMode,omitempty"`
	Description  string         `json:"description,omitempty"`
	ParamsSchema map[string]any `json:"paramsSchema,omitempty"`
}

// ResolvedTool is the smallest useful selected tool shape for the current
// outside-in seams. It combines static package identity with the concrete
// executable artifact for one visible tool.
type ResolvedTool struct {
	Name         string
	Description  string
	ParamsSchema map[string]any
	Package      *Package
	TS           *TSToolDef
	TSWasm       *TSWasmToolDef
}

// TSToolDef is the smallest useful TS tool definition for the current invoke
// seam. It points at one tool entry file inside a package-shaped filesystem.
type TSToolDef struct {
	Entry       string
	Files       fs.FS
	PackageRoot string
}

type TSWasmToolDef struct {
	TSToolDef
	Executables map[string]string
}
