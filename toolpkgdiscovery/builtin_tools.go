package toolpkgdiscovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing/fstest"

	"github.com/mackross/repljs/jswire"
	"github.com/microsoft/typescript-go/toolbox"
	"github.com/solidarity-ai/toolbox/assembler"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

var (
	builtinTemplatesOnce sync.Once
	builtinTemplates     []builtinToolTemplate
	builtinTemplatesErr  error

	builtinPackage = &tooldef.Package{
		Module:      builtinModulePath("toolbox/toolpkgdiscovery"),
		Name:        "toolpkgdiscovery",
		Runtime:     tooldef.RuntimeBuiltin,
		UseWhenHint: "there are no tool packages installed for the thing you want to do",
	}
)

type builtinToolTemplate struct {
	Name        string
	Description string
	Sig         *toolbox.FuncSignature
	Effect      tooldef.Effect
	Idempotent  *bool
}

type builtInSourceSpec struct {
	Source string
	Effect tooldef.Effect
}

// BuiltinPreparedToolset returns the builtin package discovery tools for one
// backend, using the normal prepared-tool path and synthetic package metadata.
func BuiltinPreparedToolset(backend Backend) (toolset.PreparedToolset, error) {
	templates, err := builtinToolTemplates()
	if err != nil {
		return toolset.PreparedToolset{}, err
	}
	tools := make([]assembler.LoadedTool, 0, len(templates))
	for _, tmpl := range templates {
		tool := assembler.LoadedTool{
			Name:        tmpl.Name,
			Description: tmpl.Description,
			Sig:         tmpl.Sig,
			Effect:      tmpl.Effect,
			Idempotent:  tmpl.Idempotent,
			PackageMeta: builtinPackage,
		}
		switch tmpl.Name {
		case "toolbox.search":
			tool.BuiltIn = func(ctx context.Context, args map[string]any) (string, error) {
				var req SearchRequest
				if err := decodeBuiltInArgs(args, &req); err != nil {
					return "", err
				}
				result, err := backend.Search(ctx, req)
				if err != nil {
					return "", err
				}
				return encodeBuiltInResult(result)
			}
		case "toolbox.inspect":
			tool.BuiltIn = func(ctx context.Context, args map[string]any) (string, error) {
				var req InspectRequest
				if err := decodeBuiltInArgs(args, &req); err != nil {
					return "", err
				}
				result, err := backend.Inspect(ctx, req)
				if err != nil {
					return "", err
				}
				return encodeBuiltInResult(result)
			}
		default:
			return toolset.PreparedToolset{}, fmt.Errorf("unknown builtin discovery tool %q", tmpl.Name)
		}
		tools = append(tools, tool)
	}
	return toolset.NewPreparedToolset(tools), nil
}

func builtinToolTemplates() ([]builtinToolTemplate, error) {
	builtinTemplatesOnce.Do(func() {
		builtinTemplates, builtinTemplatesErr = loadBuiltInToolTemplates(map[string]builtInSourceSpec{
			"toolbox.search": {
				Effect: tooldef.EffectReadOnly,
				Source: `
/**
 * Search available packages or tools in the toolbox registry.
 * Use this before installing a package when you need to discover candidates.
 * @effect readOnly
 * @idempotent
 */
export default async function tool(params: {
  query: string;
  tools?: boolean;
  packages?: boolean;
  runtime?: string;
  effect?: string;
  limit?: number;
  offset?: number;
}): Promise<{
  packages: Array<{
    modulePath: string;
    name: string;
    runtime: string;
    description: string;
    latestVersion: string;
    rank: number;
  }>;
  tools: Array<{
    modulePath: string;
    latestVersion: string;
    toolPath: string;
    name: string;
    description: string;
    effect: string;
    packageName: string;
    packageRuntime: string;
    rank: number;
  }>;
}> {
  return { packages: [], tools: [] };
}
`,
			},
			"toolbox.inspect": {
				Effect: tooldef.EffectReadOnly,
				Source: `
/**
 * Inspect one package target and return its resolved package metadata.
 * @effect readOnly
 * @idempotent
 */
export default async function tool(params: {
  target: string;
}): Promise<{
  target: string;
  version: string;
  source: string;
  package: unknown;
}> {
  return { target: "", version: "", source: "", package: {} };
}
`,
			},
		})
	})
	return cloneBuiltInTemplates(builtinTemplates), builtinTemplatesErr
}

func loadBuiltInToolTemplates(specs map[string]builtInSourceSpec) ([]builtinToolTemplate, error) {
	names := make([]string, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}
	sort.Strings(names)

	files := fstest.MapFS{}
	for _, name := range names {
		files["tools/"+name+".ts"] = &fstest.MapFile{Data: []byte(strings.TrimSpace(specs[name].Source) + "\n")}
	}

	templates := make([]builtinToolTemplate, 0, len(names))
	for _, name := range names {
		meta, err := toolbox.ExtractToolMetadata(context.Background(), toolbox.ExtractInput{
			Files: files,
			Entry: "tools/" + name + ".ts",
		})
		if err != nil {
			return nil, fmt.Errorf("extract builtin tool metadata for %q: %w", name, err)
		}
		if meta == nil || meta.Sig == nil {
			return nil, fmt.Errorf("extract builtin tool metadata for %q: missing function signature", name)
		}
		templates = append(templates, builtinToolTemplate{
			Name:        name,
			Description: meta.Description,
			Sig:         meta.Sig,
			Effect:      specs[name].Effect,
			Idempotent:  idempotentFromTags(meta.Sig.Tags()),
		})
	}
	return templates, nil
}

func cloneBuiltInTemplates(in []builtinToolTemplate) []builtinToolTemplate {
	if len(in) == 0 {
		return nil
	}
	out := make([]builtinToolTemplate, len(in))
	copy(out, in)
	return out
}

func idempotentFromTags(tags []toolbox.JSDocTag) *bool {
	for _, tag := range tags {
		if tag.Name == "idempotent" {
			v := true
			return &v
		}
	}
	return nil
}

func decodeBuiltInArgs(args map[string]any, dst any) error {
	if args == nil {
		args = map[string]any{}
	}
	if len(args) == 1 {
		switch nested := args["params"].(type) {
		case map[string]any:
			args = nested
		case jswire.ObjectType:
			args = map[string]any(nested)
		}
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("marshal builtin args: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decode builtin args: %w", err)
	}
	return nil
}

func encodeBuiltInResult(value any) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal builtin result: %w", err)
	}
	return string(raw), nil
}

func builtinModulePath(raw string) tooldef.ModulePath {
	return tooldef.ModulePath(raw)
}
