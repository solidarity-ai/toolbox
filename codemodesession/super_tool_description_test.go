package codemodesession_test

import (
	"context"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/codemodesession"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestUnlockedSuperToolDescriptionIsCompactPackageIndex(t *testing.T) {
	session, err := codemodesession.OpenMemory(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	session.SetPreparedTools(toolset.NewPreparedToolset([]assembler.LoadedTool{{
		Name: "hackerNews.frontPage",
		PackageMeta: &tooldef.Package{
			Name:        "hacker-news",
			UseWhenHint: "Use when you need Hacker News posts and comments.",
		},
	}}))

	description := session.SuperToolDescriptionForSurface(codemodesession.ToolSurfaceModeUnlocked, false)
	for _, want := range []string{
		"Call new_super_tool_session first",
		"- hacker-news: Use when you need Hacker News posts and comments.",
	} {
		if !strings.Contains(description, want) {
			t.Fatalf("description = %q, want %q", description, want)
		}
	}
	if strings.Contains(description, "Important Notebook Usage Information:") {
		t.Fatalf("description = %q, did not want full notebook instructions", description)
	}
}

func TestLockedSuperToolDescriptionRetainsFullInstructions(t *testing.T) {
	session, err := codemodesession.OpenMemory(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	description := session.SuperToolDescriptionForSurface(codemodesession.ToolSurfaceModeLocked, false)
	if !strings.Contains(description, "Important Notebook Usage Information:") {
		t.Fatalf("description = %q, want full notebook instructions", description)
	}
}
