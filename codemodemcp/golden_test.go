package codemodemcp_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/solidarity-ai/toolbox/codemodemcp"
	"github.com/solidarity-ai/toolbox/codemodesession"
	"github.com/solidarity-ai/toolbox/testutil/mcptest"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestSuperToolDescriptionGoldens(t *testing.T) {
	t.Run("unlocked-no-approvals", func(t *testing.T) {
		h := mcptest.NewHarness(t, codemodemcp.New())
		checkGolden(t, goldenPath("super_tool_description_unlocked_no_approvals.txt"), toolDescription(t, h.ListTools().Tools, codemodemcp.ToolSuperTool))
	})

	t.Run("unlocked-with-approvals", func(t *testing.T) {
		dir := writeApprovalPackage(t)
		prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
			ToolApprovals: map[string]bool{
				"issues.get": true,
			},
		})
		h := mcptest.NewHarness(t, codemodemcp.New(codemodesession.SessionConfig{PreparedTools: prepared}))
		checkGolden(t, goldenPath("super_tool_description_unlocked_with_approvals.txt"), toolDescription(t, h.ListTools().Tools, codemodemcp.ToolSuperTool))
	})

	t.Run("locked-with-approvals", func(t *testing.T) {
		tempDir := t.TempDir()
		t.Setenv("TOOLBOX_SESSIONS_DIR", filepath.Join(tempDir, "sessions"))
		dir := writeApprovalPackage(t)
		prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
			ToolApprovals: map[string]bool{
				"issues.get": true,
			},
		})

		managed, err := codemodemcp.OpenManagedBoundNamed(context.Background(), "example", tempDir, mustCreateTBSession(t, tempDir), codemodesession.SessionConfig{PreparedTools: prepared})
		if err != nil {
			t.Fatalf("OpenManagedBoundNamed(): %v", err)
		}
		defer managed.Close()

		h := mcptest.NewHarness(t, managed.Server())
		checkGolden(t, goldenPath("super_tool_description_locked_with_approvals.txt"), toolDescription(t, h.ListTools().Tools, codemodemcp.ToolSuperTool))
	})
}

func toolDescription(t testing.TB, tools []mcp.Tool, name string) string {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool.Description
		}
	}
	t.Fatalf("tool %q not found", name)
	return ""
}

func goldenPath(name string) string {
	return filepath.Join(testfileDir(), "..", "testutil", "testdata", "goldens", "codemodemcp", name)
}

func checkGolden(t *testing.T, path string, got string) {
	t.Helper()
	if os.Getenv("GENERATE_GOLDENS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("wrote %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with GENERATE_GOLDENS=1 to create): %v", err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("golden mismatch (-want +got):\n%s", diff)
	}
}

func testfileDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	return filepath.Dir(file)
}
