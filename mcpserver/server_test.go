package mcpserver_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/solidarity-ai/toolbox/mcpserver"
	"github.com/solidarity-ai/toolbox/testutil/mcptest"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestMCPServerListsVisibleInvokeTools(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(tooltest.CalcToolset(t)))
	names := h.ToolNames()

	assertContains(t, names, "calc.add")
	assertContains(t, names, "calc.asyncAdd")
}

func TestMCPServerExposesResolvedParamSchema(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(tooltest.CalcToolset(t)))
	tools := h.ListTools()

	var calcAdd *mcp.Tool
	for i := range tools.Tools {
		if tools.Tools[i].Name == "calc.add" {
			calcAdd = &tools.Tools[i]
			break
		}
	}
	if calcAdd == nil {
		t.Fatal("expected calc.add in MCP tool list")
	}

	if calcAdd.InputSchema.Type != "object" {
		t.Fatalf("input schema type = %q, want object", calcAdd.InputSchema.Type)
	}
	if len(calcAdd.InputSchema.Properties) == 0 {
		t.Fatalf("input schema properties = %#v, want non-empty schema", calcAdd.InputSchema.Properties)
	}

	aSchema, ok := calcAdd.InputSchema.Properties["a"].(map[string]any)
	if !ok {
		t.Fatalf("property a = %#v, want schema object", calcAdd.InputSchema.Properties["a"])
	}
	if aSchema["type"] != "number" {
		t.Fatalf("property a type = %#v, want number", aSchema["type"])
	}

	bSchema, ok := calcAdd.InputSchema.Properties["b"].(map[string]any)
	if !ok {
		t.Fatalf("property b = %#v, want schema object", calcAdd.InputSchema.Properties["b"])
	}
	if bSchema["type"] != "number" {
		t.Fatalf("property b type = %#v, want number", bSchema["type"])
	}
}

func TestMCPServerCallsInvokeForTool(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(tooltest.CalcToolset(t)))

	result := h.CallTool("calc.add", map[string]any{
		"a": 5,
		"b": 5,
	})
	if result.IsError {
		t.Fatalf("expected non-error result")
	}
	if len(result.Content) == 0 {
		t.Fatalf("expected text content")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	if text.Text != "10" {
		t.Fatalf("expected result 10, got %#v", text.Text)
	}
}

func TestMCPServerCallsInvokeForDifferentArgs(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(tooltest.CalcToolset(t)))

	result := h.CallTool("calc.asyncAdd", map[string]any{
		"a": 7,
		"b": 4,
	})
	if result.IsError {
		t.Fatalf("expected non-error result")
	}
	if len(result.Content) == 0 {
		t.Fatalf("expected text content")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	if text.Text != "11" {
		t.Fatalf("expected result 11, got %#v", text.Text)
	}
}

func TestMCPServerCallsInvokeForStringAndNumberArgs(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(tooltest.CalcToolset(t)))

	result := h.CallTool("calc.add", map[string]any{
		"a": "6",
		"b": 3,
	})
	if !result.IsError {
		t.Fatalf("expected error result")
	}
	if len(result.Content) == 0 {
		t.Fatalf("expected error content")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	if !strings.Contains(text.Text, "typescript check failed") {
		t.Fatalf("expected typecheck failure, got %#v", text.Text)
	}
}

func TestMCPServerCallsInvokeForDistArchivePackage(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(tooltest.CalcDistToolset(t)))
	names := h.ToolNames()

	assertContains(t, names, "calc.add")
	assertContains(t, names, "calc.sub")
	assertContains(t, names, "calc.asyncAdd")

	result := h.CallTool("calc.add", map[string]any{
		"a": 3,
		"b": 7,
	})
	if result.IsError {
		if len(result.Content) > 0 {
			text, _ := mcp.AsTextContent(result.Content[0])
			t.Fatalf("expected non-error result, got: %s", text.Text)
		}
		t.Fatalf("expected non-error result")
	}
	if len(result.Content) == 0 {
		t.Fatalf("expected text content")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	if text.Text != "10" {
		t.Fatalf("expected result 10, got %#v", text.Text)
	}
}

