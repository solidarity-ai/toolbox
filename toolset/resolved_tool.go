package toolset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"

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
	unavailableReason  ToolUnavailableReason
	NeedsApproval      bool
	jsonCallable       bool
	jsonCallWhyNot     string
}

type ToolUnavailableReason string

const ToolUnavailableReasonSecretStoreLocked ToolUnavailableReason = "secret_store_locked"

type ToolUnavailableError struct {
	ToolName string
	Reason   ToolUnavailableReason
}

func (e *ToolUnavailableError) Error() string {
	if e == nil {
		return "tool is unavailable"
	}
	if reason := strings.TrimSpace(toolUnavailableReasonMessage(e.Reason)); reason != "" {
		return fmt.Sprintf("tool %s is unavailable because %s", e.ToolName, reason)
	}
	return fmt.Sprintf("tool %s is unavailable", e.ToolName)
}

func (t PreparedTool) HiddenParams() map[string]bool { return t.hiddenParams }

func (t PreparedTool) AccountParams() []AccountParam { return t.accountParams }

func (t PreparedTool) RequiresCredentials() bool {
	return t.PackageMeta != nil && len(t.PackageMeta.Credentials) > 0
}

// SelectedCredentialAccountParams returns credential account selections keyed
// by their agent-facing param names, for example "workspace_account".
func (t PreparedTool) SelectedCredentialAccountParams(fullParams map[string]any) map[string]string {
	if len(t.credentialAccounts) == 0 {
		return nil
	}
	credNames := credentialSelectionNames(t)
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
		return nil
	}
	out := make(map[string]string, len(accounts))
	for cred, account := range accounts {
		out[cred+"_account"] = account
	}
	return out
}

func (t PreparedTool) Injector() *transport.CredentialInjector { return t.injector }

func (t PreparedTool) Allowlist() *transport.HostAllowlist { return t.allowlist }

func (t PreparedTool) MaxFetchResponseBytes() *int64 { return t.LoadedTool.MaxFetchResponseBytes }

func (t PreparedTool) Unavailable() bool { return t.unavailableReason != "" }

func (t PreparedTool) UnavailableReason() ToolUnavailableReason { return t.unavailableReason }

func (t PreparedTool) UnavailableMessage() string {
	return toolUnavailableReasonMessage(t.unavailableReason)
}

func (t PreparedTool) UnavailableError() error {
	if !t.Unavailable() {
		return nil
	}
	return &ToolUnavailableError{
		ToolName: t.Name,
		Reason:   t.unavailableReason,
	}
}

// JSONCallable reports whether this tool's prepared input surface can be
// supplied faithfully over the direct JSON transport used by non-codemode
// surfaces.
func (t PreparedTool) JSONCallable() bool { return t.jsonCallable }

// JSONCallWhyNot explains why JSONCallable is false.
func (t PreparedTool) JSONCallWhyNot() string { return t.jsonCallWhyNot }

// ToolCallReturnsTask reports whether notebook callers should receive a
// ToolCallTask<T> instead of a ToolCallPromise<T>.
func (t PreparedTool) ToolCallReturnsTask() bool { return t.NeedsApproval }

// ToolApprovalPackageName returns the package segment used in tool_approvals keys.
func (t PreparedTool) ToolApprovalPackageName() string {
	if t.PackageMeta != nil {
		switch {
		case strings.TrimSpace(t.PackageMeta.Name) != "":
			return strings.TrimSpace(t.PackageMeta.Name)
		case strings.TrimSpace(t.PackageMeta.Module.String()) != "":
			return strings.TrimSpace(t.PackageMeta.Module.String())
		}
	}
	return ""
}

// ToolApprovalKey returns the fully-qualified tool_approvals key.
func (t PreparedTool) ToolApprovalKey() string {
	packageName := t.ToolApprovalPackageName()
	toolName := strings.TrimSpace(t.Name)
	if packageName == "" {
		return toolName
	}
	if toolName != "" {
		return packageName + "." + toolName
	}
	return packageName
}

