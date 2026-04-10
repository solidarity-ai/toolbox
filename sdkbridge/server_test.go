package sdkbridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry"
	"github.com/solidarity-ai/toolbox/testutil/fixtures"
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
					"code": "export default tools.calc.add({ a: 2, b: 3 });",
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
	if invokeResp.Result.Content != "5" {
		t.Fatalf("invoke content = %q, want %q", invokeResp.Result.Content, "5")
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

	invoked, err := bridge.handleMethod(context.Background(), "tool.invoke", mustJSON(t, ToolInvokeParams{
		ToolsetID: composed.ToolsetID,
		ToolName:  CodeModeToolName,
		Params: map[string]any{
			"code": `
export default tools.calc.add({ a: 4, b: 5 });
`,
		},
	}))
	if err != nil {
		t.Fatalf("tool.invoke: %v", err)
	}
	if got := invoked.(ToolInvokeResult).Content; got != "9" {
		t.Fatalf("codemode invoke result = %q, want %q", got, "9")
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
