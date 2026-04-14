package codemodesession_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/codemodesession"
	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/daemon"
	"github.com/solidarity-ai/toolbox/secrets"
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

func TestSubmitPackageMetadataWorksWithInjectedAccountParamAfterOptionalInput(t *testing.T) {
	ctx := context.Background()
	dir := writeSessionPackage(t, t.TempDir(), "example.com/mail", "mail", map[string]string{
		"tools/list.ts": `export default async function tool(input?: {
  query?: string;
}): Promise<{ ok: boolean }> {
  return { ok: true };
}
`,
	})

	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
			CredentialPolicySource: credentialrepo.StaticPolicySource{
				tooldef.ModulePath("example.com/mail"): {
					CredentialAccounts: map[string][]string{
						"workspace": {"a@example.com", "b@example.com"},
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `$pkgMetadata`)
	assertContains(t, out, "mail")
	assertNotContains(t, out, "failure: typecheck failed")
	assertNotContains(t, out, "A required parameter cannot follow an optional parameter")

	countOut := session.Submit(ctx, `$pkgMetadata["mail"]?.toolCount`)
	assertContains(t, countOut, "=> 1")
	assertNotContains(t, countOut, "failure: typecheck failed")
	assertNotContains(t, countOut, "A required parameter cannot follow an optional parameter")
}

func TestInstructionsIncludeUnlockNoteForLockedPackages(t *testing.T) {
	t.Setenv(daemon.BindAddressEnv, "localhost:7113")

	prepared, err := toolset.PrepareTools(context.Background(), []assembler.LoadedTool{
		newPreparedTool("locked.listThreads", "gmail"),
	}, toolset.Config{
		CredentialPolicySource: lockedPolicySource{},
	})
	if err != nil {
		t.Fatalf("PrepareTools() error: %v", err)
	}

	session, err := codemodesession.OpenMemory(context.Background(), t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: prepared,
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	instructions := session.Instructions()
	assertContains(t, instructions, "Some tools are unavailable because the toolbox secret store is locked: gmail.")
	assertContains(t, instructions, "Unlock Toolbox at http://localhost:7113/ and reload to restore them.")
}

func TestSubmitCanCallPreparedToolPackages(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `calc.calc.add(2, 3)`)
	assertContains(t, out, "cell 1")
	assertContains(t, out, "=> 5")
}

func TestSubmitDoesNotExposeRuntimeHashToken(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `(globalThis as any).__toolboxCurrentRuntimeHash === undefined && calc.calc.add(2, 3) === 5`)
	assertContains(t, out, "=> true")
}

func TestSetPreparedTools_AddsBindingsBeforeFirstSubmit(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	session.SetPreparedTools(prepareCalcToolset(t))
	out := session.Submit(ctx, `calc.calc.add(2, 3)`)
	assertContains(t, out, "=> 5")
}

func TestSetPreparedTools_RemovesBindingsAfterCommittedCells(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	assertContains(t, session.Submit(ctx, `calc.calc.add`), "cell 1")
	assertContains(t, session.Submit(ctx, `"alpha"`), "alpha")

	session.SetPreparedTools(toolset.PreparedToolset{})

	failed := session.Submit(ctx, `($val(1) as any)(2, 3)`)
	assertContains(t, failed, "failure:")
	assertContains(t, failed, "tool calc.add came from a previous runtime and is no longer callable")

	out := session.Submit(ctx, `$val(2) === "alpha" && ((globalThis as any).calc === undefined) && ($pkgMetadata["calc"] === undefined)`)
	assertContains(t, out, "=> true")

	gone := session.Submit(ctx, `($val(1) as any)(2, 3)`)
	assertContains(t, gone, "failure:")
	assertContains(t, gone, "tool calc.add came from a previous runtime and is no longer callable")
}

func TestSetPreparedTools_WrappersDoNotExposeInternalMetadata(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	assertContains(t, session.Submit(ctx, `calc.calc.add`), "cell 1")

	initial := session.Submit(ctx, `!Object.prototype.hasOwnProperty.call($val(1), "__replRuntimeHash") && !Object.prototype.hasOwnProperty.call($val(1), "__replStaleIndexedMessage")`)
	assertContains(t, initial, "=> true")

	session.SetPreparedTools(toolset.PreparedToolset{})

	stale := session.Submit(ctx, `!Object.prototype.hasOwnProperty.call($val(1), "__replStaleIndexedMessage")`)
	assertContains(t, stale, "=> true")
}

func TestSetPreparedTools_LastTransitionWinsAtSameHead(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	session.SetPreparedTools(prepareCalcToolset(t))
	session.SetPreparedTools(toolset.PreparedToolset{})
	session.SetPreparedTools(prepareCalcToolset(t))

	out := session.Submit(ctx, `calc.calc.add(2, 3)`)
	assertContains(t, out, "=> 5")
}

func TestSetPreparedTools_SavedWrapperRejectedAfterRuntimeChangeEvenWhenToolStillExists(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	assertContains(t, session.Submit(ctx, `calc.calc.add`), "cell 1")

	session.SetPreparedTools(prepareCalcAndEdgeCasesToolset(t))

	staleLast := session.Submit(ctx, `($last as any)(2, 3)`)
	assertContains(t, staleLast, "failure:")
	assertContains(t, staleLast, "tool calc.add came from a previous runtime and is no longer callable")

	stale := session.Submit(ctx, `($val(1) as any)(2, 3)`)
	assertContains(t, stale, "failure:")
	assertContains(t, stale, "tool calc.add came from a previous runtime and is no longer callable")

	fresh := session.Submit(ctx, `calc.calc.add(2, 3)`)
	assertContains(t, fresh, "=> 5")
}

func TestSetPreparedTools_RebuildsDirtyRuntimeBeforeTransition(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	assertContains(t, session.Submit(ctx, `calc.calc.add`), "cell 1")
	assertContains(t, session.Submit(ctx, `"alpha"`), "alpha")

	failed := session.Submit(ctx, `globalThis.leaked = 1; throw new Error("boom")`)
	assertContains(t, failed, "failure:")

	session.SetPreparedTools(toolset.PreparedToolset{})

	removed := session.Submit(ctx, `($val(1) as any)(2, 3)`)
	assertContains(t, removed, "failure:")
	assertContains(t, removed, "tool calc.add came from a previous runtime and is no longer callable")

	out := session.Submit(ctx, `((globalThis as any).leaked === undefined) && $val(2) === "alpha" && ((globalThis as any).calc === undefined) && ($pkgMetadata["calc"] === undefined)`)
	assertContains(t, out, "=> true")

	gone := session.Submit(ctx, `($val(1) as any)(2, 3)`)
	assertContains(t, gone, "failure:")
	assertContains(t, gone, "tool calc.add came from a previous runtime and is no longer callable")
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
		PreparedTools: prepareCalcToolset(t),
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

func TestOpenSQLiteResumesWithCurrentPreparedToolsBeforeFirstSubmit(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "codemode-live-tools.toolbox-session")

	first, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir)
	if err != nil {
		t.Fatalf("OpenSQLite(first) error: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}

	second, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir, codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenSQLite(second) error: %v", err)
	}
	defer second.Close()

	if !second.Resumed() {
		t.Fatal("second session resumed = false, want true")
	}
	out := second.Submit(ctx, `calc.calc.add(2, 3)`)
	assertContains(t, out, "=> 5")
}

func TestOpenSQLiteResumesCommittedValuesAndAddsCurrentPreparedTools(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "codemode-live-tools-with-history.toolbox-session")

	first, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir)
	if err != nil {
		t.Fatalf("OpenSQLite(first) error: %v", err)
	}
	assertContains(t, first.Submit(ctx, `"alpha"`), "alpha")
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}

	second, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir, codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenSQLite(second) error: %v", err)
	}
	defer second.Close()

	if !second.Resumed() {
		t.Fatal("second session resumed = false, want true")
	}
	out := second.Submit(ctx, `$val(1) === "alpha" && calc.calc.add(2, 3) === 5`)
	assertContains(t, out, "warn: TypeScript static context was reset because the TypeScript env changed.")
	assertContains(t, out, "=> true")
}

