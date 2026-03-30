package toolset

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/transport"
)

// ErrNoResolver is returned by AddFromRegistry when the Builder was created
// without a registry resolver.
var ErrNoResolver = errors.New("no registry resolver configured")

// ResolvedToolset carries the visible tools and their compiled bindings.
type ResolvedToolset struct {
	tools             []tooldef.ResolvedTool
	bindings          map[string]map[string]compiledBinding // tool name -> param name -> compiled binding
	hiddenParams      map[string]map[string]bool            // tool name -> set of hidden param names
	transportPolicies map[string]*transport.Policy          // tool name -> runtime-only transport policy
	context           map[string]any
	celEnv            *cel.Env
}

// Builder incrementally assembles a toolset from source package directories.
//
// For now it only records loaded packages from
// toolbox.devpkg.json. Tool selection and binding come later.
type Builder struct {
	packages []packaging.LoadedPackage
	resolver *registry.Resolver
}

// New creates an empty toolset builder.
func New() *Builder {
	return &Builder{}
}

// NewWithResolver creates a toolset builder that can resolve registry packages.
func NewWithResolver(resolver *registry.Resolver) *Builder {
	return &Builder{resolver: resolver}
}

// AddFromDir loads a package rooted at dir.
//
// A directory is treated as a package iff it contains toolbox.devpkg.json.
func (b *Builder) AddFromDir(dir string) error {
	pkg, err := packaging.LoadDev(dir)
	if err != nil {
		return err
	}
	b.packages = append(b.packages, pkg)
	return nil
}

// AddFromArchive loads a package from a .toolbox.pkg archive and its manifest.
func (b *Builder) AddFromArchive(archivePath, manifestPath string) error {
	pkg, err := packaging.LoadArchive(archivePath, manifestPath)
	if err != nil {
		return err
	}
	b.packages = append(b.packages, pkg)
	return nil
}

// AddFromRegistry resolves a registry package by module path and version,
// then appends it to the builder's package list.
func (b *Builder) AddFromRegistry(ctx context.Context, modulePath, version string) error {
	_, err := b.AddFromRegistryWithExpected(ctx, modulePath, version, nil)
	return err
}

// AddFromRegistryWithExpected resolves a registry package through the shared
// resolver path, optionally verifying cached/fetched bytes against expected
// lock metadata, then appends the loaded package and returns the metadata that
// was trusted for this package.
func (b *Builder) AddFromRegistryWithExpected(ctx context.Context, modulePath, version string, expected *registry.ResolveMetadata) (registry.ResolveMetadata, error) {
	if b.resolver == nil {
		return registry.ResolveMetadata{}, ErrNoResolver
	}

	module, err := tooldef.ParseModulePath(modulePath)
	if err != nil {
		return registry.ResolveMetadata{}, fmt.Errorf("parse module path: %w", err)
	}
	ver, err := tooldef.ParseVersion(version)
	if err != nil {
		return registry.ResolveMetadata{}, fmt.Errorf("parse version: %w", err)
	}

	result, err := b.resolver.ResolveWithExpected(ctx, module, ver, expected)
	if err != nil {
		return registry.ResolveMetadata{}, err
	}
	b.packages = append(b.packages, result.Package)
	return result.Metadata, nil
}

// Packages returns the currently loaded source packages.
func (b *Builder) Packages() []tooldef.Package {
	out := make([]tooldef.Package, len(b.packages))
	for i, loaded := range b.packages {
		out[i] = loaded.Package
	}
	return out
}

// Resolve materializes visible tools from loaded packages, compiling any
// bindings from cfg. An empty Config{} produces the same result as before
// bindings existed — all tools visible, no bindings applied.
func (b *Builder) Resolve(cfg Config) (ResolvedToolset, error) {
	var tools []tooldef.ResolvedTool
	for _, loaded := range b.packages {
		tools = append(tools, loaded.ResolvedTools()...)
	}
	return b.resolveTools(tools, cfg)
}

func (b *Builder) resolveTools(tools []tooldef.ResolvedTool, cfg Config) (ResolvedToolset, error) {
	// Build binding lookup: tool ref -> param name -> Binding
	toolBindings := make(map[string]map[string]Binding, len(cfg.Tools))
	for _, bt := range cfg.Tools {
		toolBindings[bt.ToolRef] = bt.Bindings
	}

	env, err := newCELEnv()
	if err != nil {
		return ResolvedToolset{}, fmt.Errorf("create CEL env: %w", err)
	}

	allCompiled := make(map[string]map[string]compiledBinding, len(toolBindings))
	allHidden := make(map[string]map[string]bool)
	allTransportPolicies := make(map[string]*transport.Policy)

	for _, tool := range tools {
		// Start with explicit per-tool bindings
		bindings := make(map[string]Binding)
		if tb, ok := toolBindings[tool.Name]; ok {
			for k, v := range tb {
				bindings[k] = v
			}
		}

		// Merge resource-level bindings from the two-tier model:
		// ResolvedTool.ResourceParams maps param name -> canonical binding name
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

		if len(bindings) > 0 {
			compiled, err := compileBindings(env, bindings)
			if err != nil {
				return ResolvedToolset{}, fmt.Errorf("tool %q: %w", tool.Name, err)
			}
			allCompiled[tool.Name] = compiled

			hidden := make(map[string]bool)
			for paramName, binding := range bindings {
				if binding.Hidden {
					hidden[paramName] = true
				}
			}
			if len(hidden) > 0 {
				allHidden[tool.Name] = hidden
			}
		}

		if policy, err := resolveToolTransportPolicy(tool, cfg); err != nil {
			return ResolvedToolset{}, fmt.Errorf("tool %q: %w", tool.Name, err)
		} else if policy != nil {
			allTransportPolicies[tool.Name] = policy
		}
	}

	out := make([]tooldef.ResolvedTool, len(tools))
	copy(out, tools)
	return ResolvedToolset{
		tools:             out,
		bindings:          allCompiled,
		hiddenParams:      allHidden,
		transportPolicies: allTransportPolicies,
		context:           cfg.Context,
		celEnv:            env,
	}, nil
}

