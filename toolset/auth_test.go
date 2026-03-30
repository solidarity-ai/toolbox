package toolset_test

import (
	"strings"
	"testing"

	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestResolveToolAuthDerivesPackageScopedCredentialNamespace(t *testing.T) {
	t.Parallel()

	resolved := authResolvedToolset(t)
	policy, ok := resolved.ToolTransportPolicy("github.issues.get")
	if !ok {
		t.Fatal("expected runtime transport policy for tool")
	}
	rules := policy.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 runtime rule, got %d", len(rules))
	}
	rule := rules[0]
	if rule.SecretKey != "github.com/example/github-issues/github_token" {
		t.Fatalf("secret key = %q, want package-scoped credential key", rule.SecretKey)
	}
	secretKeys := make([]string, 0, len(rules))
	for _, resolvedRule := range rules {
		secretKeys = append(secretKeys, resolvedRule.SecretKey)
	}
	if len(secretKeys) != 1 || secretKeys[0] != rule.SecretKey {
		t.Fatalf("transport policy secret keys = %v, want [%q]", secretKeys, rule.SecretKey)
	}
}

func TestResolveToolAuthUsesReservedAccessTokenFamilyKeyForOAuth2(t *testing.T) {
	t.Parallel()

	resolved, err := toolset.ResolveTools([]tooldef.ResolvedTool{{
		Name:        "github.issues.get",
		Description: "Get a GitHub issue",
		Package: &tooldef.Package{
			Module:  "github.com/example/github-issues",
			Name:    "github-issues",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
		},
		EffectiveCredentials: []tooldef.PackageCredential{{
			Name: "github_oauth",
			Type: tooldef.CredentialTypeOAuth2,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{"api.github.com"},
				Method: "bearer_header",
			},
		}},
	}}, toolset.Config{})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}

	policy, ok := resolved.ToolTransportPolicy("github.issues.get")
	if !ok {
		t.Fatal("expected runtime transport policy for tool")
	}
	rules := policy.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 runtime rule, got %d", len(rules))
	}
	if rules[0].SecretKey != "github.com/example/github-issues/github_oauth/access_token" {
		t.Fatalf("secret key = %q, want oauth2 access token family key", rules[0].SecretKey)
	}
}

func TestResolveToolAuthRejectsAmbiguousCanonicalRules(t *testing.T) {
	t.Parallel()

	credentials := []tooldef.PackageCredential{
		{
			Name: "primary_token",
			Type: tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:      []string{" API.GitHub.com "},
				PathPrefix: "/repos/",
				Method:     "bearer_header",
			},
		},
		{
			Name: "fallback_token",
			Type: tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:      []string{"api.github.com"},
				PathPrefix: "/repos",
				Method:     "bearer_header",
			},
		},
	}

	_, err := toolset.ResolveTools([]tooldef.ResolvedTool{{
		Name:                 "github.issues.get",
		Description:          "Get a GitHub issue",
		Package:              &tooldef.Package{Module: "github.com/example/github-issues", Name: "github-issues", Runtime: tooldef.RuntimeTypeScriptSandbox},
		EffectiveCredentials: credentials,
	}}, toolset.Config{})
	if err == nil {
		t.Fatal("expected ambiguous credential rules to fail resolution")
	}
	if !strings.Contains(err.Error(), "ambiguous transport credential rules") {
		t.Fatalf("ResolveTools error = %v, want ambiguity context", err)
	}
}

func TestResolveToolAuthUsesEffectiveCredentialOverrideAndExplicitNoCredentials(t *testing.T) {
	t.Parallel()

	basePkg := tooldef.Package{
		Module:  "github.com/example/multi-auth",
		Name:    "multi-auth",
		Runtime: tooldef.RuntimeTypeScriptSandbox,
		Credentials: []tooldef.PackageCredential{{
			Name: "package_token",
			Type: tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{"api.github.com"},
				Method: "bearer_header",
			},
		}},
	}

	tools := []tooldef.ResolvedTool{
		{
			Name:                 "package.default",
			Description:          "Uses inherited credential",
			Package:              &basePkg,
			EffectiveCredentials: basePkg.Credentials,
		},
		{
			Name:        "package.none",
			Description: "Explicit no-auth tool",
			Package:     &basePkg,
		},
		{
			Name:        "package.override",
			Description: "Uses replacement credential",
			Package:     &basePkg,
			EffectiveCredentials: []tooldef.PackageCredential{{
				Name: "override_token",
				Type: tooldef.CredentialTypeBearer,
				Inject: tooldef.CredentialInject{
					Hosts:  []string{"api.github.com"},
					Method: "bearer_header",
				},
			}},
		},
	}

	resolved, err := toolset.ResolveTools(tools, toolset.Config{})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}

	defaultPolicy, ok := resolved.ToolTransportPolicy("package.default")
	if !ok {
		t.Fatal("expected policy for inherited credential tool")
	}
	defaultRules := defaultPolicy.Rules()
	if len(defaultRules) != 1 || defaultRules[0].SecretKey != "github.com/example/multi-auth/package_token" {
		t.Fatalf("default rules = %+v, want inherited package_token rule", defaultRules)
	}

	nonePolicy, ok := resolved.ToolTransportPolicy("package.none")
	if ok && len(nonePolicy.Rules()) > 0 {
		t.Fatalf("explicit no-auth tool unexpectedly received rules: %+v", nonePolicy.Rules())
	}

	overridePolicy, ok := resolved.ToolTransportPolicy("package.override")
	if !ok {
		t.Fatal("expected policy for override tool")
	}
	overrideRules := overridePolicy.Rules()
	if len(overrideRules) != 1 || overrideRules[0].SecretKey != "github.com/example/multi-auth/override_token" {
		t.Fatalf("override rules = %+v, want replacement override_token rule", overrideRules)
	}
}

