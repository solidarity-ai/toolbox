package toolset_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestResolveToolAllowedHosts(t *testing.T) {
	t.Parallel()

	pkg := tooldef.Package{
		Name:         "google-workspace",
		Runtime:      tooldef.RuntimeTypeScriptSandbox,
		AllowedHosts: []string{" *.googleapis.com ", "oauth2.googleapis.com", "oauth2.googleapis.com"},
	}

	tools := []tooldef.ResolvedTool{
		{
			Name:         "users.list",
			Description:  "List users",
			Package:      &pkg,
			AllowedHosts: []string{" *.googleapis.com ", "oauth2.googleapis.com", "oauth2.googleapis.com"},
		},
		{
			Name:         "admin.list",
			Description:  "List admin users",
			Package:      &pkg,
			AllowedHosts: []string{"Admin.GoogleAPIs.com", "admin.googleapis.com"},
		},
		{
			Name:         "status.get",
			Description:  "Get status",
			Package:      &pkg,
			AllowedHosts: nil,
		},
	}

	resolved, err := toolset.ResolveTools(tools, toolset.Config{})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}

	t.Run("inherits normalized package policy", func(t *testing.T) {
		got, ok := resolved.ToolAllowedHosts("users.list")
		if !ok {
			t.Fatal("expected allowed hosts for users.list")
		}
		want := []string{"*.googleapis.com", "oauth2.googleapis.com"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("ToolAllowedHosts(users.list) mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("replace policy remains deterministic", func(t *testing.T) {
		got, ok := resolved.ToolAllowedHosts("admin.list")
		if !ok {
			t.Fatal("expected allowed hosts for admin.list")
		}
		want := []string{"admin.googleapis.com"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("ToolAllowedHosts(admin.list) mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("deny by default leaves no runtime policy", func(t *testing.T) {
		got, ok := resolved.ToolAllowedHosts("status.get")
		if ok {
			t.Fatalf("expected no allowlist for status.get, got %v", got)
		}
	})
}

func TestAllowlistRuntimeStateStaysOutOfAgentView(t *testing.T) {
	t.Parallel()

	resolved, err := toolset.ResolveTools([]tooldef.ResolvedTool{{
		Name:        "hooks.send",
		Description: "Send webhook",
		AllowedHosts: []string{
			"hooks.slack.com",
		},
	}}, toolset.Config{})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}

	view := resolved.AgentView()
	if len(view.Tools) != 1 {
		t.Fatalf("AgentView tool count = %d, want 1", len(view.Tools))
	}
	if _, ok := resolved.ToolAllowedHosts("hooks.send"); !ok {
		t.Fatal("expected runtime allowlist for hooks.send")
	}
	if view.Tools[0].ParamsSchema != nil {
		if _, ok := view.Tools[0].ParamsSchema["allowed_hosts"]; ok {
			t.Fatal("allowed host runtime policy should not appear in agent-visible params schema")
		}
	}
}
