package codemodesdks_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/microsoft/typescript-go/toolbox"
	"github.com/solidarity-ai/toolbox/codemodesdks"
	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestDeclarationSource_UsesPackageNamespaces(t *testing.T) {
	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("calc"), toolset.Config{})

	got := codemodesdks.DeclarationSource(prepared)
	if !strings.Contains(got, "declare namespace calc {") {
		t.Fatalf("missing calc namespace:\n%s", got)
	}
	if !strings.Contains(got, "function add(") {
		t.Fatalf("missing add declaration:\n%s", got)
	}
	if !strings.Contains(got, "function asyncAdd(") {
		t.Fatalf("missing asyncAdd declaration:\n%s", got)
	}
	if strings.Contains(got, "export declare const tools") {
		t.Fatalf("unexpected tools object declaration:\n%s", got)
	}
}

func TestDeclarationSource_LeavesToolCallHelpersToCodemodePrelude(t *testing.T) {
	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("calc"), toolset.Config{})

	got := codemodesdks.DeclarationSource(prepared)
	if strings.Contains(got, "type ToolCallTask<T = unknown>") {
		t.Fatalf("DeclarationSource should not declare top-level ToolCallTask:\n%s", got)
	}
	if strings.Contains(got, "type ToolCallPromise<T>") {
		t.Fatalf("DeclarationSource should not declare top-level ToolCallPromise:\n%s", got)
	}
	if strings.Contains(got, "type ToolCallView<T = unknown>") {
		t.Fatalf("DeclarationSource should not declare top-level ToolCallView:\n%s", got)
	}
	if strings.Contains(got, "declare function $tool_call") {
		t.Fatalf("DeclarationSource should not declare top-level $tool_call:\n%s", got)
	}
}

func TestDeclarationSource_UsesToolCallPromiseAndTaskReturnTypes(t *testing.T) {
	dir := writePackage(t, t.TempDir(), "example.com/issues", "issues", map[string]string{
		"tools/get.ts": `interface Issue {
  id: string;
  title: string;
}

export default async function tool(id: string): Promise<Issue> {
  return { id, title: "Example" };
}
`,
	})

	promisePrepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{})
	promiseDecls := codemodesdks.DeclarationSource(promisePrepared)
	if !strings.Contains(promiseDecls, `function get(id: string): ToolCallPromise<{ id: string; title: string }>;`) {
		t.Fatalf("expected ToolCallPromise return type:\n%s", promiseDecls)
	}

	taskPrepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
		ToolApprovals: map[string]bool{
			"issues.get": true,
		},
	})
	taskDecls := codemodesdks.DeclarationSource(taskPrepared)
	if !strings.Contains(taskDecls, `function get(id: string): ToolCallTask<{ id: string; title: string }>;`) {
		t.Fatalf("expected ToolCallTask return type:\n%s", taskDecls)
	}
}

func TestDeclarationSource_UsesNestedPackageNamespaces(t *testing.T) {
	dir := writePackage(t, t.TempDir(), "example.com/gws-gmail", "gws.gmail", map[string]string{
		"tools/reply.ts": `/**
 * Reply to a thread.
 */
export default function tool(args: { body: string }): { ok: boolean } {
  return { ok: args.body.length > 0 };
}
`,
	})

	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{})
	got := codemodesdks.DeclarationSource(prepared)
	if !strings.Contains(got, "declare namespace gws {\n  namespace gmail {") {
		t.Fatalf("missing nested package namespace:\n%s", got)
	}
	if !strings.Contains(got, "function reply(") {
		t.Fatalf("missing reply declaration:\n%s", got)
	}
}

func TestDeclarationSource_DoesNotDeduplicatePackagePrefixFromToolName(t *testing.T) {
	dir := writePackage(t, t.TempDir(), "example.com/gmail", "gmail", map[string]string{
		"tools/gmail.reply.ts": `/**
 * Reply to a thread.
 */
export default function tool(args: { body: string }): { ok: boolean } {
  return { ok: args.body.length > 0 };
}
`,
	})

	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{})
	got := codemodesdks.DeclarationSource(prepared)
	if strings.Count(got, "namespace gmail {") != 2 {
		t.Fatalf("expected duplicated gmail namespace in package and tool path:\n%s", got)
	}
	if !strings.Contains(got, "function reply(") {
		t.Fatalf("expected package prefix to remain in tool path:\n%s", got)
	}
}

