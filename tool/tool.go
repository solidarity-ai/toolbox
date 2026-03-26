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
	EntryTS        string           `json:"entry_ts"`
	Idempotent     *bool            `json:"idempotent,omitempty"`
	AccessMode     AccessMode       `json:"accessMode,omitempty"`
	Description    string           `json:"description,omitempty"`
	ParamsSchema   map[string]any   `json:"paramsSchema,omitempty"`
	Sig            *toolbox.FuncSig `json:"-"`
	ResourceParams []ResourceParam  `json:"resourceParams,omitempty"`
}

// ResolvedTool is the smallest useful selected tool shape for the current
// outside-in seams. It combines static package identity with the concrete
// executable artifact for one visible tool.
type ResolvedTool struct {
	Name           string
	Description    string
	Sig            *toolbox.FuncSig
	AccessMode     AccessMode
	Idempotent     *bool
	ResourceParams []ResourceParam
	Package        *Package
	TS             *TSToolDef
	TSWasm         *TSWasmToolDef

	// paramsSchema is the fallback JSON Schema for when Sig is nil (e.g. dist packages).
	// Use ParamsSchema() to access — it derives from Sig when available.
	paramsSchema map[string]any
}

// SetParamsSchema sets the fallback JSON Schema (used when Sig is nil).
func (rt *ResolvedTool) SetParamsSchema(schema map[string]any) {
	rt.paramsSchema = schema
}

// ParamsSchema returns the JSON Schema for this tool's parameters.
// When Sig is available, it derives the schema from the type signature
// using CombinedParamsType (which handles both single-param-object and
// multi-param functions); otherwise it falls back to the stored schema
// (e.g. from dist manifests).
func (rt ResolvedTool) ParamsSchema() map[string]any {
	if rt.Sig != nil {
		if pt := rt.Sig.CombinedParamsType(); pt != nil {
			return pt.ToJSONSchema()
		}
	}
	return rt.paramsSchema
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
