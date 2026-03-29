package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	clienttransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/solidarity-ai/toolbox/registry"
	"github.com/solidarity-ai/toolbox/testutil/fixtures"
	"github.com/solidarity-ai/toolbox/toolsetfile"
)

func TestRunVersions(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/admin/stub/releases":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 3, "tag_name": "v1.2.0"},
				{"id": 2, "tag_name": "v1.0.0"},
				{"id": 1, "tag_name": "not-a-version"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	t.Setenv("GITHUB_BASE_URL", ts.URL)
	t.Setenv("TOOLBOX_CACHE_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := run([]string{"versions", "github.com/admin/stub"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	got := strings.Fields(stdout.String())
	want := []string{"v1.2.0", "v1.0.0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("versions output = %#v, want %#v", got, want)
	}
}

func TestRunVersionsUsesGitHubTokenAuthorizationWithoutLeakingIt(t *testing.T) {
	const token = "ghp-secret-token-for-cli-test"

	var mu sync.Mutex
	var authHeaders []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		mu.Unlock()

		switch r.URL.Path {
		case "/repos/admin/stub/releases":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "tag_name": "v1.0.0"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	t.Setenv("GITHUB_BASE_URL", ts.URL)
	t.Setenv("GITHUB_TOKEN", token)
	t.Setenv("TOOLBOX_CACHE_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := run([]string{"versions", "github.com/admin/stub"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.Fields(stdout.String()); !reflect.DeepEqual(got, []string{"v1.0.0"}) {
		t.Fatalf("versions output = %#v, want %#v", got, []string{"v1.0.0"})
	}

	mu.Lock()
	gotHeaders := append([]string(nil), authHeaders...)
	mu.Unlock()
	if !reflect.DeepEqual(gotHeaders, []string{"token " + token}) {
		t.Fatalf("Authorization headers = %#v, want %#v", gotHeaders, []string{"token " + token})
	}
	assertNoTokenLeak(t, token, stdout.String(), stderr.String())
}

func TestRunVersionsRejectsInvalidModuleArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"versions", "admin"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run() error = nil, want invalid module path error")
	}
	if !strings.Contains(err.Error(), `module path "admin" must have at least host/path`) {
		t.Fatalf("error = %v, want invalid module path context", err)
	}
}

func TestRunResolveWritesLockfile(t *testing.T) {
	archiveBytes, manifestBytes := loadDistFixtureBytes(t, "calc-dist")
	const commitSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/admin/stub/releases/tags/v1.0.0":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":       10,
				"tag_name": "v1.0.0",
				"assets": []map[string]any{
					{"id": 1, "name": "calc.toolbox.pkg"},
					{"id": 2, "name": "toolbox.pkg.json"},
				},
			})
		case "/repos/admin/stub/git/ref/tags/v1.0.0":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": map[string]any{"type": "commit", "sha": commitSHA},
			})
		case "/repos/admin/stub/releases/assets/1":
			_, _ = w.Write(archiveBytes)
		case "/repos/admin/stub/releases/assets/2":
			_, _ = w.Write(manifestBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	toolsetPath := writeToolsetFile(t, map[string]any{
		"packages": map[string]string{"github.com/admin/stub": "v1.0.0"},
		"tools":    []map[string]string{{"tool": "github.com/admin/stub@v1.0.0/calc.add"}},
	})

	t.Setenv("GITHUB_BASE_URL", ts.URL)
	t.Setenv("TOOLBOX_CACHE_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := run([]string{"resolve", "--file", toolsetPath}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "resolved 3 tools") {
		t.Fatalf("stdout = %q, want resolved tool count", stdout.String())
	}

	lock, err := toolsetfile.LoadLock(strings.TrimSuffix(toolsetPath, ".json") + ".lock")
	if err != nil {
		t.Fatalf("LoadLock(): %v", err)
	}
	entry, ok := lock.Packages["github.com/admin/stub@v1.0.0"]
	if !ok {
		t.Fatalf("lock packages = %#v, want github.com/admin/stub@v1.0.0", lock.Packages)
	}
	if entry.GitSHA != commitSHA {
		t.Fatalf("git_sha = %q, want %q", entry.GitSHA, commitSHA)
	}
}