func TestMCPServerRunsExternalWasmerPackageFromDir(t *testing.T) {
	requireTSWasmerArtifacts(t)

	builder := toolset.New()
	if err := builder.AddFromDir(gwsFixtureDir()); err != nil {
		t.Fatalf("add external package dir: %v", err)
	}

	h := mcptest.NewHarness(t, mcpserver.New(mustResolve(t, builder)))
	result := h.CallTool("users.list", map[string]any{})
	if result.IsError {
		t.Fatalf("expected non-error result")
	}
	if len(result.Content) == 0 {
		t.Fatalf("expected text content")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	if text.Text != "wasm-ada@example.com" {
		t.Fatalf("expected parsed tool result, got %#v", text.Text)
	}
}

// TODO: Move these copied-package Wasmer integration checks out of the default
// MCP test file once we have a better home for them, either in toolpkg-gws
// itself or in dedicated WASIX/Wasm runtime integration tests.
func TestMCPServerRunsWasmerPackageFromCopiedDirWithBinaryNamedArtifact(t *testing.T) {
	requireTSWasmerArtifacts(t)

	srcDir := gwsFixtureDir()
	dstDir := filepath.Join(t.TempDir(), "toolpkg-gws")
	if err := copyPackageDir(srcDir, dstDir); err != nil {
		t.Fatalf("copy package dir: %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(dstDir, "tools", "users.list.ts"),
		[]byte(strings.ReplaceAll(readFile(t, filepath.Join(srcDir, "tools", "users.list.ts")), `"gwc"`, `"gwc2"`)),
		0o644,
	); err != nil {
		t.Fatalf("rewrite tool source: %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(dstDir, "toolbox.devpkg.json"),
		[]byte(strings.ReplaceAll(
			readFile(t, filepath.Join(srcDir, "toolbox.devpkg.json")),
			`"gwc": "dist/gwc.wasm"`,
			`"gwc2": "dist/gwc2.wasm"`,
		)),
		0o644,
	); err != nil {
		t.Fatalf("rewrite package manifest: %v", err)
	}

	srcWasm := filepath.Join(srcDir, "dist", "gwc.wasm")
	dstWasm := filepath.Join(dstDir, "dist", "gwc2.wasm")
	if err := copyFile(srcWasm, dstWasm); err != nil {
		t.Fatalf("copy renamed wasm: %v", err)
	}

	builder := toolset.New()
	if err := builder.AddFromDir(dstDir); err != nil {
		t.Fatalf("add copied package dir: %v", err)
	}

	h := mcptest.NewHarness(t, mcpserver.New(mustResolve(t, builder)))
	result := h.CallTool("users.list", map[string]any{})
	if result.IsError {
		t.Fatalf("expected non-error result")
	}
	if len(result.Content) == 0 {
		t.Fatalf("expected text content")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	if text.Text != "wasm-ada@example.com" {
		t.Fatalf("expected parsed tool result, got %#v", text.Text)
	}
}

func requireTSWasmerArtifacts(t *testing.T) {
	t.Helper()
	tooltest.EnsureSandboxBinary(t)

	paths := []string{
		filepath.Join(gwsFixtureDir(), "toolbox.devpkg.json"),
		filepath.Join(gwsFixtureDir(), "dist", "gwc.wasm"),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("fixture artifact missing: %s", p)
		}
	}
}

func gwsFixtureDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("mcpserver_test: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "testutil", "fixtures", "toolbox.pkgs", "google-workspace")
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

type sourcePackageManifest struct {
	AdditionalTypeScriptGlobs []string          `json:"additionalTypeScriptGlobs"`
	Executables               map[string]string `json:"executables"`
	Tools                     []struct {
		EntryTS string `json:"entry_ts"`
	} `json:"tools"`
}

func copyPackageDir(src string, dst string) error {
	manifestPath := filepath.Join(src, "toolbox.devpkg.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}

	var manifest sourcePackageManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return err
	}

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	if err := copyFile(manifestPath, filepath.Join(dst, "toolbox.devpkg.json")); err != nil {
		return err
	}

	seen := map[string]struct{}{}
	copyRel := func(rel string) error {
		if _, ok := seen[rel]; ok {
			return nil
		}
		seen[rel] = struct{}{}
		return copyFile(filepath.Join(src, rel), filepath.Join(dst, rel))
	}

	for _, tool := range manifest.Tools {
		if err := copyRel(tool.EntryTS); err != nil {
			return err
		}
	}
	for _, path := range manifest.Executables {
		if err := copyRel(path); err != nil {
			return err
		}
	}
	for _, pattern := range manifest.AdditionalTypeScriptGlobs {
		matches, err := filepath.Glob(filepath.Join(src, pattern))
		if err != nil {
			return err
		}
		for _, match := range matches {
			info, err := os.Lstat(match)
			if err != nil {
				return err
			}
			if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			rel, err := filepath.Rel(src, match)
			if err != nil {
				return err
			}
			if err := copyRel(rel); err != nil {
				return err
			}
		}
	}

	return nil
}

func copyFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Close()
}

func TestMCPServerRunsWasip2PackageHTTPClient(t *testing.T) {
	requireTSWasip2Artifacts(t)

	builder := toolset.New()
	if err := builder.AddFromDir(httpClientFixtureDir()); err != nil {
		t.Fatalf("add http-client package dir: %v", err)
	}

	h := mcptest.NewHarness(t, mcpserver.New(mustResolve(t, builder)))
	result := h.CallTool("httpClient.fetch", map[string]any{})
	if result.IsError {
		t.Fatalf("expected non-error result")
	}

	if len(result.Content) == 0 {
		t.Fatalf("expected content in result")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	resultStr := text.Text
	if !strings.Contains(resultStr, "Status: 200 OK") {
		t.Fatalf("expected result to contain 'Status: 200 OK', got:\n%s", resultStr)
	}
	if !strings.Contains(resultStr, "httpbin.org") {
		t.Fatalf("expected result to contain 'httpbin.org', got:\n%s", resultStr)
	}
}

func requireTSWasip2Artifacts(t *testing.T) {
	t.Helper()

	tooltest.EnsureSandboxBinary(t)

	paths := []string{
		filepath.Join(httpClientFixtureDir(), "toolbox.devpkg.json"),
		filepath.Join(httpClientFixtureDir(), "dist", "http-client.wasm"),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("wasip2 artifacts not ready: missing %s", p)
		}
	}
}

func httpClientFixtureDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("mcpserver_test: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "testutil", "fixtures", "toolbox.pkgs", "http-client")
}

func TestMCPServerFetchToolMakesHTTPRequest(t *testing.T) {
	// Start a test HTTP server that returns known content.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"greeting":"hello from test server"}`))
	}))
	t.Cleanup(srv.Close)

	h := mcptest.NewHarness(t, mcpserver.New(tooltest.FetchTestToolset(t)))

	// Invoke the fetch-test.get tool with the test server URL.
	result := h.CallTool("fetchTest.get", map[string]any{
		"url": srv.URL,
	})
	if result.IsError {
		if len(result.Content) > 0 {
			text, _ := mcp.AsTextContent(result.Content[0])
			t.Fatalf("expected non-error result, got: %s", text.Text)
		}
		t.Fatalf("expected non-error result")
	}
	if len(result.Content) == 0 {
		t.Fatalf("expected text content")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	resultStr := text.Text

	// The tool returns JSON with status and body.
	var fetchResult struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal([]byte(resultStr), &fetchResult); err != nil {
		t.Fatalf("parse fetch result: %v (raw: %s)", err, resultStr)
	}
	if fetchResult.Status != 200 {
		t.Fatalf("expected status 200, got %d", fetchResult.Status)
	}
	if !strings.Contains(fetchResult.Body, "hello from test server") {
		t.Fatalf("expected body to contain greeting, got: %s", fetchResult.Body)
	}
}

func mustResolve(t testing.TB, builder *toolset.Builder) toolset.ResolvedToolset {
	t.Helper()
	resolved, err := builder.Resolve(toolset.Config{})
	if err != nil {
		t.Fatalf("resolve toolset: %v", err)
	}
	return resolved
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, v := range values {
		if v == want {
			return
		}
	}
	t.Fatalf("expected %q in %v", want, values)
}