// FileSystem returns the filesystem containing the tool's files.
func (t PreparedTool) FileSystem() fs.FS {
	switch {
	case t.TS != nil:
		return t.TS.Files
	case t.TSWasm != nil:
		return t.TSWasm.Files
	default:
		return nil
	}
}

// ContentHash returns a hash of the tool's file contents for identification.
func (t PreparedTool) ContentHash() string {
	fsys := t.FileSystem()
	if fsys == nil {
		return ""
	}
	hash := sha256.New()
	if err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(hash, path); err != nil {
			return err
		}
		if _, err := hash.Write([]byte{0}); err != nil {
			return err
		}
		if _, err := hash.Write(data); err != nil {
			return err
		}
		_, err = hash.Write([]byte{0})
		return err
	}); err != nil {
		return ""
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// CacheKey returns a stable key suitable for caching and session identification.
func (t PreparedTool) CacheKey() string {
	var parts []string
	if t.PackageMeta != nil {
		if module := strings.TrimSpace(t.PackageMeta.Module.String()); module != "" {
			parts = append(parts, "module="+module)
		}
		if name := strings.TrimSpace(t.PackageMeta.Name); name != "" {
			parts = append(parts, "name="+name)
		}
		if runtime := strings.TrimSpace(string(t.PackageMeta.Runtime)); runtime != "" {
			parts = append(parts, "runtime="+runtime)
		}
		if sha := strings.TrimSpace(t.PackageMeta.SHA256); sha != "" {
			parts = append(parts, "sha="+sha)
		}
	}
	if t.PackageMeta == nil || strings.TrimSpace(t.PackageMeta.SHA256) == "" {
		if contentHash := t.ContentHash(); contentHash != "" {
			parts = append(parts, "content="+contentHash)
		}
	}
	if len(parts) == 0 {
		switch {
		case t.TS != nil:
			parts = append(parts, "entry="+t.TS.Entry)
		case t.TSWasm != nil:
			parts = append(parts, "entry="+t.TSWasm.Entry)
		default:
			parts = append(parts, "tool="+t.Name)
		}
	}
	return strings.Join(parts, "\n")
}

// ApprovalFingerprint returns a stable identifier for the reviewed prepared
// execution surface of this tool, including bindings and credential policy.
func (t PreparedTool) ApprovalFingerprint() string {
	payload := struct {
		Name               string                    `json:"name"`
		CacheKey           string                    `json:"cache_key"`
		NeedsApproval      bool                      `json:"needs_approval"`
		UnavailableReason  ToolUnavailableReason     `json:"unavailable_reason,omitempty"`
		HiddenParams       map[string]bool           `json:"hidden_params,omitempty"`
		Bindings           map[string]Binding        `json:"bindings,omitempty"`
		AccountParams      []AccountParam            `json:"account_params,omitempty"`
		CredentialAccounts map[string][]string       `json:"credential_accounts,omitempty"`
		AllowlistPatterns  []string                  `json:"allowlist_patterns,omitempty"`
		InjectorRules      []transport.InjectionRule `json:"injector_rules,omitempty"`
		Context            map[string]any            `json:"context,omitempty"`
	}{
		Name:              t.Name,
		CacheKey:          t.CacheKey(),
		NeedsApproval:     t.NeedsApproval,
		UnavailableReason: t.unavailableReason,
		Context:           cloneApprovalContext(t.context),
	}

	if len(t.hiddenParams) > 0 {
		payload.HiddenParams = make(map[string]bool, len(t.hiddenParams))
		for name, hidden := range t.hiddenParams {
			payload.HiddenParams[name] = hidden
		}
	}

	if len(t.bindings) > 0 {
		payload.Bindings = make(map[string]Binding, len(t.bindings))
		for name, binding := range t.bindings {
			payload.Bindings[name] = binding.Binding
		}
	}

	if len(t.accountParams) > 0 {
		payload.AccountParams = append([]AccountParam(nil), t.accountParams...)
		sort.Slice(payload.AccountParams, func(i, j int) bool {
			return payload.AccountParams[i].ParamName < payload.AccountParams[j].ParamName
		})
	}

	if len(t.credentialAccounts) > 0 {
		payload.CredentialAccounts = make(map[string][]string, len(t.credentialAccounts))
		for credName, accounts := range t.credentialAccounts {
			sorted := append([]string(nil), accounts...)
			sort.Strings(sorted)
			payload.CredentialAccounts[credName] = sorted
		}
	}

	if patterns := t.allowlist.Patterns(); len(patterns) > 0 {
		payload.AllowlistPatterns = append([]string(nil), patterns...)
		sort.Strings(payload.AllowlistPatterns)
	}

	if rules := t.injector.Rules(); len(rules) > 0 {
		for i := range rules {
			if len(rules[i].Hosts) > 0 {
				rules[i].Hosts = append([]string(nil), rules[i].Hosts...)
				sort.Strings(rules[i].Hosts)
			}
		}
		sort.Slice(rules, func(i, j int) bool {
			return approvalInjectionRuleSortKey(rules[i]) < approvalInjectionRuleSortKey(rules[j])
		})
		payload.InjectorRules = rules
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return t.CacheKey()
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (t PreparedTool) ValidateCall(agentParams map[string]any) (map[string]any, error) {
	if err := t.UnavailableError(); err != nil {
		return nil, err
	}
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
		allowlist:          effectiveAllowlist(tool.PackageMeta, policy.Allowlist),
		context:            context,
	}

	accountParams, err := buildAccountParams(prepared, policy.CredentialAccounts)
	if err != nil {
		return PreparedTool{}, err
	}
	prepared.accountParams = accountParams
	prepared.setJSONCallable()

	return prepared, nil
}

func buildUnavailablePreparedTool(tool assembler.LoadedTool, bindings map[string]compiledBinding, hidden map[string]bool, context map[string]any, reason ToolUnavailableReason) (PreparedTool, error) {
	stripped := tool
	stripped.BuiltIn = nil

	prepared, err := buildPreparedTool(stripped, bindings, hidden, context, PackageCredentialPolicy{})
	if err != nil {
		return PreparedTool{}, err
	}
	prepared.unavailableReason = reason
	return prepared, nil
}

func (t *PreparedTool) setJSONCallable() {
	ok, whyNot := jsonCallableForTool(*t)
	t.jsonCallable = ok
	t.jsonCallWhyNot = whyNot
}

func effectiveAllowlist(pkg *tooldef.Package, override *transport.HostAllowlist) *transport.HostAllowlist {
	if override != nil {
		return override
	}
	if pkg == nil {
		return transport.NewHostAllowlist(nil)
	}
	return transport.NewHostAllowlist(pkg.AllowedHosts)
}

func credentialNames(creds []tooldef.PackageCredential) []string {
	names := make([]string, len(creds))
	for i, c := range creds {
		names[i] = c.Name
	}
	return names
}

func credentialSelectionNames(tool PreparedTool) []string {
	if tool.PackageMeta != nil && len(tool.PackageMeta.Credentials) > 0 {
		return credentialNames(tool.PackageMeta.Credentials)
	}
	names := make([]string, 0, len(tool.credentialAccounts))
	for name := range tool.credentialAccounts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func toolUnavailableReasonMessage(reason ToolUnavailableReason) string {
	switch reason {
	case ToolUnavailableReasonSecretStoreLocked:
		return "the toolbox secret store is locked"
	default:
		return ""
	}
}

func cloneApprovalContext(context map[string]any) map[string]any {
	if len(context) == 0 {
		return nil
	}
	out := make(map[string]any, len(context))
	for key, value := range context {
		out[key] = value
	}
	return out
}

func approvalInjectionRuleSortKey(rule transport.InjectionRule) string {
	providerAuthURL := ""
	providerTokenURL := ""
	if rule.Provider != nil {
		providerAuthURL = rule.Provider.AuthURL
		providerTokenURL = rule.Provider.TokenURL
	}
	return strings.Join([]string{
		strings.Join(rule.Hosts, ","),
		rule.PathPrefix,
		rule.ModuleName,
		rule.CredentialName,
		rule.SecretPrefix,
		string(rule.Type),
		string(rule.Method),
		rule.HeaderName,
		providerAuthURL,
		providerTokenURL,
	}, "\x00")
}