func TestDeclarationSource_ConcatsWithoutTypeCollisions(t *testing.T) {
	dirA := writePackage(t, t.TempDir(), "example.com/pkg-a", "pkgA", map[string]string{
		"tools/tickets.get.ts": `type Ticket = {
  /** Ticket ID */
  id: string;
  /** Title */
  title: string;
};

/**
 * Get ticket A.
 */
export default function tool(args: { id: string }): Ticket {
  return { id: args.id, title: "A" };
}
`,
	})
	dirB := writePackage(t, t.TempDir(), "example.com/pkg-b", "pkgB", map[string]string{
		"tools/tickets.get.ts": `type Ticket = {
  /** Ticket ID */
  id: string;
  /** Title */
  title: string;
};

/**
 * Get ticket B.
 */
export default function tool(args: { id: string }): Ticket {
  return { id: args.id, title: "B" };
}
`,
	})

	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dirA, dirB), toolset.Config{})
	got := codemodesdks.DeclarationSource(prepared)
	if !strings.Contains(got, "declare namespace pkgA {") || !strings.Contains(got, "declare namespace pkgB {") {
		t.Fatalf("missing package namespaces:\n%s", got)
	}
	typecheckDeclarations(t, got, `const a = pkgA.tickets.get({ id: "1" });
const b = pkgB.tickets.get({ id: "2" });
void a;
void b;
export {};
`)
}

func TestDeclarationSource_KeepsSharedTypesWithinPackageNamespace(t *testing.T) {
	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("shared-types"), toolset.Config{})

	got := codemodesdks.DeclarationSource(prepared)
	if !strings.Contains(got, "declare namespace sharedTypes {") {
		t.Fatalf("missing sharedTypes namespace:\n%s", got)
	}
	if !strings.Contains(got, "namespace tickets {") {
		t.Fatalf("missing tickets namespace:\n%s", got)
	}
	if strings.Count(got, "interface Ticket") > 1 || strings.Count(got, "type Ticket =") > 1 {
		t.Fatalf("expected one shared Ticket declaration:\n%s", got)
	}
}

func TestDeclarationSource_SingleUseNamedAsyncReturnInlinesShape(t *testing.T) {
	dir := writePackage(t, t.TempDir(), "example.com/issues", "issues", map[string]string{
		"tools/get.ts": `interface Issue {
  id: string;
  title: string;
}

export default async function tool(id: string): Promise<Issue> {
  return { id, title: "Example" };
}
`,
	})

	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{})
	got := codemodesdks.DeclarationSource(prepared)

	if !strings.Contains(got, `function get(id: string): ToolCallPromise<{ id: string; title: string }>;`) {
		t.Fatalf("expected single-use named async return to inline:\n%s", got)
	}
	if strings.Contains(got, `function get(id: string): ToolCallPromise<Issue>;`) {
		t.Fatalf("unexpected top-level ref for single-use named async return:\n%s", got)
	}
}

func TestDeclarationSource_SharedNamedAsyncReturnKeepsSharedName(t *testing.T) {
	dir := writePackage(t, t.TempDir(), "example.com/tickets", "tickets", map[string]string{
		"tools/get.ts": `interface Ticket {
  id: string;
}

export default async function tool(id: string): Promise<Ticket> {
  return { id };
}
`,
		"tools/create.ts": `interface Ticket {
  id: string;
}

export default async function tool(): Promise<Ticket> {
  return { id: "T-1" };
}
`,
	})

	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{})
	got := codemodesdks.DeclarationSource(prepared)

	if !strings.Contains(got, `function create(): ToolCallPromise<Ticket>;`) || !strings.Contains(got, `function get(id: string): ToolCallPromise<Ticket>;`) {
		t.Fatalf("expected shared async return to use shared Ticket name:\n%s", got)
	}
	if strings.Contains(got, "CreateResult") || strings.Contains(got, "GetResult") {
		t.Fatalf("unexpected synthetic shared name for shared async return:\n%s", got)
	}
	if !strings.Contains(got, "interface Ticket {") && !strings.Contains(got, "type Ticket = {") {
		t.Fatalf("expected shared Ticket declaration:\n%s", got)
	}
}

func TestDeclarationSource_AsyncInlineReturnKeepsNamedDefinitions(t *testing.T) {
	dir := writePackage(t, t.TempDir(), "example.com/google-workspace", "googleWorkspace", map[string]string{
		"tools/calendar.list.ts": `interface EventSummary {
  eventId: string;
  title: string | null;
}

export default async function tool(): Promise<{
  nextPageToken: string | null;
  events: EventSummary[];
}> {
  return {
    nextPageToken: null,
    events: [{ eventId: "evt_1", title: "Staff meeting" }],
  };
}
`,
		"tools/gmail.read.ts": `interface Attachment {
  filename: string;
  mimeType: string;
}

export default async function tool(): Promise<{
  attachments: Attachment[];
}> {
  return {
    attachments: [{ filename: "brief.pdf", mimeType: "application/pdf" }],
  };
}
`,
	})

	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{})
	got := codemodesdks.DeclarationSource(prepared)

	if !strings.Contains(got, "type EventSummary =") && !strings.Contains(got, "interface EventSummary {") {
		t.Fatalf("expected EventSummary declaration to be retained:\n%s", got)
	}
	if !strings.Contains(got, "type Attachment =") && !strings.Contains(got, "interface Attachment {") {
		t.Fatalf("expected Attachment declaration to be retained:\n%s", got)
	}

	typecheckDeclarations(t, got, `const title = (await googleWorkspace.calendar.list()).events[0].title;
const mimeType = (await googleWorkspace.gmail.read()).attachments[0].mimeType;
void title;
void mimeType;
export {};
`)
}