func TestResolveToolAuthKeepsPackageScopedKeysDistinctAcrossPackages(t *testing.T) {
	t.Parallel()

	sharedCredential := tooldef.PackageCredential{
		Name: "shared_token",
		Type: tooldef.CredentialTypeBearer,
		Inject: tooldef.CredentialInject{
			Hosts:  []string{"api.example.com"},
			Method: "bearer_header",
		},
	}

	tools := []tooldef.ResolvedTool{
		{
			Name:                 "pkg.one.tool",
			Description:          "Package one",
			Package:              &tooldef.Package{Module: "github.com/example/pkg-one", Name: "pkg-one", Runtime: tooldef.RuntimeTypeScriptSandbox},
			EffectiveCredentials: []tooldef.PackageCredential{sharedCredential},
		},
		{
			Name:                 "pkg.two.tool",
			Description:          "Package two",
			Package:              &tooldef.Package{Module: "github.com/example/pkg-two", Name: "pkg-two", Runtime: tooldef.RuntimeTypeScriptSandbox},
			EffectiveCredentials: []tooldef.PackageCredential{sharedCredential},
		},
	}

	resolved, err := toolset.ResolveTools(tools, toolset.Config{})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}

	firstPolicy, ok := resolved.ToolTransportPolicy("pkg.one.tool")
	if !ok {
		t.Fatal("expected policy for pkg.one.tool")
	}
	secondPolicy, ok := resolved.ToolTransportPolicy("pkg.two.tool")
	if !ok {
		t.Fatal("expected policy for pkg.two.tool")
	}
	firstKey := firstPolicy.Rules()[0].SecretKey
	secondKey := secondPolicy.Rules()[0].SecretKey
	if firstKey == secondKey {
		t.Fatalf("package-scoped keys collided: %q", firstKey)
	}
	if firstKey != "github.com/example/pkg-one/shared_token" {
		t.Fatalf("first key = %q, want package-one scoped key", firstKey)
	}
	if secondKey != "github.com/example/pkg-two/shared_token" {
		t.Fatalf("second key = %q, want package-two scoped key", secondKey)
	}
}

func TestResolveToolAuthNamespaceHelpersReserveTenantAndCredentialFamilies(t *testing.T) {
	t.Parallel()

	module := tooldef.ModulePath("github.com/example/github-issues")

	packageNamespace, err := tooldef.PackageSecretNamespace(module)
	if err != nil {
		t.Fatalf("PackageSecretNamespace: %v", err)
	}
	if packageNamespace != "github.com/example/github-issues" {
		t.Fatalf("package namespace = %q", packageNamespace)
	}

	tenantNamespace, err := tooldef.TenantSecretNamespace(module, "acme")
	if err != nil {
		t.Fatalf("TenantSecretNamespace: %v", err)
	}
	if tenantNamespace != "github.com/example/github-issues/tenant/acme" {
		t.Fatalf("tenant namespace = %q", tenantNamespace)
	}
	if !strings.HasPrefix(tenantNamespace, packageNamespace+"/") {
		t.Fatalf("tenant namespace = %q, want nested beneath package root %q", tenantNamespace, packageNamespace)
	}

	familyKey, err := tooldef.CredentialFamilySecretKey(module, "acme", "github_oauth", "access_token")
	if err != nil {
		t.Fatalf("CredentialFamilySecretKey: %v", err)
	}
	if familyKey != "github.com/example/github-issues/tenant/acme/github_oauth/access_token" {
		t.Fatalf("family key = %q", familyKey)
	}

	otherFamilyKey, err := tooldef.CredentialFamilySecretKey(module, "acme", "calendar_oauth", "access_token")
	if err != nil {
		t.Fatalf("CredentialFamilySecretKey second credential: %v", err)
	}
	if familyKey == otherFamilyKey {
		t.Fatalf("credential family keys collided: %q", familyKey)
	}
}

