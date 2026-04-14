package sdkbridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/codemodesession"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry"
	"github.com/solidarity-ai/toolbox/testutil/fixtures"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetctl"
	"github.com/solidarity-ai/toolbox/toolsetfile"
)

func TestBridgeServeStdioProcessesMultipleRequests(t *testing.T) {
	bridge := New(Options{Version: "v1.2.3"})

	input := strings.Join([]string{
		string(mustJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      "req-1",
			"method":  "system.version",
		})),
		string(mustJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      "req-2",
			"method":  "system.ping",
		})),
		"",
	}, "\n")

	var stdout bytes.Buffer
	if err := bridge.ServeStdio(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("ServeStdio(): %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("response lines = %d, want 2\n%s", len(lines), stdout.String())
	}

	var ids []string
	for _, line := range lines {
		var resp struct {
			ID     string         `json:"id"`
			Result map[string]any `json:"result"`
			Error  map[string]any `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("json.Unmarshal(%q): %v", line, err)
		}
		if resp.Error != nil {
			t.Fatalf("unexpected error response: %v", resp.Error)
		}
		ids = append(ids, resp.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"req-1", "req-2"}) {
		t.Fatalf("response ids = %#v, want %#v", ids, []string{"req-1", "req-2"})
	}
}

func TestBridgeServeStdioPreservesLifecycleRequestOrder(t *testing.T) {
	path := writeLocalToolsetFile(t, "calc")
	bridge := New(Options{})

	composedAny, err := bridge.handleMethod(context.Background(), "toolset.compose", mustJSON(t, ComposeParams{
		Mode:        ComposeModeCodemode,
		ToolsetFile: path,
	}))
	if err != nil {
		t.Fatalf("toolset.compose: %v", err)
	}
	composed := composedAny.(ComposeResult)

	input := strings.Join([]string{
		string(mustJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      "req-invoke",
			"method":  "tool.invoke",
			"params": map[string]any{
				"toolset_id": composed.ToolsetID,
				"tool_name":  CodeModeToolName,
				"params": map[string]any{
					codemodesession.TypeScriptCellSourceParam: "calc.calc.add(2, 3)",
				},
			},
		})),
		string(mustJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      "req-close",
			"method":  "toolset.close",
			"params": map[string]any{
				"toolset_id": composed.ToolsetID,
			},
		})),
		"",
	}, "\n")

	var stdout bytes.Buffer
	if err := bridge.ServeStdio(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("ServeStdio(): %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("response lines = %d, want 2\n%s", len(lines), stdout.String())
	}

	var invokeResp struct {
		ID     string `json:"id"`
		Result struct {
			Content string `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &invokeResp); err != nil {
		t.Fatalf("json.Unmarshal(invoke): %v", err)
	}
	if invokeResp.ID != "req-invoke" {
		t.Fatalf("first response id = %q, want %q", invokeResp.ID, "req-invoke")
	}
	if invokeResp.Error != nil {
		t.Fatalf("invoke error = %#v, want nil", invokeResp.Error)
	}
	if !strings.Contains(invokeResp.Result.Content, "=> 5") {
		t.Fatalf("invoke content = %q, want completion preview 5", invokeResp.Result.Content)
	}

	var closeResp struct {
		ID    string          `json:"id"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &closeResp); err != nil {
		t.Fatalf("json.Unmarshal(close): %v", err)
	}
	if closeResp.ID != "req-close" {
		t.Fatalf("second response id = %q, want %q", closeResp.ID, "req-close")
	}
	if len(closeResp.Error) != 0 && string(closeResp.Error) != "null" {
		t.Fatalf("close error = %s, want none", closeResp.Error)
	}
}

func TestBridgeToolsetFileLoadAndWrite(t *testing.T) {
	bridge := New(Options{})
	path := filepath.Join(t.TempDir(), "toolbox.toolset.json")
	if err := os.WriteFile(path, []byte(`{"packages":{"example.com/z":"v1.0.0","example.com/a":"v1.0.0"},"tools":[{"tool":"example.com/z@v1.0.0/z.run"}]}`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}

	result, err := bridge.handleMethod(context.Background(), "toolsetfile.load", mustJSON(t, ToolsetFileLoadParams{
		Path: path,
	}))
	if err != nil {
		t.Fatalf("toolsetfile.load: %v", err)
	}

	doc, ok := result.(*toolsetfile.ToolsetFile)
	if !ok {
		t.Fatalf("load result type = %T, want *toolsetfile.ToolsetFile", result)
	}

	if _, err := bridge.handleMethod(context.Background(), "toolsetfile.write", mustJSON(t, ToolsetFileWriteParams{
		Path:    path,
		Toolset: mustJSON(t, doc),
	})); err != nil {
		t.Fatalf("toolsetfile.write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	want := "{\n  \"packages\": {\n    \"example.com/a\": \"v1.0.0\",\n    \"example.com/z\": \"v1.0.0\"\n  },\n  \"tools\": [\n    {\"tool\":\"example.com/z@v1.0.0/z.run\"}\n  ]\n}\n"
	if string(got) != want {
		t.Fatalf("written toolset = %q, want %q", string(got), want)
	}
}

func TestBridgeComposeDirectFromFileAndInvoke(t *testing.T) {
	path := writeLocalToolsetFile(t, "calc")
	bridge := New(Options{})

	result, err := bridge.handleMethod(context.Background(), "toolset.compose", mustJSON(t, ComposeParams{
		Mode:        ComposeModeDirect,
		ToolsetFile: path,
	}))
	if err != nil {
		t.Fatalf("toolset.compose: %v", err)
	}

	composed := result.(ComposeResult)
	if composed.ToolsetID == "" {
		t.Fatal("toolset_id is empty")
	}
	if len(composed.Tools) == 0 {
		t.Fatal("compose returned no tools")
	}

	var addTool *ToolDescriptor
	for i := range composed.Tools {
		if composed.Tools[i].Name == "calc.add" {
			addTool = &composed.Tools[i]
			break
		}
	}
	if addTool == nil {
		t.Fatalf("compose tools = %#v, want calc.add", composed.Tools)
	}
	props, _ := addTool.ParamsSchema["properties"].(map[string]any)
	if _, ok := props["a"]; !ok {
		t.Fatalf("calc.add params schema = %#v, want property a", addTool.ParamsSchema)
	}

	invoked, err := bridge.handleMethod(context.Background(), "tool.invoke", mustJSON(t, ToolInvokeParams{
		ToolsetID: composed.ToolsetID,
		ToolName:  "calc.add",
		Params: map[string]any{
			"a": 1,
			"b": 2,
		},
	}))
	if err != nil {
		t.Fatalf("tool.invoke: %v", err)
	}
	if got := invoked.(ToolInvokeResult).Content; got != "3" {
		t.Fatalf("invoke result = %q, want %q", got, "3")
	}
}

func TestBridgePublishesPreparedTools(t *testing.T) {
	path := writeLocalToolsetFile(t, "calc")

	var snapshots [][]string
	bridge := New(Options{
		PreparedToolsConsumer: preparedToolConsumerFunc(func(prepared toolset.PreparedToolset) {
			snapshots = append(snapshots, toolNames(prepared))
		}),
	})

	result, err := bridge.handleMethod(context.Background(), "toolset.compose", mustJSON(t, ComposeParams{
		Mode:        ComposeModeDirect,
		ToolsetFile: path,
	}))
	if err != nil {
		t.Fatalf("toolset.compose: %v", err)
	}
	composed := result.(ComposeResult)

	if len(snapshots) == 0 {
		t.Fatal("observer snapshots = 0, want at least one publish")
	}
	wantRef := fixtureModule(t, "calc") + "@v1.2.3/calc.add"
	if got := snapshots[len(snapshots)-1]; !slices.Contains(got, wantRef) {
		t.Fatalf("latest observer snapshot = %#v, want %q", got, wantRef)
	}

	if _, err := bridge.handleMethod(context.Background(), "toolset.close", mustJSON(t, ToolsetCloseParams{
		ToolsetID: composed.ToolsetID,
	})); err != nil {
		t.Fatalf("toolset.close: %v", err)
	}
	if got := snapshots[len(snapshots)-1]; len(got) != 0 {
		t.Fatalf("latest observer snapshot after close = %#v, want empty", got)
	}
}

func TestBridgeReloadFileBackedToolsetsReloadsReloadableBackends(t *testing.T) {
	bridge := New(Options{})
	reloadable := &reloadableBackend{
		ToolsetBackend: toolsetctl.NewPreparedBackend(toolset.PreparedToolset{}, nil),
	}
	bridge.toolsets["ts_1"] = &composedToolset{backend: reloadable}
	bridge.toolsets["ts_2"] = &composedToolset{backend: toolsetctl.NewPreparedBackend(toolset.PreparedToolset{}, nil)}

	if err := bridge.ReloadFileBackedToolsets(context.Background()); err != nil {
		t.Fatalf("ReloadFileBackedToolsets() error: %v", err)
	}
	if reloadable.reloads != 1 {
		t.Fatalf("reloads = %d, want 1", reloadable.reloads)
	}
}

func TestBridgeReloadFileBackedToolsetsReturnsReloadErrors(t *testing.T) {
	bridge := New(Options{})
	reloadable := &reloadableBackend{
		ToolsetBackend: toolsetctl.NewPreparedBackend(toolset.PreparedToolset{}, nil),
		err:            errors.New("boom"),
	}
	bridge.toolsets["ts_1"] = &composedToolset{backend: reloadable}

	err := bridge.ReloadFileBackedToolsets(context.Background())
	if err == nil {
		t.Fatal("ReloadFileBackedToolsets() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("ReloadFileBackedToolsets() error = %v, want boom", err)
	}
}

type preparedToolConsumerFunc func(toolset.PreparedToolset)

func (f preparedToolConsumerFunc) SetPreparedTools(prepared toolset.PreparedToolset) {
	f(prepared)
}

type reloadableBackend struct {
	toolsetctl.ToolsetBackend
	reloads int
	err     error
}

func (b *reloadableBackend) Reload(context.Context) (toolset.PreparedToolset, error) {
	b.reloads++
	return toolset.PreparedToolset{}, b.err
}

func toolNames(prepared toolset.PreparedToolset) []string {
	tools := prepared.Tools()
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, tool.Name)
	}
	return out
}

func TestDescribeToolsDirectSkipsNonJSONCallableTools(t *testing.T) {
	prepared := toolset.NewPreparedToolset([]assembler.LoadedTool{
		{
			Name: "json.ok",
			Sig: tooltest.NewTSSig(t, `
export default function tool(input: { subject: string; labels?: string[] }) {
  return input.subject.length;
}
`),
			TS: inlineSDKBridgeToolDef(`
export default function tool(input: { subject: string; labels?: string[] }) {
  return input.subject.length;
}
`),
		},
		{
			Name: "json.nope",
			Sig: tooltest.NewTSSig(t, `
export default function tool(when: Date) {
  return when.toISOString();
}
`),
			TS: inlineSDKBridgeToolDef(`
export default function tool(when: Date) {
  return when.toISOString();
}
`),
		},
	})

	tools := describeTools(ComposeModeDirect, prepared, nil)
	if len(tools) != 1 {
		t.Fatalf("describeTools() returned %d tools, want 1: %#v", len(tools), tools)
	}
	if got := tools[0].Name; got != "json.ok" {
		t.Fatalf("describeTools()[0].Name = %q, want %q", got, "json.ok")
	}
}

func TestBridgeInvokeDirectRejectsNonJSONCallableTool(t *testing.T) {
	prepared := toolset.NewPreparedToolset([]assembler.LoadedTool{
		{
			Name: "json.ok",
			Sig: tooltest.NewTSSig(t, `
export default function tool(input: { subject: string }) {
  return input.subject.length;
}
`),
			TS: inlineSDKBridgeToolDef(`
export default function tool(input: { subject: string }) {
  return input.subject.length;
}
`),
		},
		{
			Name: "json.nope",
			Sig: tooltest.NewTSSig(t, `
export default function tool(when: Date) {
  return when.toISOString();
}
`),
			TS: inlineSDKBridgeToolDef(`
export default function tool(when: Date) {
  return when.toISOString();
}
`),
		},
	})

	bridge := New(Options{})
	bridge.toolsets["ts_1"] = &composedToolset{
		mode:     ComposeModeDirect,
		prepared: prepared,
		tools:    describeTools(ComposeModeDirect, prepared, nil),
	}

	_, err := bridge.invoke(context.Background(), ToolInvokeParams{
		ToolsetID: "ts_1",
		ToolName:  "json.nope",
		Params: map[string]any{
			"when": "2026-04-13T12:00:00Z",
		},
	})
	if err == nil {
		t.Fatal("invoke error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), `unknown tool "json.nope"`) {
		t.Fatalf("invoke error = %v, want unknown tool", err)
	}
}

func inlineSDKBridgeToolDef(source string) *tooldef.TSToolDef {
	return &tooldef.TSToolDef{
		Entry: "tools/test.ts",
		Files: fstest.MapFS{
			"tools/test.ts": &fstest.MapFile{Data: []byte(source)},
		},
	}
}

func TestBridgeComposeDirectFromFileIncludesBuiltinManagementToolsWhenEnabled(t *testing.T) {
	path := writeLocalToolsetFileWithToolsetManagement(t, "calc")
	bridge := New(Options{})

	result, err := bridge.handleMethod(context.Background(), "toolset.compose", mustJSON(t, ComposeParams{
		Mode:        ComposeModeDirect,
		ToolsetFile: path,
	}))
	if err != nil {
		t.Fatalf("toolset.compose: %v", err)
	}

	composed := result.(ComposeResult)
	var names []string
	for _, tool := range composed.Tools {
		names = append(names, tool.Name)
	}
	if !slices.Contains(names, "calc.add") {
		t.Fatalf("compose tools = %#v, want calc.add", names)
	}
	if !slices.Contains(names, "toolbox.search") {
		t.Fatalf("compose tools = %#v, want toolbox.search", names)
	}
	if !slices.Contains(names, "toolbox.install") {
		t.Fatalf("compose tools = %#v, want toolbox.install", names)
	}
}

func TestBridgeComposeCodemodeReturnsSingleTool(t *testing.T) {
	path := writeLocalToolsetFile(t, "calc")
	bridge := New(Options{})

	result, err := bridge.handleMethod(context.Background(), "toolset.compose", mustJSON(t, ComposeParams{
		Mode:        ComposeModeCodemode,
		ToolsetFile: path,
	}))
	if err != nil {
		t.Fatalf("toolset.compose: %v", err)
	}

	composed := result.(ComposeResult)
	if len(composed.Tools) != 1 {
		t.Fatalf("codemode compose tools = %d, want 1", len(composed.Tools))
	}
	if got := composed.Tools[0].Name; got != CodeModeToolName {
		t.Fatalf("codemode tool name = %q, want %q", got, CodeModeToolName)
	}
	props, _ := composed.Tools[0].ParamsSchema["properties"].(map[string]any)
	if _, ok := props[codemodesession.TypeScriptCellSourceParam]; !ok {
		t.Fatalf("codemode params schema = %#v, want %q", composed.Tools[0].ParamsSchema, codemodesession.TypeScriptCellSourceParam)
	}
	if _, ok := props[codemodesession.TimeoutSecsParam]; !ok {
		t.Fatalf("codemode params schema = %#v, want %q", composed.Tools[0].ParamsSchema, codemodesession.TimeoutSecsParam)
	}
	if !strings.Contains(composed.Tools[0].Description, "super_tool submits a code cell to a notebook like environment") {
		t.Fatalf("codemode description = %q, want super_tool instructions", composed.Tools[0].Description)
	}

	invoked, err := bridge.handleMethod(context.Background(), "tool.invoke", mustJSON(t, ToolInvokeParams{
		ToolsetID: composed.ToolsetID,
		ToolName:  CodeModeToolName,
		Params: map[string]any{
			codemodesession.TypeScriptCellSourceParam: `calc.calc.add(4, 5)`,
		},
	}))
	if err != nil {
		t.Fatalf("tool.invoke: %v", err)
	}
	if got := invoked.(ToolInvokeResult).Content; !strings.Contains(got, "=> 9") {
		t.Fatalf("codemode invoke result = %q, want completion preview 9", got)
	}

	timedOut, err := bridge.handleMethod(context.Background(), "tool.invoke", mustJSON(t, ToolInvokeParams{
		ToolsetID: composed.ToolsetID,
		ToolName:  CodeModeToolName,
		Params: map[string]any{
			codemodesession.TypeScriptCellSourceParam: `await new Promise(() => {})`,
			codemodesession.TimeoutSecsParam:          0.25,
		},
	}))
	if err != nil {
		t.Fatalf("tool.invoke timeout case: %v", err)
	}
	if got := timedOut.(ToolInvokeResult).Content; !strings.Contains(got, "context deadline exceeded") {
		t.Fatalf("codemode timeout result = %q, want timeout failure", got)
	}
}

func TestBridgeComposeInlineToolset(t *testing.T) {
	archiveBytes, manifestBytes := packSourceFixtureBytesWithModule(t, "calc", "github.com/admin/stub")
	const commitSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	cache, err := registry.NewCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewCache(): %v", err)
	}
	bridge := New(Options{
		Resolver: registry.NewResolver(cache, stubSource{
			archive:  archiveBytes,
			manifest: manifestBytes,
			metadata: registry.ResolveMetadata{
				ArchiveSHA256: sha256Hex(archiveBytes),
				GitSHA:        commitSHA,
				ResolvedFrom:  registry.ResolvedFromGitHubRelease,
				ResolvedAt:    "2026-04-10T12:00:00Z",
			},
		}),
	})

	result, err := bridge.handleMethod(context.Background(), "toolset.compose", mustJSON(t, map[string]any{
		"mode": "direct",
		"toolset": map[string]any{
			"packages": map[string]string{"github.com/admin/stub": "v1.0.0"},
			"tools": []map[string]string{
				{"tool": "github.com/admin/stub@v1.0.0/calc.add"},
			},
		},
	}))
	if err != nil {
		t.Fatalf("inline toolset.compose: %v", err)
	}

	composed := result.(ComposeResult)
	if composed.ToolsetID == "" || len(composed.Tools) == 0 {
		t.Fatalf("compose result = %#v, want non-empty toolset_id and tools", composed)
	}

	var names []string
	for _, tool := range composed.Tools {
		names = append(names, tool.Name)
	}
	if !slices.Contains(names, "calc.add") {
		t.Fatalf("compose tools = %#v, want calc.add", names)
	}
	if !slices.Contains(names, "toolbox.search") {
		t.Fatalf("compose tools = %#v, want toolbox.search", names)
	}
	if !slices.Contains(names, "toolbox.inspect") {
		t.Fatalf("compose tools = %#v, want toolbox.inspect", names)
	}
	if slices.Contains(names, "toolbox.install") {
		t.Fatalf("compose tools = %#v, do not want toolbox.install", names)
	}

	inspectedAny, err := bridge.handleMethod(context.Background(), "toolset.inspect", mustJSON(t, ToolsetInspectParams{
		ToolsetID: composed.ToolsetID,
		Target:    "github.com/admin/stub",
	}))
	if err != nil {
		t.Fatalf("toolset.inspect: %v", err)
	}
	inspected := inspectedAny.(ToolsetInspectResult)
	if inspected.Source != "toolset" {
		t.Fatalf("inspect source = %q, want %q", inspected.Source, "toolset")
	}
	if got := inspected.Package.Module.String(); got != "github.com/admin/stub" {
		t.Fatalf("inspect package module = %q, want %q", got, "github.com/admin/stub")
	}
}

func TestBridgeComposeInlineToolsetRejectsAgentToolsetManagement(t *testing.T) {
	archiveBytes, manifestBytes := packSourceFixtureBytesWithModule(t, "calc", "github.com/admin/stub")

	cache, err := registry.NewCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewCache(): %v", err)
	}
	bridge := New(Options{
		Resolver: registry.NewResolver(cache, stubSource{
			archive:  archiveBytes,
			manifest: manifestBytes,
			metadata: registry.ResolveMetadata{
				ArchiveSHA256: sha256Hex(archiveBytes),
				GitSHA:        "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				ResolvedFrom:  registry.ResolvedFromGitHubRelease,
				ResolvedAt:    "2026-04-10T12:00:00Z",
			},
		}),
	})

	_, err = bridge.handleMethod(context.Background(), "toolset.compose", mustJSON(t, map[string]any{
		"mode": "direct",
		"toolset": map[string]any{
			"packages": map[string]string{"github.com/admin/stub": "v1.0.0"},
			"tools": []map[string]string{
				{"tool": "github.com/admin/stub@v1.0.0/calc.add"},
			},
			"agent": map[string]any{
				"unsafe": map[string]any{
					"allow_toolset_management": true,
				},
			},
		},
	}))
	if err == nil {
		t.Fatal("inline toolset.compose error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "inline toolset compose does not support agent.unsafe.allow_toolset_management") {
		t.Fatalf("inline toolset.compose error = %v, want unsupported inline management", err)
	}
}

func TestBridgeInstallUpdatesFileEvenWhenAgentManagementToolsAreHidden(t *testing.T) {
	path := writeLocalToolsetFile(t, "calc")
	sharedTypesModule := fixtureModule(t, "shared-types")
	dir := filepath.Dir(path)
	if err := os.WriteFile(filepath.Join(dir, "toolbox.toolset.local.json"), mustJSON(t, map[string]any{
		"replace": map[string]string{
			fixtureModule(t, "calc"): loadSourceFixtureDir(t, "calc"),
			sharedTypesModule:        loadSourceFixtureDir(t, "shared-types"),
		},
	}), 0o644); err != nil {
		t.Fatalf("WriteFile(local): %v", err)
	}

	bridge := New(Options{})
	composedAny, err := bridge.handleMethod(context.Background(), "toolset.compose", mustJSON(t, ComposeParams{
		Mode:        ComposeModeDirect,
		ToolsetFile: path,
	}))
	if err != nil {
		t.Fatalf("toolset.compose: %v", err)
	}
	composed := composedAny.(ComposeResult)

	var initialNames []string
	for _, tool := range composed.Tools {
		initialNames = append(initialNames, tool.Name)
	}
	if slices.Contains(initialNames, "toolbox.install") {
		t.Fatalf("compose tools = %#v, do not want toolbox.install when agent management is disabled", initialNames)
	}

	updatedAny, err := bridge.handleMethod(context.Background(), "toolset.install", mustJSON(t, ToolsetInstallParams{
		ToolsetID: composed.ToolsetID,
		Package:   sharedTypesModule + "@v1.2.3",
	}))
	if err != nil {
		t.Fatalf("toolset.install: %v", err)
	}
	updated := updatedAny.(ToolsetUpdateResult)

	var updatedNames []string
	for _, tool := range updated.Tools {
		updatedNames = append(updatedNames, tool.Name)
	}
	if !slices.Contains(updatedNames, "tickets.list") {
		t.Fatalf("updated tools = %#v, want tickets.list", updatedNames)
	}
	if !slices.Contains(updatedNames, "calc.add") {
		t.Fatalf("updated tools = %#v, want calc.add", updatedNames)
	}

	written, err := toolsetfile.Load(path)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}
	if got := written.Packages[sharedTypesModule]; got != "v1.2.3" {
		t.Fatalf("written package version = %q, want %q", got, "v1.2.3")
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%T): %v", value, err)
	}
	return data
}

func writeLocalToolsetFile(t *testing.T, fixtureName string) string {
	return writeLocalToolsetFileForAgent(t, fixtureName, false)
}

func writeLocalToolsetFileWithToolsetManagement(t *testing.T, fixtureName string) string {
	return writeLocalToolsetFileForAgent(t, fixtureName, true)
}

func writeLocalToolsetFileForAgent(t *testing.T, fixtureName string, allowToolsetManagement bool) string {
	t.Helper()
	dir := t.TempDir()
	module := fixtureModule(t, fixtureName)
	path := filepath.Join(dir, "toolbox.toolset.json")
	if err := os.WriteFile(path, mustJSON(t, map[string]any{
		"packages": map[string]string{
			module: "v1.2.3",
		},
		"tools": []map[string]string{
			{"tool": module + "@v1.2.3/calc.add"},
		},
		"agent": map[string]any{
			"allow_package_discovery": true,
			"unsafe": map[string]any{
				"allow_toolset_management": allowToolsetManagement,
			},
		},
	}), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "toolbox.toolset.local.json"), mustJSON(t, map[string]any{
		"replace": map[string]string{
			module: loadSourceFixtureDir(t, fixtureName),
		},
	}), 0o644); err != nil {
		t.Fatalf("WriteFile(local): %v", err)
	}
	return path
}

func loadSourceFixtureDir(t *testing.T, fixtureName string) string {
	t.Helper()
	for _, dir := range fixtures.SourceDirs() {
		if filepath.Base(dir) == fixtureName {
			return dir
		}
	}
	t.Fatalf("source fixture %q not found", fixtureName)
	return ""
}

func fixtureModule(t *testing.T, fixtureName string) string {
	t.Helper()
	path := filepath.Join(loadSourceFixtureDir(t, fixtureName), packaging.DevManifestFilename)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	var manifest struct {
		Module string `json:"module"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", path, err)
	}
	return manifest.Module
}

func packSourceFixtureBytesWithModule(t *testing.T, fixtureName, module string) ([]byte, []byte) {
	t.Helper()

	workDir := filepath.Join(t.TempDir(), fixtureName)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", workDir, err)
	}
	copyFixtureDir(t, loadSourceFixtureDir(t, fixtureName), workDir)
	rewriteSourceFixtureModule(t, workDir, module)

	outDir := t.TempDir()
	result, err := packaging.Pack(workDir, outDir)
	if err != nil {
		t.Fatalf("Pack(%q): %v", workDir, err)
	}

	archiveBytes, err := os.ReadFile(result.ArchivePath)
	if err != nil {
		t.Fatalf("read archive %s: %v", result.ArchivePath, err)
	}
	manifestBytes, err := os.ReadFile(result.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest %s: %v", result.ManifestPath, err)
	}
	return archiveBytes, manifestBytes
}

func rewriteSourceFixtureModule(t *testing.T, dir, module string) {
	t.Helper()
	path := filepath.Join(dir, packaging.DevManifestFilename)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}

	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", path, err)
	}
	manifest["module"] = module
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal(%q): %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func copyFixtureDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", src, err)
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0o755); err != nil {
				t.Fatalf("MkdirAll(%q): %v", dstPath, err)
			}
			copyFixtureDir(t, srcPath, dstPath)
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", srcPath, err)
		}
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", dstPath, err)
		}
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type stubSource struct {
	archive  []byte
	manifest []byte
	metadata registry.ResolveMetadata
}

func (s stubSource) Fetch(context.Context, registry.ModulePath, registry.Version) (registry.FetchResult, error) {
	return registry.FetchResult{
		Archive:  s.archive,
		Manifest: s.manifest,
		Metadata: s.metadata,
	}, nil
}
