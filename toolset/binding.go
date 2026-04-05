package toolset

import (
	"context"

	tooldef "github.com/solidarity-ai/toolbox/tool"
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

// PackageCredentialPolicy carries package-scoped execution attachments and credential
// account catalogs. Package module identity is resolved outside of toolset.
type PackageCredentialPolicy struct {
	CredentialAccounts map[string][]string // credName → []accountName
	Injector           *transport.CredentialInjector
	Allowlist          *transport.HostAllowlist
}

// PackageCredentialPolicySource loads the scoped runtime credential policy for one package.
type PackageCredentialPolicySource interface {
	PackageCredentialPolicy(ctx context.Context, pkg tooldef.Package) (PackageCredentialPolicy, error)
}

// Config is the input to PrepareTools(). It carries bindings, environment context,
// and request-scoped execution attachments.
type Config struct {
	Tools                  []BoundTool        // Explicitly bound tools
	ResourceBindings       map[string]Binding // Resource-level bindings by canonical name
	EnvContext             map[string]any     // Flat key-value environment context from harness
	CredentialPolicySource PackageCredentialPolicySource
}
