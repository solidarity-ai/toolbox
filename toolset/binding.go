package toolset

import (
	"fmt"

	"github.com/solidarity-ai/toolbox/secrets"
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