func TestOpenSQLiteResumesWithCurrentPreparedToolsAfterCommittedCells(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "codemode-remove-tools.toolbox-session")

	first, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir, codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenSQLite(first) error: %v", err)
	}
	assertContains(t, first.Submit(ctx, `calc.calc.add`), "cell 1")
	assertContains(t, first.Submit(ctx, `"alpha"`), "alpha")
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
	failed := second.Submit(ctx, `($val(1) as any)(2, 3)`)
	assertContains(t, failed, "failure:")
	assertContains(t, failed, "tool calc.add came from a previous runtime and is no longer callable")

	out := second.Submit(ctx, `$val(2) === "alpha" && ((globalThis as any).calc === undefined) && ($pkgMetadata["calc"] === undefined)`)
	assertContains(t, out, "=> true")

	gone := second.Submit(ctx, `($val(1) as any)(2, 3)`)
	assertContains(t, gone, "failure:")
	assertContains(t, gone, "tool calc.add came from a previous runtime and is no longer callable")
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
	assertContains(t, instructions, "super_tool submits a code cell to a notebook like environment")
	assertContains(t, instructions, "super_tool is much more efficient and effective than regular tool calling")
	assertContains(t, instructions, "$pkgMetadata")
	assertContains(t, instructions, "declare const $pkgMetadata: Record<string, {")
	assertContains(t, instructions, "toolCount: number;")
	assertContains(t, instructions, "present when the package needs extra guidance")
	assertContains(t, instructions, "useWhenHint?: string;")
	assertContains(t, instructions, "/* .d.ts for package, always use console.log to view */")
	assertContains(t, instructions, "meta.toolCount")
	assertContains(t, instructions, "meta.useWhenHint")
	assertContains(t, instructions, "// Notebook Input")
	assertContains(t, instructions, "// Notebook Output")
	assertContains(t, instructions, `["gmail", 2, ""]`)
	assertContains(t, instructions, `["hacker_news", 1, "Use when you need Hacker News posts and comments."]`)
	assertContains(t, instructions, "last value in an expression in prior cell")
	assertContains(t, instructions, "$val(<last-cell>)")
	assertContains(t, instructions, "$last : any")
	assertContains(t, instructions, "$val(index : number) : any")
	assertContains(t, instructions, "inspect(x : any) : string")
}

