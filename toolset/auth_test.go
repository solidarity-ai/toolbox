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

func TestResolveToolAuthRejectsAmbiguousCanonicalRules(t *testing.T) {
	t.Parallel()

	pkg := tooldef.Package{
		Module:  "github.com/example/github-issues",
		Name:    "github-issues",
		Runtime: tooldef.RuntimeTypeScriptSandbox,
		Credentials: []tooldef.PackageCredential{
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
		},
	}

	_, err := toolset.ResolveTools([]tooldef.ResolvedTool{{
		Name:        "github.issues.get",
		Description: "Get a GitHub issue",
		Package:     &pkg,
	}}, toolset.Config{})
	if err == nil {
		t.Fatal("expected ambiguous credential rules to fail resolution")
	}
	if !strings.Contains(err.Error(), "ambiguous transport credential rules") {
		t.Fatalf("ResolveTools error = %v, want ambiguity context", err)
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
		Name:        "github.issues.get",
		Description: "Get a GitHub issue",
		Package:     &pkg,
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
