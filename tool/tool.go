package tool

import (
	"io/fs"

	"github.com/microsoft/typescript-go/toolbox"
)

// Package is the smallest useful static package shape for the first package-loading seam.
type Package struct {
	Name                      string            `json:"name"`
	Runtime                   ToolRuntime       `json:"runtime"`
	SHA256                    string            `json:"sha256,omitempty"`
	AdditionalTypeScriptGlobs []string          `json:"additionalTypeScriptGlobs,omitempty"`
	Executables               map[string]string `json:"executables,omitempty"`
	Tools                     []PackageTool     `json:"tools"`
}

type ToolRuntime string

const RuntimeTypeScriptSandbox ToolRuntime = "typescript-sandbox"

const RuntimeTypeScriptWasixSandbox ToolRuntime = "typescript+wasix-sandbox"

const RuntimeTypeScriptWasip2Sandbox ToolRuntime = "typescript+wasip2-sandbox"

type AccessMode string

const (
	AccessModeReadOnly    AccessMode = "readOnly"
	AccessModeAppendOnly  AccessMode = "appendOnly"
	AccessModeCanDestruct AccessMode = "canDestruct"
)

// ResourceParam describes one inferred resource parameter and its canonical binding name.
type ResourceParam struct {
	Name        string `json:"name"`         // e.g. "account_id"
	BindingName string `json:"binding_name"` // e.g. "zendesk_account" (defaults to Name)
}

type PackageTool struct {
	EntryTS        string              `json:"entry_ts"`
	Idempotent     *bool               `json:"idempotent,omitempty"`
	AccessMode     AccessMode          `json:"accessMode,omitempty"`
	Description    string              `json:"description,omitempty"`
	ParamsSchema   map[string]any      `json:"paramsSchema,omitempty"`
	ParamsType   *toolbox.ParamsType     `json:"-"`
	Sig        *toolbox.FuncSig  `json:"-"`
	ResourceParams []ResourceParam     `json:"resourceParams,omitempty"`
}

// ResolvedTool is the smallest useful selected tool shape for the current
// outside-in seams. It combines static package identity with the concrete
// executable artifact for one visible tool.
type ResolvedTool struct {
	Name           string
	Description    string
	ParamsSchema   map[string]any
	ParamsType   *toolbox.ParamsType
	Sig        *toolbox.FuncSig
	AccessMode     AccessMode
	Idempotent     *bool
	ResourceParams []ResourceParam
	Package        *Package
	TS             *TSToolDef
	TSWasm         *TSWasmToolDef
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