func TestResolveToolAuthNamespaceHelpersRejectMalformedParts(t *testing.T) {
	t.Parallel()

	module := tooldef.ModulePath("github.com/example/github-issues")

	if _, err := tooldef.PackageSecretNamespace(""); err == nil {
		t.Fatal("expected empty module to fail package namespace resolution")
	}
	if _, err := tooldef.TenantSecretNamespace(module, "tenant/one"); err == nil {
		t.Fatal("expected malformed tenant to fail")
	}
	if _, err := tooldef.CredentialSecretKey(module, " "); err == nil {
		t.Fatal("expected empty credential name to fail")
	}
	if _, err := tooldef.CredentialFamilySecretKey(module, "", "github_oauth", "refresh/token"); err == nil {
		t.Fatal("expected malformed family segment to fail")
	}
}

func TestResolveToolAuthFailsClosedWhenEffectiveCredentialsLackPackageMetadata(t *testing.T) {
	t.Parallel()

	_, err := toolset.ResolveTools([]tooldef.ResolvedTool{{
		Name:        "github.issues.get",
		Description: "Get a GitHub issue",
		EffectiveCredentials: []tooldef.PackageCredential{{
			Name: "github_token",
			Type: tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{"api.github.com"},
				Method: "bearer_header",
			},
		}},
	}}, toolset.Config{})
	if err == nil {
		t.Fatal("expected missing package metadata to fail resolution")
	}
	if !strings.Contains(err.Error(), "resolved credentials require package metadata") {
		t.Fatalf("ResolveTools error = %v, want package metadata context", err)
	}
}

func TestResolveToolAuthRejectsMalformedNamespaceDerivedSecretKeys(t *testing.T) {
	t.Parallel()

	_, err := toolset.ResolveTools([]tooldef.ResolvedTool{{
		Name:        "github.issues.get",
		Description: "Get a GitHub issue",
		Package: &tooldef.Package{
			Module:  "github.com/example/github-issues",
			Name:    "github-issues",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
		},
		EffectiveCredentials: []tooldef.PackageCredential{{
			Name: "bad/token",
			Type: tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{"api.github.com"},
				Method: "bearer_header",
			},
		}},
	}}, toolset.Config{})
	if err == nil {
		t.Fatal("expected malformed secret namespace part to fail resolution")
	}
	if !strings.Contains(err.Error(), "credential \"bad/token\"") {
		t.Fatalf("ResolveTools error = %v, want credential context", err)
	}
	if !strings.Contains(err.Error(), "must not contain '/'") {
		t.Fatalf("ResolveTools error = %v, want namespace validation context", err)
	}
}

func TestAgentViewDoesNotExposeTransportCredentialState(t *testing.T) {
	t.Parallel()

	resolved := authResolvedToolset(t)
	view := resolved.AgentView()
	tool := findAgentTool(t, view, "github.issues.get")

	props, ok := tool.ParamsSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in ParamsSchema")
	}
	if _, ok := props["owner"]; !ok {
		t.Fatal("expected owner param to remain visible")
	}
	if _, ok := props["repo"]; !ok {
		t.Fatal("expected repo param to remain visible")
	}
	if _, ok := props["number"]; !ok {
		t.Fatal("expected number param to remain visible")
	}
	if _, ok := props["github_token"]; ok {
		t.Fatal("transport credential should not appear in AgentView schema")
	}
}

func TestValidateCallDoesNotInjectCredentialShapedParams(t *testing.T) {
	t.Parallel()

	resolved := authResolvedToolset(t)
	params, err := resolved.ValidateCall("github.issues.get", map[string]any{
		"owner":  "octocat",
		"repo":   "hello-world",
		"number": 1,
	})
	if err != nil {
		t.Fatalf("ValidateCall: %v", err)
	}
	if len(params) != 3 {
		t.Fatalf("validated param count = %d, want 3", len(params))
	}
	if _, ok := params["github_token"]; ok {
		t.Fatal("transport credential should not be injected into validated params")
	}
	if _, ok := params["authorization"]; ok {
		t.Fatal("authorization header state should not be injected into validated params")
	}
}

func authResolvedToolset(t testing.TB) toolset.ResolvedToolset {
	t.Helper()

	pkg := tooldef.Package{
		Module:  "github.com/example/github-issues",
		Name:    "github-issues",
		Runtime: tooldef.RuntimeTypeScriptSandbox,
		Credentials: []tooldef.PackageCredential{{
			Name: "github_token",
			Type: tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{"api.github.com"},
				Method: "bearer_header",
			},
		}},
	}

	rt := tooldef.ResolvedTool{
		Name:                 "github.issues.get",
		Description:          "Get a GitHub issue",
		Package:              &pkg,
		EffectiveCredentials: pkg.Credentials,
	}
	rt.SetParamsSchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"owner":  map[string]any{"type": "string"},
			"repo":   map[string]any{"type": "string"},
			"number": map[string]any{"type": "integer"},
		},
		"required": []any{"owner", "repo", "number"},
	})

	resolved, err := toolset.ResolveTools([]tooldef.ResolvedTool{rt}, toolset.Config{})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}
	return resolved
}