func TestRunResolveUsesCommittedLockCacheHitWithoutRefetch(t *testing.T) {
	archiveBytes, manifestBytes := loadDistFixtureBytes(t, "calc-dist")
	const commitSHA = "cccccccccccccccccccccccccccccccccccccccc"

	var mu sync.Mutex
	var requestPaths []string
	secondResolve := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestPaths = append(requestPaths, r.URL.Path)
		refetch := secondResolve
		mu.Unlock()

		if refetch {
			http.Error(w, "unexpected refetch during committed-lock cache verification for "+r.URL.Path, http.StatusInternalServerError)
			return
		}

		switch r.URL.Path {
		case "/repos/admin/stub/releases/tags/v1.0.0":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":       10,
				"tag_name": "v1.0.0",
				"assets": []map[string]any{
					{"id": 1, "name": "calc.toolbox.pkg"},
					{"id": 2, "name": "toolbox.pkg.json"},
				},
			})
		case "/repos/admin/stub/git/ref/tags/v1.0.0":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": map[string]any{"type": "commit", "sha": commitSHA},
			})
		case "/repos/admin/stub/releases/assets/1":
			_, _ = w.Write(archiveBytes)
		case "/repos/admin/stub/releases/assets/2":
			_, _ = w.Write(manifestBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	toolsetPath := writeToolsetFile(t, map[string]any{
		"packages": map[string]string{"github.com/admin/stub": "v1.0.0"},
		"tools":    []map[string]string{{"tool": "github.com/admin/stub@v1.0.0/calc.add"}},
	})

	t.Setenv("GITHUB_BASE_URL", ts.URL)
	t.Setenv("TOOLBOX_CACHE_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	if err := run([]string{"resolve", "--file", toolsetPath}, &stdout, &stderr); err != nil {
		t.Fatalf("first run() error: %v\nstderr=%s", err, stderr.String())
	}
	lockPath := strings.TrimSuffix(toolsetPath, ".json") + ".lock"
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", lockPath, err)
	}

	mu.Lock()
	firstRunRequests := append([]string(nil), requestPaths...)
	secondResolve = true
	mu.Unlock()
	if len(firstRunRequests) == 0 {
		t.Fatal("first resolve made no HTTP requests, want initial fetch before cache-hit verification")
	}

	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"resolve", "--file", toolsetPath}, &stdout, &stderr); err != nil {
		t.Fatalf("second run() error: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "resolved 3 tools") {
		t.Fatalf("second resolve stdout = %q, want resolved tool count", stdout.String())
	}

	after, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", lockPath, err)
	}
	if string(after) != string(before) {
		t.Fatalf("lockfile changed during committed-lock cache hit:\nbefore:\n%s\nafter:\n%s", string(before), string(after))
	}

	mu.Lock()
	secondRunRequests := append([]string(nil), requestPaths...)
	mu.Unlock()
	if !reflect.DeepEqual(secondRunRequests, firstRunRequests) {
		t.Fatalf("HTTP requests changed on second resolve: got %#v, want %#v", secondRunRequests, firstRunRequests)
	}
}

