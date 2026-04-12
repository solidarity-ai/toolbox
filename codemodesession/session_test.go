package codemodesession_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/codemodesession"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestOpenMemorySubmitReturnsSharedReplOutput(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: toolset.NewPreparedToolset([]assembler.LoadedTool{
			newPreparedTool("gmail.listThreads", "gmail"),
			newPreparedTool("gmail.sendDraft", "gmail"),
			newPreparedTool("hackerNews.frontPage", "hacker_news"),
		}),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `Object.entries($pkgMetadata).map(([packageName, meta]) => [packageName, meta.toolCount])`)
	assertContains(t, out, "cell 1")
	assertContains(t, out, "gmail")
	assertContains(t, out, "hacker_news")
	assertContains(t, out, "--")
}

func TestPackagesAPIContainsPackageDeclarationSource(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: toolset.NewPreparedToolset([]assembler.LoadedTool{
			newPreparedTool("gmail.listThreads", "gmail"),
		}),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `$pkgMetadata["gmail"]?.api.includes("declare namespace gmail")`)
	assertContains(t, out, "=> true")
}

func TestPackageMetadataExposesUseWhenHint(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: toolset.NewPreparedToolset([]assembler.LoadedTool{
			newPreparedToolWithUseWhenHint("hn.frontPage", "hacker_news", "Use when you need Hacker News posts and comments."),
		}),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `$pkgMetadata["hacker_news"]?.useWhenHint?.includes("Hacker News posts and comments.")`)
	assertContains(t, out, "=> true")
}

func TestSubmitCanCallPreparedToolPackages(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("calc"), toolset.Config{}),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `calc.calc.add(2, 3)`)
	assertContains(t, out, "cell 1")
	assertContains(t, out, "=> 5")
}

func TestSubmitRendersTypeScriptDiagnostics(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `const value: number = "x"`)
	assertContains(t, out, "cell (failed to commit)")
	assertContains(t, out, "failure: typecheck failed")
	assertContains(t, out, "diagnostics:")
	assertContains(t, out, "error:")
	assertNotContains(t, out, "submit error:")
}

func TestSubmitTranslatesWrappedObjectLiteralDiagnosticsBackToUserColumns(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `{ a: missingVar }`)
	assertContains(t, out, "cell (failed to commit)")
	assertContains(t, out, "1:6 error:")
	assertContains(t, out, "Cannot find name 'missingVar'.")
	assertNotContains(t, out, "1:7 error:")
}

func TestSubmitPrintsConsoleLogs(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `console.log("ok", { a: 1 }); 1`)
	assertContains(t, out, "cell 1")
	assertContains(t, out, "--\nok [object Object]\n=> 1\n")
}

func TestSubmitPrintsConsoleLogsOnFailure(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `console.log("bad", { a: 2 }); throw new Error("boom")`)
	assertContains(t, out, "cell (failed to commit)")
	assertContains(t, out, "failure:")
	assertContains(t, out, "--\nbad [object Object]\n")
	assertNotContains(t, out, "submit error:")
}

func TestOpenSQLiteResumesLatestSession(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "codemode.toolbox-session")

	first, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir)
	if err != nil {
		t.Fatalf("OpenSQLite(first) error: %v", err)
	}
	if first.Resumed() {
		t.Fatal("first session resumed = true, want false")
	}
	assertContains(t, first.Submit(ctx, "const value: number = 1"), "cell 1")
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}

	second, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir)
	if err != nil {
		t.Fatalf("OpenSQLite(second) error: %v", err)
	}
	defer second.Close()

	if !second.Resumed() {
		t.Fatal("second session resumed = false, want true")
	}
	out := second.Submit(ctx, "value + 2")
	assertContains(t, out, "=> 3")
}

func TestOpenSQLiteReopensToolCellsWithoutPreparedTools(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "codemode-tools.toolbox-session")

	first, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir, codemodesession.SessionConfig{
		PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("calc"), toolset.Config{}),
	})
	if err != nil {
		t.Fatalf("OpenSQLite(first) error: %v", err)
	}
	assertContains(t, first.Submit(ctx, `calc.calc.add(2, 3)`), `=> 5`)
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}

	second, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir)
	if err != nil {
		t.Fatalf("OpenSQLite(second) error: %v", err)
	}
	defer second.Close()
	if !second.Resumed() {
		t.Fatal("second session resumed = false, want true")
	}
	out := second.Submit(ctx, `const ok: number = 1; ok`)
	assertContains(t, out, "warn: TypeScript static context was reset because the TypeScript env changed.")
	assertContains(t, out, "=> 1")
}

func TestInstructionsDescribeSessionUsage(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	session.SetPreparedTools(toolset.NewPreparedToolset([]assembler.LoadedTool{
		newPreparedTool("gmail.listThreads", "gmail"),
		newPreparedTool("gmail.sendDraft", "gmail"),
		newPreparedToolWithUseWhenHint("hackerNews.frontPage", "hacker_news", "Use when you need Hacker News posts and comments."),
	}))
	instructions := session.Instructions()
	assertContains(t, instructions, "super_tool submits a code cell to a REPL")
	assertContains(t, instructions, "$pkgMetadata")
	assertContains(t, instructions, "declare const $pkgMetadata: Record<string, {")
	assertContains(t, instructions, "toolCount: number;")
	assertContains(t, instructions, "present when the package needs extra guidance")
	assertContains(t, instructions, "useWhenHint?: string;")
	assertContains(t, instructions, "/* .d.ts for package */")
	assertContains(t, instructions, "meta.toolCount")
	assertContains(t, instructions, "meta.useWhenHint")
	assertContains(t, instructions, "// REPL input")
	assertContains(t, instructions, "// REPL output")
	assertContains(t, instructions, `["gmail", 2, ""]`)
	assertContains(t, instructions, `["hacker_news", 1, "Use when you need Hacker News posts and comments."]`)
	assertContains(t, instructions, "last cell value")
	assertContains(t, instructions, "value for a prior cell index")
	assertContains(t, instructions, "$last : any")
	assertContains(t, instructions, "$val(index : number) : any")
	assertContains(t, instructions, "inspect(x : any) : string")
}

func newPreparedTool(name, packageName string) assembler.LoadedTool {
	return newPreparedToolWithUseWhenHint(name, packageName, "")
}

func newPreparedToolWithUseWhenHint(name, packageName, useWhenHint string) assembler.LoadedTool {
	return assembler.LoadedTool{
		Name: name,
		PackageMeta: &tooldef.Package{
			Name:        packageName,
			UseWhenHint: useWhenHint,
		},
	}
}

func assertContains(t testing.TB, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output missing %q\noutput:\n%s", want, got)
	}
}

func assertNotContains(t testing.TB, got, want string) {
	t.Helper()
	if strings.Contains(got, want) {
		t.Fatalf("output unexpectedly contained %q\noutput:\n%s", want, got)
	}
}
