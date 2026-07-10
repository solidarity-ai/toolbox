package toolsetctl

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
	builtinToolsetManagementOnce sync.Once
	builtinToolsetManagement     []builtinToolTemplate
	builtinToolsetManagementErr  error

	builtinToolboxCtlPackage = &tooldef.Package{
		Module:       builtinModulePath("toolbox/toolsetctl"),
		Name:         "toolboxctl",
		Runtime:      tooldef.RuntimeBuiltin,
		UseWhenHint:  "to add/remove/auth a tool package",
		AllowedHosts: nil,
	}
)

type builtinToolTemplate struct {
	Name        string
	Description string
	Sig         *toolbox.FuncSignature
	Effect      tooldef.Effect
	Idempotent  *bool
}

type preparedToolsetSummary struct {
	Packages []preparedPackageSummary `json:"packages"`
	Tools    []string                 `json:"tools"`
}

type preparedPackageSummary struct {
	Name        string `json:"name"`
	ToolCount   int    `json:"toolCount"`
	UseWhenHint string `json:"useWhenHint,omitempty"`
}

type builtInSourceSpec struct {
	Source string
	Effect tooldef.Effect
}

func toolsetManagementPreparedToolset(backend ToolsetBackend) (toolset.PreparedToolset, error) {
	templates, err := builtinToolsetManagementTemplates()
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
			PackageMeta: builtinToolboxCtlPackage,
		}
		switch tmpl.Name {
		case "toolbox.install":
			tool.BuiltIn = func(ctx context.Context, args map[string]any) (string, error) {
				var req InstallRequest
				if err := decodeBuiltInArgs(args, &req); err != nil {
					return "", err
				}
				prepared, err := backend.Install(ctx, req)
				if err != nil {
					return "", err
				}
				return encodeBuiltInResult(summarizePreparedToolset(prepared))
			}
		case "toolbox.uninstall":
			tool.BuiltIn = func(ctx context.Context, args map[string]any) (string, error) {
				var req UninstallRequest
				if err := decodeBuiltInArgs(args, &req); err != nil {
					return "", err
				}
				prepared, err := backend.Uninstall(ctx, req)
				if err != nil {
					return "", err
				}
				return encodeBuiltInResult(summarizePreparedToolset(prepared))
			}
		case "toolbox.auth":
			tool.BuiltIn = func(ctx context.Context, args map[string]any) (string, error) {
				var req AuthRequest
				if err := decodeBuiltInArgs(args, &req); err != nil {
					return "", err
				}
				prepared, err := backend.Auth(ctx, req)
				if err != nil {
					return "", err
				}
				return encodeBuiltInResult(summarizePreparedToolset(prepared))
			}
		default:
			return toolset.PreparedToolset{}, fmt.Errorf("unknown builtin management tool %q", tmpl.Name)
		}
		tools = append(tools, tool)
	}
	return toolset.NewPreparedToolset(tools), nil
}

func builtinToolsetManagementTemplates() ([]builtinToolTemplate, error) {
	builtinToolsetManagementOnce.Do(func() {
		builtinToolsetManagement, builtinToolsetManagementErr = loadBuiltInToolTemplates(map[string]builtInSourceSpec{
			"toolbox.install": {
				Effect: tooldef.EffectIrreversible,
				Source: `
/**
 * Install or update one package in the active toolset.
 * @effect irreversible
 */
export default async function tool(params: {
  package: string;
}): Promise<{
  packages: Array<{ name: string; toolCount: number; useWhenHint?: string }>;
  tools: string[];
}> {
  return { packages: [], tools: [] };
}
`,
			},
			"toolbox.uninstall": {
				Effect: tooldef.EffectIrreversible,
				Source: `
/**
 * Remove one installed package from the active toolset.
 * @effect irreversible
 */
export default async function tool(params: {
  target: string;
}): Promise<{
  packages: Array<{ name: string; toolCount: number; useWhenHint?: string }>;
  tools: string[];
}> {
  return { packages: [], tools: [] };
}
`,
			},
			"toolbox.auth": {
				Effect: tooldef.EffectIrreversible,
				Source: `
/**
 * Configure, check, rename, or delete credentials for one installed package.
 * @effect irreversible
 */
export default async function tool(params: {
  target: string;
  account?: string;
  credential?: string;
  check?: boolean;
  deleteCredential?: boolean;
  renameAccountFrom?: string;
  renameAccountTo?: string;
  deleteAccount?: string;
}): Promise<{
  packages: Array<{ name: string; toolCount: number; useWhenHint?: string }>;
  tools: string[];
}> {
  return { packages: [], tools: [] };
}
`,
			},
		})
	})
	return cloneBuiltInTemplates(builtinToolsetManagement), builtinToolsetManagementErr
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

func summarizePreparedToolset(prepared toolset.PreparedToolset) preparedToolsetSummary {
	grouped := map[string]preparedPackageSummary{}
	tools := make([]string, 0, len(prepared.Tools()))
	for _, tool := range prepared.Tools() {
		if tool.PackageMeta != nil && tool.PackageMeta.Runtime == tooldef.RuntimeBuiltin {
			continue
		}
		name := preparedToolPackageName(tool)
		row := grouped[name]
		row.Name = name
		row.ToolCount++
		if row.UseWhenHint == "" && tool.PackageMeta != nil {
			row.UseWhenHint = strings.TrimSpace(tool.PackageMeta.UseWhenHint)
		}
		grouped[name] = row
		tools = append(tools, tool.Name)
	}

	names := make([]string, 0, len(grouped))
	for name := range grouped {
		names = append(names, name)
	}
	sort.Strings(names)
	sort.Strings(tools)

	packages := make([]preparedPackageSummary, 0, len(names))
	for _, name := range names {
		packages = append(packages, grouped[name])
	}
	return preparedToolsetSummary{
		Packages: packages,
		Tools:    tools,
	}
}

func preparedToolPackageName(tool toolset.PreparedTool) string {
	if tool.PackageMeta != nil {
		if name := strings.TrimSpace(tool.PackageMeta.Name); name != "" {
			return name
		}
		if module := strings.TrimSpace(tool.PackageMeta.Module.String()); module != "" {
			return module
		}
	}
	if name := strings.TrimSpace(tool.Name); name != "" {
		return name
	}
	return "<unknown>"
}

func builtinModulePath(raw string) tooldef.ModulePath {
	return tooldef.ModulePath(raw)
}
