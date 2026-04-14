package toolset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
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
	stripped.TS = nil
	stripped.TSWasm = nil

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

func toolUnavailableReasonMessage(reason ToolUnavailableReason) string {
	switch reason {
	case ToolUnavailableReasonSecretStoreLocked:
		return "the toolbox secret store is locked"
	default:
		return ""
	}
}