func newPreparedTool(name, packageName string) assembler.LoadedTool {
	return newPreparedToolWithUseWhenHint(name, packageName, "")
}

func prepareCalcToolset(t testing.TB) toolset.PreparedToolset {
	t.Helper()
	return tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("calc"), toolset.Config{})
}

func prepareCalcAndEdgeCasesToolset(t testing.TB) toolset.PreparedToolset {
	t.Helper()
	return tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("calc", "edge-cases"), toolset.Config{})
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

func writeSessionPackage(t testing.TB, dir, module, name string, files map[string]string) string {
	t.Helper()

	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		fullPath := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(fullPath), err)
		}
		if err := os.WriteFile(fullPath, []byte(files[path]), 0o644); err != nil {
			t.Fatalf("write %s: %v", fullPath, err)
		}
	}

	entries := make([]string, 0, len(paths))
	for _, path := range paths {
		entries = append(entries, fmt.Sprintf(`{ "entry_ts": %q }`, path))
	}
	manifest := fmt.Sprintf("{\n  \"module\": %q,\n  \"name\": %q,\n  \"runtime\": \"typescript-sandbox\",\n  \"tools\": [\n    %s\n  ]\n}\n", module, name, strings.Join(entries, ",\n    "))
	if err := os.WriteFile(filepath.Join(dir, "toolbox.devpkg.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

type lockedPolicySource struct{}

func (lockedPolicySource) PackageCredentialPolicy(context.Context, tooldef.Package) (toolset.PackageCredentialPolicy, error) {
	return toolset.PackageCredentialPolicy{}, secrets.ErrLocked
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
