package toolset

// Binding describes how a single tool parameter is resolved at call time.
type Binding struct {
	Value  string // CEL expression: "context.customer_id", "params.channel", "'literal'"
	Hidden bool   // If true, agent never sees this param
	Check  string // Optional CEL guard: "params.channel in context.allowed_channels"
}

// BoundTool associates a tool reference with per-parameter bindings.
type BoundTool struct {
	ToolRef  string             // "zendesk@2.0.1/account.tickets.get"
	Bindings map[string]Binding // param name -> binding
}

// Config is the input to Resolve(). It carries bindings and context from the harness.
type Config struct {
	Tools            []BoundTool        // Explicitly bound tools
	ResourceBindings map[string]Binding // Resource-level bindings (e.g., account_id)
	Context          map[string]any     // Flat key-value context from harness
	Credentials      map[string]string  // Named secrets (transport layer, not visible to tools)
}
