package toolset

import (
	"context"
	"fmt"
	"net/http"

	"github.com/solidarity-ai/toolbox/assembler"
)

// AccountParam describes an auto-generated {cred}_account parameter for
// multi-account credential selection.
type AccountParam struct {
	ParamName   string   // e.g. "google_workspace_account"
	CredName    string   // e.g. "google_workspace"
	Accounts    []string // sorted account names (enum values)
	Description string   // e.g. "Account for google_workspace credential"
}

// PreparedToolset carries the visible tools and their compiled bindings.
type PreparedToolset struct {
	tools          []PreparedTool
	byName         map[string]int
	fetchTransport http.RoundTripper
}

// FetchTransport returns the optional http.RoundTripper configured via
// Config.FetchTransport. Used to intercept fetch calls in tests.
func (r PreparedToolset) FetchTransport() http.RoundTripper {
	return r.fetchTransport
}

// PrepareTools prepares a pre-built list of loaded tools with the given config.
// This is useful for testing with synthetic tool definitions.
func PrepareTools(ctx context.Context, tools []assembler.LoadedTool, cfg Config) (PreparedToolset, error) {
	// Build binding lookup: tool ref -> param name -> Binding
	toolBindings := make(map[string]map[string]Binding, len(cfg.Tools))
	for _, bt := range cfg.Tools {
		toolBindings[bt.ToolRef] = bt.Bindings
	}

	env, err := newCELEnv()
	if err != nil {
		return PreparedToolset{}, fmt.Errorf("create CEL env: %w", err)
	}

	out := make([]PreparedTool, len(tools))
	byName := make(map[string]int, len(tools))
	policies := make(map[string]PackageCredentialPolicy)
	for i, tool := range tools {
		// Start with explicit per-tool bindings
		bindings := make(map[string]Binding)
		if tb, ok := toolBindings[tool.Name]; ok {
			for k, v := range tb {
				bindings[k] = v
			}
		}

		// Merge resource-level bindings from the two-tier model:
		// assembler.LoadedTool.ResourceParams maps param name -> canonical binding name
		// Config.ResourceBindings maps canonical name -> Binding
		for _, rp := range tool.ResourceParams {
			// Skip if explicit per-tool binding already set
			if _, exists := bindings[rp.Name]; exists {
				continue
			}
			if rb, ok := cfg.ResourceBindings[rp.BindingName]; ok {
				bindings[rp.Name] = rb
			}
		}

		var compiled map[string]compiledBinding
		var hidden map[string]bool
		if len(bindings) > 0 {
			compiled, err = compileBindings(env, bindings)
			if err != nil {
				return PreparedToolset{}, fmt.Errorf("tool %q: %w", tool.Name, err)
			}
			hidden = make(map[string]bool)
			for paramName, binding := range bindings {
				if binding.Hidden {
					hidden[paramName] = true
				}
			}
			if len(hidden) == 0 {
				hidden = nil
			}
		}

		policy := PackageCredentialPolicy{}
		if tool.PackageMeta != nil {
			if cached, ok := policies[tool.PackageMeta.Module.String()]; ok {
				policy = cached
			} else {
				policy, err = packagePolicyForTool(ctx, cfg, tool)
				if err != nil {
					return PreparedToolset{}, fmt.Errorf("tool %q: load package credential policy: %w", tool.Name, err)
				}
				policies[tool.PackageMeta.Module.String()] = policy
			}
		}

		prepared, err := buildPreparedTool(tool, compiled, hidden, cfg.EnvContext, policy)
		if err != nil {
			return PreparedToolset{}, fmt.Errorf("tool %q: %w", tool.Name, err)
		}
		out[i] = prepared
		byName[tool.Name] = i
	}

	return PreparedToolset{
		tools:          out,
		byName:         byName,
		fetchTransport: cfg.FetchTransport,
	}, nil
}

// NewPreparedToolset creates a prepared toolset from a visible tool list
// with no bindings. This is a convenience for callers that don't use bindings.
func NewPreparedToolset(tools []assembler.LoadedTool) PreparedToolset {
	out := make([]PreparedTool, len(tools))
	byName := make(map[string]int, len(tools))
	for i, tool := range tools {
		out[i] = PreparedTool{
			LoadedTool: tool,
			allowlist:  effectiveAllowlist(tool.PackageMeta, nil),
		}
		byName[tool.Name] = i
	}
	return PreparedToolset{tools: out, byName: byName}
}

// Tools returns a shallow copy of the visible tools for this prepared toolset.
func (r PreparedToolset) Tools() []PreparedTool {
	out := make([]PreparedTool, len(r.tools))
	copy(out, r.tools)
	return out
}

// Tool returns one prepared tool by name.
func (r PreparedToolset) Tool(name string) (PreparedTool, bool) {
	if idx, ok := r.byName[name]; ok {
		return r.tools[idx], true
	}
	return PreparedTool{}, false
}
