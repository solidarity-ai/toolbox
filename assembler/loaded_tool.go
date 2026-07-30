package assembler

import (
	"path/filepath"
	"strings"

	"github.com/microsoft/typescript-go/toolbox"
	"github.com/solidarity-ai/toolbox/packaging"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// LoadedTool is the execution-ready tool shape materialized from a loaded package.
type LoadedTool struct {
	Name                  string
	Description           string
	Sig                   *toolbox.FuncSignature
	Effect                tooldef.Effect
	Idempotent            *bool
	MaxFetchResponseBytes *int64
	ResourceUses          []tooldef.ResourceUse
	PackageMeta           *tooldef.Package
	PackageVersion        tooldef.Version
	BuiltIn               BuiltInFunc
	TS                    *tooldef.TSToolDef
	TSWasm                *tooldef.TSWasmToolDef
}

// LoadedTools derives loaded tool records from a loaded package.
func LoadedTools(p packaging.LoadedPackage) []LoadedTool {
	tools := make([]LoadedTool, 0, len(p.Package.Tools))
	for _, pkgTool := range p.Package.Tools {
		description := pkgTool.Description
		if description == "" {
			description = inferToolName(pkgTool.EntryTS)
		}
		loaded := LoadedTool{
			Name:                  inferToolName(pkgTool.EntryTS),
			Description:           description,
			Sig:                   pkgTool.Sig,
			Effect:                pkgTool.Effect,
			Idempotent:            pkgTool.Idempotent,
			MaxFetchResponseBytes: pkgTool.MaxFetchResponseBytes,
			ResourceUses:          tooldef.CloneResourceUses(pkgTool.ResourceUses),
			PackageMeta:           &p.Package,
		}

		baseDef := tooldef.TSToolDef{
			Entry:       pkgTool.EntryTS,
			Files:       p.Files,
			PackageRoot: p.Dir,
		}

		if p.Package.Runtime == tooldef.RuntimeTypeScriptWasixSandbox ||
			p.Package.Runtime == tooldef.RuntimeTypeScriptWasip2Sandbox {
			loaded.TSWasm = &tooldef.TSWasmToolDef{
				TSToolDef:   baseDef,
				Executables: p.Package.Executables,
			}
		} else {
			loaded.TS = &baseDef
		}

		tools = append(tools, loaded)
	}
	return tools
}

func inferToolName(entryTS string) string {
	base := filepath.Base(entryTS)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	parts := strings.Split(name, ".")
	for i, part := range parts {
		parts[i] = kebabToCamel(part)
	}
	return strings.Join(parts, ".")
}

func kebabToCamel(s string) string {
	segments := strings.Split(s, "-")
	for i := 1; i < len(segments); i++ {
		if len(segments[i]) > 0 {
			segments[i] = strings.ToUpper(segments[i][:1]) + segments[i][1:]
		}
	}
	return strings.Join(segments, "")
}
