package toolset

import (
	"context"
	"fmt"

	"github.com/solidarity-ai/toolbox/assembler"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/transport"
)

// PreparedTool is one loaded tool prepared for a specific toolset configuration.
// It owns tool-scoped prepared state instead of storing it in toolset-wide maps.
type PreparedTool struct {
	assembler.LoadedTool
	bindings           map[string]compiledBinding
	hiddenParams       map[string]bool
	accountParams      []AccountParam
	credentialAccounts map[string][]string
	injector           *transport.CredentialInjector
	allowlist          *transport.HostAllowlist
	context            map[string]any
}

func (t PreparedTool) HiddenParams() map[string]bool { return t.hiddenParams }

func (t PreparedTool) AccountParams() []AccountParam { return t.accountParams }

func (t PreparedTool) Injector() *transport.CredentialInjector { return t.injector }

func (t PreparedTool) Allowlist() *transport.HostAllowlist { return t.allowlist }

func (t PreparedTool) MaxFetchResponseBytes() *int64 { return t.LoadedTool.MaxFetchResponseBytes }

func (t PreparedTool) ValidateCall(agentParams map[string]any) (map[string]any, error) {
	if len(t.bindings) == 0 {
		return agentParams, nil
	}

	context := t.context
	if context == nil {
		context = map[string]any{}
	}

	fullParams := make(map[string]any, len(agentParams)+len(t.bindings))
	for k, v := range agentParams {
		fullParams[k] = v
	}

	for paramName, cb := range t.bindings {
		if cb.checkProgram == nil {
			continue
		}
		pass, err := evalCheck(cb.checkProgram, agentParams, context)
		if err != nil {
			return nil, fmt.Errorf("param %q: %w", paramName, err)
		}
		if !pass {
			return nil, fmt.Errorf("check failed for param %q on tool %q", paramName, t.Name)
		}
	}

	for paramName, cb := range t.bindings {
		if cb.valueProgram == nil {
			continue
		}
		val, err := evalBinding(cb.valueProgram, agentParams, context)
		if err != nil {
			return nil, fmt.Errorf("param %q: %w", paramName, err)
		}
		fullParams[paramName] = val
	}

	return fullParams, nil
}

func (t PreparedTool) boundLiterals() map[string]any {
	if len(t.bindings) == 0 {
		return nil
	}
	context := t.context
	if context == nil {
		context = map[string]any{}
	}

	var literals map[string]any
	for paramName, cb := range t.bindings {
		if t.hiddenParams[paramName] || cb.valueProgram == nil {
			continue
		}
		val, err := evalBinding(cb.valueProgram, map[string]any{}, context)
		if err != nil {
			continue
		}
		if literals == nil {
			literals = make(map[string]any)
		}
		literals[paramName] = val
	}
	return literals
}

func (t PreparedTool) ScopedInjector(fullParams map[string]any, injector *transport.CredentialInjector) (*transport.CredentialInjector, error) {
	if injector == nil || t.PackageMeta == nil {
		return injector, nil
	}

	credNames := credentialNames(t.PackageMeta.Credentials)
	accounts := extractAccountParams(fullParams, credNames)
	for _, name := range credNames {
		if _, ok := accounts[name]; ok {
			continue
		}
		if accts := t.credentialAccounts[name]; len(accts) == 1 {
			accounts[name] = accts[0]
		}
	}
	if len(accounts) == 0 {
		return injector, nil
	}

	scoped, err := injector.WithAccounts(accounts)
	if err != nil {
		return nil, fmt.Errorf("account scoping: %w", err)
	}
	return scoped, nil
}

func packagePolicyForTool(ctx context.Context, cfg Config, tool assembler.LoadedTool) (PackageCredentialPolicy, error) {
	if tool.PackageMeta == nil || cfg.CredentialPolicySource == nil {
		return PackageCredentialPolicy{}, nil
	}
	return cfg.CredentialPolicySource.PackageCredentialPolicy(ctx, *tool.PackageMeta)
}

func buildPreparedTool(tool assembler.LoadedTool, bindings map[string]compiledBinding, hidden map[string]bool, context map[string]any, policy PackageCredentialPolicy) (PreparedTool, error) {
	prepared := PreparedTool{
		LoadedTool:         tool,
		bindings:           bindings,
		hiddenParams:       hidden,
		credentialAccounts: policy.CredentialAccounts,
		injector:           policy.Injector,
		allowlist:          policy.Allowlist,
		context:            context,
	}

	accountParams, err := buildAccountParams(prepared, policy.CredentialAccounts)
	if err != nil {
		return PreparedTool{}, err
	}
	prepared.accountParams = accountParams

	return prepared, nil
}

func credentialNames(creds []tooldef.PackageCredential) []string {
	names := make([]string, len(creds))
	for i, c := range creds {
		names[i] = c.Name
	}
	return names
}