func TestRunResolveSiblingLocalOverlayPreservesLockfile(t *testing.T) {
	archiveBytes, manifestBytes := loadDistFixtureBytes(t, "calc-dist")
	githubIssuesDir := loadSourceFixtureDir(t, "github-issues")

	workspace := filepath.Join(t.TempDir(), "nested", "support-agent")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", workspace, err)
	}
	toolsetPath := filepath.Join(workspace, "support-agent.toolset.json")
	writeJSONFile(t, toolsetPath, map[string]any{
		"packages": map[string]string{
			"example.com/zeta/github-issues": "v2.0.0",
			"example.com/acme/calc":          "v1.2.3",
		},
		"tools": []map[string]string{
			{"tool": "example.com/zeta/github-issues@v2.0.0/github-issues.get"},
			{"tool": "example.com/acme/calc@v1.2.3/calc.add"},
		},
	})

	file, err := toolsetfile.Load(toolsetPath)
	if err != nil {
		t.Fatalf("Load(%q): %v", toolsetPath, err)
	}
	replacePath, err := filepath.Rel(filepath.Dir(file.LocalFilename()), githubIssuesDir)
	if err != nil {
		t.Fatalf("filepath.Rel(%q, %q): %v", filepath.Dir(file.LocalFilename()), githubIssuesDir, err)
	}
	if filepath.IsAbs(replacePath) {
		t.Fatalf("replace path = %q, want relative path anchored to sibling overlay", replacePath)
	}
	writeJSONFile(t, file.LocalFilename(), map[string]any{
		"replace": map[string]string{
			"example.com/zeta/github-issues": replacePath,
		},
	})

	cacheDir := t.TempDir()
	cache, err := registry.NewCache(cacheDir)
	if err != nil {
		t.Fatalf("NewCache(%q): %v", cacheDir, err)
	}
	if err := cache.Put(registry.ModulePath("example.com/acme/calc"), registry.Version("v1.2.3"), archiveBytes, manifestBytes); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	locked := &toolsetfile.ToolsetLockFile{Packages: map[string]toolsetfile.ToolsetLockEntry{
		"example.com/zeta/github-issues@v2.0.0": {
			ArchiveSHA256: strings.Repeat("a", 64),
			GitSHA:        strings.Repeat("b", 40),
			ResolvedFrom:  toolsetfile.ToolsetLockResolvedFromGitHubRelease,
			ResolvedAt:    "2026-03-28T12:05:00Z",
		},
		"example.com/acme/calc@v1.2.3": {
			ArchiveSHA256: sha256HexForTest(archiveBytes),
			GitSHA:        strings.Repeat("c", 40),
			ResolvedFrom:  toolsetfile.ToolsetLockResolvedFromGitSource,
			ResolvedAt:    "2026-03-28T12:00:00Z",
		},
	}}
	if err := locked.Write(file.LockFilename()); err != nil {
		t.Fatalf("Write(%q): %v", file.LockFilename(), err)
	}
	before, err := os.ReadFile(file.LockFilename())
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", file.LockFilename(), err)
	}

	t.Setenv("GITHUB_BASE_URL", "http://127.0.0.1:1")
	t.Setenv("TOOLBOX_CACHE_DIR", cacheDir)

	var stdout, stderr bytes.Buffer
	err = run([]string{"resolve", "--file", toolsetPath}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "resolved 4 tools") {
		t.Fatalf("stdout = %q, want resolved tool count for local+cached packages", stdout.String())
	}

	after, err := os.ReadFile(file.LockFilename())
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", file.LockFilename(), err)
	}
	if string(after) != string(before) {
		t.Fatalf("lockfile changed during local overlay resolve:\nbefore:\n%s\nafter:\n%s", string(before), string(after))
	}
}