func resolveToolTransportPolicy(tool tooldef.ResolvedTool, cfg Config) (*transport.Policy, error) {
	rules, err := resolveToolTransportRules(tool)
	if err != nil {
		return nil, err
	}
	return transport.NewPolicyWithOptions(
		cfg.SecretStore,
		rules,
		resolveToolAllowedHosts(tool),
		toolRequiresRuntimeTransportPolicy(tool),
		transport.WithAuditSink(cfg.AuditSink),
	)
}

func toolRequiresRuntimeTransportPolicy(tool tooldef.ResolvedTool) bool {
	if tool.TS != nil || tool.TSWasm != nil {
		return true
	}
	if tool.Package == nil {
		return false
	}
	switch tool.Package.Runtime {
	case tooldef.RuntimeTypeScriptSandbox, tooldef.RuntimeTypeScriptWasixSandbox, tooldef.RuntimeTypeScriptWasip2Sandbox:
		return true
	default:
		return false
	}
}

func resolveToolTransportRules(tool tooldef.ResolvedTool) ([]transport.Rule, error) {
	if len(tool.EffectiveCredentials) == 0 {
		return nil, nil
	}
	if tool.Package == nil {
		return nil, fmt.Errorf("resolved credentials require package metadata")
	}

	rules := make([]transport.Rule, 0, len(tool.EffectiveCredentials))
	for _, declared := range tool.EffectiveCredentials {
		secretKey, err := resolveSecretKey(tool.Package.Module, declared)
		if err != nil {
			return nil, fmt.Errorf("credential %q: %w", declared.Name, err)
		}

		var provider *tooldef.OAuth2ProviderConfig
		oauth2SecretFamily := ""
		oauth2CacheKey := ""
		if declared.Type == tooldef.CredentialTypeOAuth2 {
			resolvedProvider, err := tooldef.ResolveOAuth2Provider(declared.Provider)
			if err != nil {
				return nil, fmt.Errorf("credential %q provider: %w", declared.Name, err)
			}
			provider = &resolvedProvider

			oauth2SecretFamily, err = resolveOAuth2SecretFamily(tool.Package.Module, declared)
			if err != nil {
				return nil, fmt.Errorf("credential %q oauth2 secret family: %w", declared.Name, err)
			}
			oauth2CacheKey = resolveOAuth2CacheKey(tool.Package.Module, declared)
		}

		rules = append(rules, transport.Rule{
			Name:               declared.Name,
			Type:               declared.Type,
			OAuth2Provider:     provider,
			OAuth2SecretFamily: oauth2SecretFamily,
			OAuth2CacheKey:     oauth2CacheKey,
			Scopes:             append([]string(nil), declared.Scopes...),
			SecretKey:          secretKey,
			Inject:             declared.Inject,
		})
	}
	return rules, nil
}

func resolveToolAllowedHosts(tool tooldef.ResolvedTool) []string {
	return transport.NormalizeAllowedHosts(tool.AllowedHosts)
}

// ResolveTools resolves a pre-built list of tools with the given config.
// This is useful for testing with synthetic tool definitions.
func ResolveTools(tools []tooldef.ResolvedTool, cfg Config) (ResolvedToolset, error) {
	b := &Builder{}
	// Inject pre-resolved tools directly into the resolve flow.
	return b.resolveTools(tools, cfg)
}

// NewResolvedToolset creates a resolved toolset from a visible tool list
// with no bindings. This is a convenience for callers that don't use bindings.
func NewResolvedToolset(tools []tooldef.ResolvedTool) ResolvedToolset {
	out := make([]tooldef.ResolvedTool, len(tools))
	copy(out, tools)
	transportPolicies := make(map[string]*transport.Policy)
	for _, tool := range out {
		policy, err := resolveToolTransportPolicy(tool, Config{})
		if err == nil && policy != nil {
			transportPolicies[tool.Name] = policy
		}
	}
	return ResolvedToolset{tools: out, transportPolicies: transportPolicies}
}

// Tools returns a shallow copy of the visible tools for this resolved toolset.
func (r ResolvedToolset) Tools() []tooldef.ResolvedTool {
	out := make([]tooldef.ResolvedTool, len(r.tools))
	copy(out, r.tools)
	return out
}

// ToolTransportPolicy returns the runtime-only shared transport policy for a
// resolved tool.
func (r ResolvedToolset) ToolTransportPolicy(toolName string) (*transport.Policy, bool) {
	policy, ok := r.transportPolicies[toolName]
	return policy, ok
}

// ToolAllowedHosts returns the effective runtime-only host allowlist for a resolved tool.
func (r ResolvedToolset) ToolAllowedHosts(toolName string) ([]string, bool) {
	policy, ok := r.transportPolicies[toolName]
	if !ok || policy == nil {
		return nil, false
	}
	allowedHosts := policy.AllowedHosts()
	if len(allowedHosts) == 0 {
		return nil, false
	}
	return allowedHosts, true
}
