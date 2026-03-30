package toolset

import (
	"github.com/solidarity-ai/toolbox/audit"
	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
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

// resolveSecretKey derives the runtime secret material key for one credential
// using the shared package namespace helpers.
func resolveSecretKey(module tooldef.ModulePath, credential tooldef.PackageCredential) (string, error) {
	if credential.Type == tooldef.CredentialTypeOAuth2 {
		return tooldef.CredentialFamilySecretKey(module, "", credential.Name, "access_token")
	}
	return tooldef.CredentialSecretKey(module, credential.Name)
}

func resolveOAuth2SecretFamily(module tooldef.ModulePath, credential tooldef.PackageCredential) (string, error) {
	return tooldef.CredentialFamilyNamespace(module, "", credential.Name)
}

func resolveOAuth2CacheKey(module tooldef.ModulePath, credential tooldef.PackageCredential) string {
	return module.String() + ":" + credential.Name
}

// Config is the input to Resolve(). It carries bindings, context, and resolved
// runtime dependencies such as transport-managed secret state.
type Config struct {
	Tools            []BoundTool        // Explicitly bound tools
	ResourceBindings map[string]Binding // Resource-level bindings by canonical name
	Context          map[string]any     // Flat key-value context from harness
	SecretStore      secrets.SecretStore
	AuditSink        audit.Sink
}