func TestRunMCPServeLoadsLocalOverlayToolsetAndServesTools(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	packageDir := filepath.Join(workspace, "package-repo")
	consumerDir := filepath.Join(workspace, "consumer-repo")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", packageDir, err)
	}
	if err := os.MkdirAll(consumerDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", consumerDir, err)
	}

	copyFixtureDir(t, loadSourceFixtureDir(t, "calc"), packageDir)

	toolsetPath := filepath.Join(consumerDir, "toolbox.toolset.json")
	writeJSONFile(t, toolsetPath, map[string]any{
		"packages": map[string]string{"example.com/acme/calc": "v1.2.3"},
		"tools":    []map[string]string{{"tool": "example.com/acme/calc@v1.2.3/calc.add"}},
	})
	writeJSONFile(t, filepath.Join(consumerDir, "toolbox.toolset.local.json"), map[string]any{
		"replace": map[string]string{"example.com/acme/calc": "../package-repo"},
	})

	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()
	defer clientRead.Close()
	defer clientWrite.Close()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- runWithIO(
			[]string{"mcp", "serve", "--file", toolsetPath},
			serverRead,
			serverWrite,
			io.Discard,
		)
	}()

	stdio := clienttransport.NewIO(clientRead, clientWrite, io.NopCloser(strings.NewReader("")))
	if err := stdio.Start(context.Background()); err != nil {
		t.Fatalf("stdio.Start(): %v", err)
	}
	defer stdio.Close()

	c := client.NewClient(stdio)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "toolbox-cli-test", Version: "1.0.0"}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}
	initRes, err := c.Initialize(ctx, initReq)
	if err != nil {
		t.Fatalf("Initialize(): %v", err)
	}
	if initRes.ServerInfo.Name != "toolbox-mcp-server" {
		t.Fatalf("server name = %q, want toolbox-mcp-server", initRes.ServerInfo.Name)
	}

	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools(): %v", err)
	}
	if len(tools.Tools) != 3 {
		t.Fatalf("len(tools) = %d, want 3", len(tools.Tools))
	}
	if !hasToolNamed(tools.Tools, "calc.add") {
		t.Fatalf("tools = %#v, want calc.add", tools.Tools)
	}

	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = "calc.add"
	callReq.Params.Arguments = map[string]any{"a": 2.0, "b": 3.0}
	result, err := c.CallTool(ctx, callReq)
	if err != nil {
		t.Fatalf("CallTool(): %v", err)
	}
	if result.IsError {
		t.Fatalf("CallTool() returned MCP error: %#v", result)
	}
	if len(result.Content) == 0 {
		t.Fatalf("CallTool() content = %#v, want at least one content item", result.Content)
	}

	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("first content item = %#v, want text content", result.Content[0])
	}
	if text.Text != "5" {
		t.Fatalf("text content = %q, want 5", text.Text)
	}

	_ = stdio.Close()
	_ = clientWrite.Close()
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("mcp serve exited with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for mcp serve to exit")
	}
}

