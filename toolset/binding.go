package toolset

import (
	"fmt"

	"github.com/solidarity-ai/toolbox/secrets"
	"github.com/solidarity-ai/toolbox/transport"
)

// Binding describes how a parameter is resolved at call time.
type Binding struct {
	Value  string // CEL expression: "context.customer_id", "params.channel", "'literal'"
	Hidden bool   // If true, agent never sees this param
	Check  string // Optional CEL guard: "params.channel in context.allowed_channels"
}

// BoundTool associates a tool reference with per-param bindings.
type BoundTool struct {
	ToolRef  string             // e.g. "calc.add"
	Bindings map[string]Binding // param name -> binding
}

// ResolvedAuth is the runtime-only transport auth context for one resolved tool.
// It exposes the shared transport injector while keeping transport state out of
// agent-visible call parameters.
type ResolvedAuth struct {
	Injector *transport.Injector
}

// Rules returns the normalized runtime transport rules for inspection in tests
// and diagnostics without exposing secret values.
func (a ResolvedAuth) Rules() []transport.Rule {
	if a.Injector == nil {
		return nil
	}
	return a.Injector.Rules()
}

// SecretKeys returns the package-scoped credential keys that back this tool's
// runtime transport auth state.
func (a ResolvedAuth) SecretKeys() []string {
	rules := a.Rules()
	if len(rules) == 0 {
		return nil
	}
	keys := make([]string, 0, len(rules))
	for _, rule := range rules {
		keys = append(keys, rule.SecretKey)
	}
	return keys
}

func resolveSecretKey(namespace, credentialName string) string {
	if namespace == "" {
		return credentialName
	}
	if credentialName == "" {
		return namespace
	}
	return fmt.Sprintf("%s/%s", namespace, credentialName)
}

// Config is the input to Resolve(). It carries bindings, context, and resolved
// runtime dependencies such as transport-managed secret state.
type Config struct {
	Tools            []BoundTool        // Explicitly bound tools
	ResourceBindings map[string]Binding // Resource-level bindings by canonical name
	Context          map[string]any     // Flat key-value context from harness
	SecretStore      secrets.SecretStore
}
