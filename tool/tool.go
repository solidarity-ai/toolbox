package tool

import (
	"io/fs"
	"testing/fstest"
)

const calcToolsPackageDir = "github.com/solidarity-ai/calc-tools"

// Package is the smallest useful static package shape for the first package-loading seam.
type Package struct {
	Name    string        `json:"name"`
	Runtime ToolRuntime   `json:"runtime"`
	Tools   []PackageTool `json:"tools"`
}

type ToolRuntime string

const RuntimeTypeScriptSandbox ToolRuntime = "typescript-sandbox"

type AccessMode string

const (
	AccessModeReadOnly    AccessMode = "readOnly"
	AccessModeAppendOnly  AccessMode = "appendOnly"
	AccessModeCanDestruct AccessMode = "canDestruct"
)

type PackageTool struct {
	EntryTS    string     `json:"entry_ts"`
	Idempotent *bool      `json:"idempotent,omitempty"`
	AccessMode AccessMode `json:"accessMode,omitempty"`
}

// TSToolDef is the smallest useful TS tool definition for the current invoke
// seam. It points at one tool entry file inside a package-shaped filesystem.
type TSToolDef struct {
	Entry string
	Files fs.FS
}

// StubTSToolDef returns a package-shaped TS tool definition for known stub
// tools used in the current outside-in tests.
func StubTSToolDef(name string) (TSToolDef, bool) {
	switch name {
	case "calc.add":
		return TSToolDef{
			Entry: calcToolsPackageDir + "/tools/calc.add.ts",
			Files: fstest.MapFS{
				calcToolsPackageDir + "/manifest.toml": {
					Data: []byte(`[package]
path = "github.com/solidarity-ai/calc-tools"
version = "v1.0.0"
description = "Stub calc tools"
`),
				},
				calcToolsPackageDir + "/tools/calc.add.ts": {
					Data: []byte(`const tool = JSON.parse('{"params":{"a":{"type":"number","required":true,"description":"First number"},"b":{"type":"number","required":true,"description":"Second number"}},"metadata":{"idempotent":true,"description":"Add two numbers"}}');

export const params = tool.params;
export const metadata = tool.metadata;

export function execute(params: { a: number; b: number }, ctx: unknown) {
  return String(params.a + params.b)
}
`),
				},
				calcToolsPackageDir + "/lib/internal.ts": {
					Data: []byte(`export function internalValue(): string {
  return "shared-package-code"
}
`),
				},
			},
		}, true
	case "calc.sub":
		return TSToolDef{
			Entry: calcToolsPackageDir + "/tools/calc.sub.ts",
			Files: fstest.MapFS{
				calcToolsPackageDir + "/manifest.toml": {
					Data: []byte(`[package]
path = "github.com/solidarity-ai/calc-tools"
version = "v1.0.0"
description = "Stub calc tools"
`),
				},
				calcToolsPackageDir + "/tools/calc.sub.ts": {
					Data: []byte(`const tool = JSON.parse('{"params":{"a":{"type":"number","required":true,"description":"First number"},"b":{"type":"number","required":true,"description":"Second number"}},"metadata":{"idempotent":true,"description":"Subtract two numbers"}}');

export const params = tool.params;
export const metadata = tool.metadata;

export function execute(params: { a: number; b: number }, ctx: unknown) {
  return String(params.a - params.b)
}
`),
				},
			},
		}, true
	case "calc.asyncAdd":
		return TSToolDef{
			Entry: calcToolsPackageDir + "/tools/calc.asyncAdd.ts",
			Files: fstest.MapFS{
				calcToolsPackageDir + "/manifest.toml": {
					Data: []byte(`[package]
path = "github.com/solidarity-ai/calc-tools"
version = "v1.0.0"
description = "Stub calc tools"
`),
				},
				calcToolsPackageDir + "/tools/calc.asyncAdd.ts": {
					Data: []byte(`const tool = JSON.parse('{"params":{"a":{"type":"number","required":true,"description":"First number"},"b":{"type":"number","required":true,"description":"Second number"}},"metadata":{"idempotent":true,"description":"Add two numbers asynchronously"}}');

export const params = tool.params;
export const metadata = tool.metadata;

export async function execute(params: { a: number; b: number }, ctx: unknown) {
  return String(params.a + params.b)
}
`),
				},
			},
		}, true
	default:
		return TSToolDef{}, false
	}
}