func TestRunResolveRejectsInvalidUpgradeModuleArgument(t *testing.T) {
	toolsetPath := writeToolsetFile(t, map[string]any{
		"packages": map[string]string{"github.com/admin/stub": "v1.0.0"},
		"tools":    []map[string]string{{"tool": "github.com/admin/stub@v1.0.0/calc.add"}},
	})

	var stdout, stderr bytes.Buffer
	err := run([]string{"resolve", "--file", toolsetPath, "--upgrade", "stub"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run() error = nil, want invalid upgrade module error")
	}
	if !strings.Contains(err.Error(), "parse upgrade module path") {
		t.Fatalf("error = %v, want parse upgrade module path context", err)
	}
}

func TestRunResolveMalformedLocalOverlayKeepsValidationContext(t *testing.T) {
	toolsetPath := writeToolsetFile(t, map[string]any{
		"packages": map[string]string{"example.com/acme/calc": "v1.2.3"},
		"tools":    []map[string]string{},
	})
	file, err := toolsetfile.Load(toolsetPath)
	if err != nil {
		t.Fatalf("Load(%q): %v", toolsetPath, err)
	}
	if err := os.WriteFile(file.LocalFilename(), []byte(`{"replace":["bad"]}`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", file.LocalFilename(), err)
	}

	t.Setenv("GITHUB_BASE_URL", "http://127.0.0.1:1")
	t.Setenv("TOOLBOX_CACHE_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	err = run([]string{"resolve", "--file", toolsetPath}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run() error = nil, want malformed local overlay error")
	}
	assertErrorContains(t, err, "resolve toolset file")
	assertErrorContains(t, err, "schema-validate toolset local file")
	assertErrorContains(t, err, file.LocalFilename())
}

func TestRunResolveUpgradeRewritesToolsetAndLockfile(t *testing.T) {
	archiveBytes, manifestBytes := loadDistFixtureBytes(t, "calc-dist")
	const commitSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/admin/stub/releases":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 2, "tag_name": "v1.2.0"},
				{"id": 1, "tag_name": "v1.0.0"},
			})
		case "/repos/admin/stub/releases/tags/v1.2.0":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":       20,
				"tag_name": "v1.2.0",
				"assets": []map[string]any{
					{"id": 1, "name": "calc.toolbox.pkg"},
					{"id": 2, "name": "toolbox.pkg.json"},
				},
			})
		case "/repos/admin/stub/git/ref/tags/v1.2.0":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": map[string]any{"type": "commit", "sha": commitSHA},
			})
		case "/repos/admin/stub/releases/assets/1":
			_, _ = w.Write(archiveBytes)
		case "/repos/admin/stub/releases/assets/2":
			_, _ = w.Write(manifestBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	toolsetPath := writeToolsetFile(t, map[string]any{
		"packages": map[string]string{"github.com/admin/stub": "v1.0.0"},
		"tools":    []map[string]string{{"tool": "github.com/admin/stub@v1.0.0/calc.add"}},
	})

	t.Setenv("GITHUB_BASE_URL", ts.URL)
	t.Setenv("TOOLBOX_CACHE_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := run([]string{"resolve", "--file", toolsetPath, "--upgrade", "github.com/admin/stub"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "upgraded github.com/admin/stub: v1.0.0 -> v1.2.0") {
		t.Fatalf("stdout = %q, want upgrade message", stdout.String())
	}

	raw, err := os.ReadFile(toolsetPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", toolsetPath, err)
	}
	text := string(raw)
	if !strings.Contains(text, `"github.com/admin/stub": "v1.2.0"`) {
		t.Fatalf("toolset file = %s, want updated package version", text)
	}
	if !strings.Contains(text, `"tool":"github.com/admin/stub@v1.2.0/calc.add"`) {
		t.Fatalf("toolset file = %s, want updated tool FQN", text)
	}

	lock, err := toolsetfile.LoadLock(strings.TrimSuffix(toolsetPath, ".json") + ".lock")
	if err != nil {
		t.Fatalf("LoadLock(): %v", err)
	}
	if _, ok := lock.Packages["github.com/admin/stub@v1.2.0"]; !ok {
		t.Fatalf("lock packages = %#v, want github.com/admin/stub@v1.2.0", lock.Packages)
	}
}

func hasToolNamed(tools []mcp.Tool, want string) bool {
	for _, tool := range tools {
		if tool.Name == want {
			return true
		}
	}
	return false
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

func writeToolsetFile(t *testing.T, value any) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "toolbox.toolset.json")
	writeJSONFile(t, filename, value)
	return filename
}

func writeJSONFile(t *testing.T, filename string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := os.WriteFile(filename, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", filename, err)
	}
}

func loadDistFixtureBytes(t *testing.T, fixtureName string) ([]byte, []byte) {
	t.Helper()
	fixtureDir := ""
	for _, dir := range fixtures.DistDirs() {
		if filepath.Base(dir) == fixtureName {
			fixtureDir = dir
			break
		}
	}
	if fixtureDir == "" {
		t.Fatalf("dist fixture %q not found", fixtureName)
	}

	archivePath := filepath.Join(fixtureDir, "calc.toolbox.pkg")
	manifestPath := filepath.Join(fixtureDir, "toolbox.pkg.json")
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive fixture %s: %v", archivePath, err)
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest fixture %s: %v", manifestPath, err)
	}
	return archiveBytes, manifestBytes
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

func sha256HexForTest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want substring %q", err, want)
	}
}

func assertNoTokenLeak(t *testing.T, token string, outputs ...string) {
	t.Helper()
	for _, output := range outputs {
		if strings.Contains(output, token) {
			t.Fatalf("output leaked token value: %q", output)
		}
	}
}
