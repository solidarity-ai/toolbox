package tool

import (
	"io/fs"
	"testing/fstest"
)

const calcToolsPackageDir = "github.com/solidarity-ai/calc-tools"

// TSToolDef is the smallest useful TS tool definition for the current invoke
// seam. It points at one tool entry file inside a package-shaped filesystem.
type TSToolDef struct {
	Entry string
	Files fs.FS
}

// StubTSToolDef returns a package-shaped TS tool definition for known stub
// tools used in the current outside-in tests.
func StubTSToolDef(name string) (TSToolDef, bool) {
	if name != "calc.add" {
		return TSToolDef{}, false
	}

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
		},
	}, true
}