func TestDeclarationSource_OptionalParamBeforeInjectedAccountParamTypechecks(t *testing.T) {
	dir := writePackage(t, t.TempDir(), "example.com/mail", "mail", map[string]string{
		"tools/list.ts": `export default async function tool(input?: {
  query?: string;
}): Promise<{ ok: boolean }> {
  return { ok: true };
}
`,
	})

	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
		CredentialPolicySource: credentialrepo.StaticPolicySource{
			tooldef.ModulePath("example.com/mail"): {
				CredentialAccounts: map[string][]string{
					"workspace": {"a@example.com", "b@example.com"},
				},
			},
		},
	})
	got := codemodesdks.DeclarationSource(prepared)

	if !strings.Contains(got, `function list(input: { query?: string } | undefined,`) {
		t.Fatalf("expected optional input to render as an explicit undefined union:\n%s", got)
	}
	if !strings.Contains(got, `workspace_account: "a@example.com" | "b@example.com"`) {
		t.Fatalf("expected injected account union in rendered signature:\n%s", got)
	}

	typecheckDeclarations(t, got, `const ok = (await mail.list(undefined, "a@example.com")).ok;
const ok2 = (await mail.list({ query: "hello" }, "b@example.com")).ok;
void ok;
void ok2;
export {};
`)
}

func TestDeclarationSource_PreservesNativeMapAndSetTypes(t *testing.T) {
	dir := writePackage(t, t.TempDir(), "example.com/native-values", "nativeValues", map[string]string{
		"tools/use-native.ts": `type NativeInput = {
  map: Map<string, number>;
  set: Set<string>;
};

type NativeOutput = {
  returnedMap: Map<string, number>;
  returnedSet: Set<string>;
};

export default async function tool(input: NativeInput): Promise<NativeOutput> {
  return {
    returnedMap: new Map([["answer", input.map.get("answer") ?? 0]]),
    returnedSet: new Set(input.set),
  };
}
`,
	})

	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{})
	got := codemodesdks.DeclarationSource(prepared)
	if !strings.Contains(got, "returnedMap: Map<string, number>") {
		t.Fatalf("expected returnedMap to preserve Map type:\n%s", got)
	}
	if !strings.Contains(got, "returnedSet: Set<string>") {
		t.Fatalf("expected returnedSet to preserve Set type:\n%s", got)
	}

	typecheckDeclarations(t, got, `const got = await nativeValues.useNative({
  map: new Map<string, number>([["answer", 42]]),
  set: new Set<string>(["ok"]),
});
const answer = got.returnedMap.get("answer");
const hasOK = got.returnedSet.has("ok");
void answer;
void hasOK;
export {};
`)
}

func writePackage(t testing.TB, dir, module, name string, files map[string]string) string {
	t.Helper()

	entries := make([]string, 0, len(files))
	for path, contents := range files {
		fullPath := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(fullPath), err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", fullPath, err)
		}
		entries = append(entries, fmt.Sprintf(`{ "entry_ts": %q }`, path))
	}
	manifest := fmt.Sprintf("{\n  \"module\": %q,\n  \"name\": %q,\n  \"runtime\": \"typescript-sandbox\",\n  \"tools\": [\n    %s\n  ]\n}\n", module, name, strings.Join(entries, ",\n    "))
	if err := os.WriteFile(filepath.Join(dir, packaging.DevManifestFilename), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

func typecheckDeclarations(t testing.TB, declarations, entry string) {
	t.Helper()

	diagnostics, _, err := toolbox.Check(context.Background(), toolbox.CheckInput{
		Files: fstest.MapFS{
			"__env.d.ts": &fstest.MapFile{Data: []byte(declarations)},
			"__entry.ts": &fstest.MapFile{Data: []byte("/// <reference path=\"./__env.d.ts\" />\n" + entry)},
		},
		Entry:            "__entry.ts",
		CurrentDirectory: "/",
	}, nil)
	if err != nil {
		t.Fatalf("toolbox.Check: %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v\n\ndeclarations:\n%s", diagnostics, declarations)
	}
}
