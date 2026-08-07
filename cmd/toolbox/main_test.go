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
	"sync/atomic"
	"testing"
	"time"

	"github.com/mackross/repljs/jswire"
	"github.com/mark3labs/mcp-go/client"
	clienttransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/codemodesession"
	"github.com/solidarity-ai/toolbox/credpath"
	"github.com/solidarity-ai/toolbox/daemon"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry"
	"github.com/solidarity-ai/toolbox/sdkbridge"
	"github.com/solidarity-ai/toolbox/testutil"
	"github.com/solidarity-ai/toolbox/testutil/fixtures"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetfile"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("TOOLBOX_REGISTRY", "off")
	_ = os.Setenv("TOOLBOX_SECRET_STORE_SCRYPT_WORK_FACTOR", "10")
	_ = os.Setenv("TOOLBOX_SECRET_STORE_SCRYPT_MAX_WORK_FACTOR", "10")
	os.Exit(m.Run())
}

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
	err := run([]string{"versions", "--source", "registry", "github.com/admin/stub"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	got := strings.Fields(stdout.String())
	want := []string{"v1.2.0", "v1.0.0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("versions output = %#v, want %#v", got, want)
	}
}

func TestRunWithNoArgsPrintsHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage: toolbox <command>") {
		t.Fatalf("stdout = %q, want root usage", stdout.String())
	}
	if !strings.Contains(stdout.String(), "install [<package>] [flags]") {
		t.Fatalf("stdout = %q, want command list", stdout.String())
	}
	if !strings.Contains(stdout.String(), "auth <command> [flags]") {
		t.Fatalf("stdout = %q, want grouped auth command", stdout.String())
	}
	for _, unwanted := range []string{
		"auth status [<target>] [flags]",
		"auth oauth2 login <target> [flags]",
		"auth secret set <target> [flags]",
	} {
		if strings.Contains(stdout.String(), unwanted) {
			t.Fatalf("stdout = %q, did not want expanded command %q", stdout.String(), unwanted)
		}
	}
}

func TestRunDaemonStopCommand(t *testing.T) {
	prev := daemonStopAll
	var called bool
	daemonStopAll = func(func(string, ...any)) ([]int, error) {
		called = true
		return nil, nil
	}
	defer func() {
		daemonStopAll = prev
	}()

	var stdout, stderr bytes.Buffer
	if err := run([]string{"daemon", "stop"}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	if !called {
		t.Fatal("daemon stop handler was not called")
	}
	if got := stdout.String(); got != "no running toolbox daemons\n" {
		t.Fatalf("stdout = %q, want %q", got, "no running toolbox daemons\n")
	}
}

func TestRunInternalDaemonStopCommand(t *testing.T) {
	prev := daemonStopAll
	var called bool
	daemonStopAll = func(func(string, ...any)) ([]int, error) {
		called = true
		return []int{55}, nil
	}
	defer func() {
		daemonStopAll = prev
	}()

	var stdout, stderr bytes.Buffer
	if err := run([]string{"_daemon", "stop"}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	if !called {
		t.Fatal("_daemon stop handler was not called")
	}
	if got := stdout.String(); got != "stopped toolbox daemons: 55\n" {
		t.Fatalf("stdout = %q, want %q", got, "stopped toolbox daemons: 55\n")
	}
}

func TestInstallHelpDescribesPackageModes(t *testing.T) {
	help := installCmd{}.Help()
	for _, want := range []string{
		"Without <package>, install resolves the selected toolset and updates its lockfile.",
		"Use <module> to install the latest available version.",
		"Use <module>@<version> to install that exact version.",
		"Go pseudo-version directly",
		"v0.0.0-20260410153000-abcdef123456",
		"install creates a default empty",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("help = %q, want substring %q", help, want)
		}
	}
}

func TestToolRegistryConfigFromEnv(t *testing.T) {
	t.Run("empty uses hosted default", func(t *testing.T) {
		t.Setenv("TOOLBOX_REGISTRY", "")
		got, enabled, err := toolRegistryConfigFromEnv()
		if err != nil {
			t.Fatalf("toolRegistryConfigFromEnv() error: %v", err)
		}
		if !enabled {
			t.Fatal("enabled = false, want true")
		}
		if got != defaultToolRegistryBaseURL {
			t.Fatalf("baseURL = %q, want %q", got, defaultToolRegistryBaseURL)
		}
	})

	t.Run("off disables registry", func(t *testing.T) {
		t.Setenv("TOOLBOX_REGISTRY", "off")
		got, enabled, err := toolRegistryConfigFromEnv()
		if err != nil {
			t.Fatalf("toolRegistryConfigFromEnv() error: %v", err)
		}
		if enabled {
			t.Fatal("enabled = true, want false")
		}
		if got != "" {
			t.Fatalf("baseURL = %q, want empty", got)
		}
	})

	t.Run("host only is normalized to https", func(t *testing.T) {
		t.Setenv("TOOLBOX_REGISTRY", "packages.internal.example")
		got, enabled, err := toolRegistryConfigFromEnv()
		if err != nil {
			t.Fatalf("toolRegistryConfigFromEnv() error: %v", err)
		}
		if !enabled {
			t.Fatal("enabled = false, want true")
		}
		if got != "https://packages.internal.example" {
			t.Fatalf("baseURL = %q, want %q", got, "https://packages.internal.example")
		}
	})

	t.Run("invalid URL fails", func(t *testing.T) {
		t.Setenv("TOOLBOX_REGISTRY", "https://")
		_, _, err := toolRegistryConfigFromEnv()
		if err == nil {
			t.Fatal("toolRegistryConfigFromEnv() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "invalid TOOLBOX_REGISTRY") {
			t.Fatalf("error = %v, want TOOLBOX_REGISTRY context", err)
		}
	})
}

func TestRunVersionsUsesGitHubTokenAuthorizationWithoutLeakingIt(t *testing.T) {
	const token = "ghp-secret-token-for-cli-test"

	var registryMu sync.Mutex
	var registryAuthHeaders []string
	registryTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registryMu.Lock()
		registryAuthHeaders = append(registryAuthHeaders, r.Header.Get("Authorization"))
		registryMu.Unlock()

		switch r.URL.Path {
		case "/v1/packages/github.com/admin/stub/@v/list":
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer registryTS.Close()

	var githubMu sync.Mutex
	var githubAuthHeaders []string
	githubTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		githubMu.Lock()
		githubAuthHeaders = append(githubAuthHeaders, r.Header.Get("Authorization"))
		githubMu.Unlock()

		switch r.URL.Path {
		case "/repos/admin/stub/releases":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "tag_name": "v1.0.0"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer githubTS.Close()

	t.Setenv("TOOLBOX_REGISTRY", registryTS.URL)
	t.Setenv("GITHUB_BASE_URL", githubTS.URL)
	t.Setenv("GITHUB_TOKEN", token)
	t.Setenv("TOOLBOX_CACHE_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := run([]string{"versions", "--source", "registry", "github.com/admin/stub"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.Fields(stdout.String()); !reflect.DeepEqual(got, []string{"v1.0.0"}) {
		t.Fatalf("versions output = %#v, want %#v", got, []string{"v1.0.0"})
	}

	registryMu.Lock()
	gotRegistryHeaders := append([]string(nil), registryAuthHeaders...)
	registryMu.Unlock()
	for _, header := range gotRegistryHeaders {
		if header != "" {
			t.Fatalf("registry Authorization headers = %#v, want no credentials", gotRegistryHeaders)
		}
	}

	githubMu.Lock()
	gotGitHubHeaders := append([]string(nil), githubAuthHeaders...)
	githubMu.Unlock()
	if !reflect.DeepEqual(gotGitHubHeaders, []string{"token " + token}) {
		t.Fatalf("GitHub Authorization headers = %#v, want %#v", gotGitHubHeaders, []string{"token " + token})
	}
	assertNoTokenLeak(t, token, stdout.String(), stderr.String())
}

func TestRunVersionsRejectsInvalidModuleArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"versions", "admin"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run() error = nil, want invalid module path error")
	}
	if !strings.Contains(err.Error(), `target "admin" is not a module path`) {
		t.Fatalf("error = %v, want target resolution context", err)
	}
}

func TestRunVersionsLocalSourceSkipsResolverSetupForModuleTarget(t *testing.T) {
	archiveBytes, manifestBytes := packSourceFixtureBytesWithModule(t, "calc", "github.com/admin/stub")
	cacheDir := t.TempDir()
	cache, err := registry.NewCache(cacheDir)
	if err != nil {
		t.Fatalf("NewCache(%q): %v", cacheDir, err)
	}
	if err := cache.Put(
		registry.ModulePath("github.com/admin/stub"),
		registry.Version("v1.0.0"),
		archiveBytes,
		manifestBytes,
	); err != nil {
		t.Fatalf("cache.Put(): %v", err)
	}

	t.Setenv("TOOLBOX_CACHE_DIR", cacheDir)
	t.Setenv("TOOLBOX_REGISTRY", "https://")

	var stdout, stderr bytes.Buffer
	err = run([]string{"versions", "--source", "local", "github.com/admin/stub"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.Fields(stdout.String()); !reflect.DeepEqual(got, []string{"v1.0.0"}) {
		t.Fatalf("versions output = %#v, want %#v", got, []string{"v1.0.0"})
	}
}

func TestRunInfoJSONForLocalDir(t *testing.T) {
	packageDir := filepath.Join(t.TempDir(), "calc")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", packageDir, err)
	}
	copyFixtureDir(t, loadSourceFixtureDir(t, "calc"), packageDir)
	rewriteSourceFixtureModule(t, packageDir, "example.com/acme/calc")

	var stdout, stderr bytes.Buffer
	err := run([]string{"info", "--json", packageDir}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}

	var got packageInfoView
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal(stdout): %v\nstdout=%s", err, stdout.String())
	}
	if got.Source != "local-dir" {
		t.Fatalf("source = %q, want local-dir", got.Source)
	}
	if got.Package.Module != "example.com/acme/calc" {
		t.Fatalf("module = %q, want example.com/acme/calc", got.Package.Module)
	}
	if got.Package.Name != "calc" {
		t.Fatalf("name = %q, want calc", got.Package.Name)
	}
}

func TestRunInfoJSONForExplicitPackageVersionReportsActualProvenance(t *testing.T) {
	archiveBytes, manifestBytes := packSourceFixtureBytesWithModule(t, "calc", "github.com/admin/stub")
	const commitSHA = "cccccccccccccccccccccccccccccccccccccccc"
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

	t.Setenv("TOOLBOX_REGISTRY", "off")
	t.Setenv("GITHUB_BASE_URL", ts.URL)
	t.Setenv("TOOLBOX_CACHE_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := run([]string{"info", "--json", "github.com/admin/stub@v1.0.0"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}

	var got packageInfoView
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal(stdout): %v\nstdout=%s", err, stdout.String())
	}
	if got.Source != "github-release" {
		t.Fatalf("source = %q, want github-release", got.Source)
	}
	if got.Version != "v1.0.0" {
		t.Fatalf("version = %q, want v1.0.0", got.Version)
	}
}

func TestRunOutdatedReportsNewerPublishedVersion(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/admin/stub/releases":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 2, "tag_name": "v1.2.0"},
				{"id": 1, "tag_name": "v1.0.0"},
			})
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
	err := run([]string{"outdated", "--toolset", toolsetPath}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "MODULE") || !strings.Contains(stdout.String(), "CURRENT") || !strings.Contains(stdout.String(), "LATEST") {
		t.Fatalf("stdout = %q, want table header", stdout.String())
	}
	if !strings.Contains(stdout.String(), "github.com/admin/stub") || !strings.Contains(stdout.String(), "v1.2.0") {
		t.Fatalf("stdout = %q, want outdated row", stdout.String())
	}
}

func TestRunSDKBridgeServeStdio(t *testing.T) {
	calls := stubSessionDaemon(t)

	var stdout, stderr bytes.Buffer
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":\"req-1\",\"method\":\"system.version\"}\n")

	if err := runWithIO([]string{"_sdkbridge", "serve-stdio"}, input, &stdout, &stderr); err != nil {
		t.Fatalf("runWithIO() error: %v\nstderr=%s", err, stderr.String())
	}

	var resp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Result  struct {
			Version string `json:"version"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
		t.Fatalf("json.Unmarshal(stdout): %v\nstdout=%s", err, stdout.String())
	}
	if resp.JSONRPC != "2.0" || resp.ID != "req-1" {
		t.Fatalf("response = %#v, want jsonrpc=2.0 id=req-1", resp)
	}
	if resp.Result.Version == "" {
		t.Fatalf("version result = %#v, want non-empty version", resp.Result)
	}
	if calls.Load() != 1 {
		t.Fatalf("ensureSessionDaemon() calls = %d, want 1", calls.Load())
	}
}

func TestRunSDKBridgeServeStdioBindsSecretEpochReload(t *testing.T) {
	prevEnsure := ensureSessionDaemon
	prevNewBridge := newSDKBridgeServer
	delegate := &recordingSessionDaemon{}
	bridge := &recordingSDKBridgeServer{}
	ensureSessionDaemon = func(string, string, io.Writer) (daemon.SessionDelegate, error) {
		return delegate, nil
	}
	newSDKBridgeServer = func(sdkbridge.Options) sdkBridgeServer {
		return bridge
	}
	t.Cleanup(func() {
		ensureSessionDaemon = prevEnsure
		newSDKBridgeServer = prevNewBridge
	})

	var stdout, stderr bytes.Buffer
	if err := runSDKBridgeServeStdio(secretStoreOptions{}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("runSDKBridgeServeStdio() error: %v", err)
	}

	delegate.fire()
	waitForAtomic(t, &bridge.reloads, 1)
}

type recordingSDKBridgeServer struct {
	reloads atomic.Int32
}

func (s *recordingSDKBridgeServer) ReloadFileBackedToolsets(context.Context) error {
	s.reloads.Add(1)
	return nil
}

func (*recordingSDKBridgeServer) ServeStdio(context.Context, io.Reader, io.Writer) error {
	return nil
}

func TestRunInstallWritesLockfile(t *testing.T) {
	archiveBytes, manifestBytes := packSourceFixtureBytesWithModule(t, "calc", "github.com/admin/stub")
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
	err := run([]string{"install", "--toolset", toolsetPath}, &stdout, &stderr)
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

func TestRunInstallWritesToolRegistryLockfileProvenance(t *testing.T) {
	archiveBytes, manifestBytes := packSourceFixtureBytesWithModule(t, "calc", "github.com/admin/stub")
	const commitSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	registryTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/packages/github.com/admin/stub/@v/v1.0.0.info":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"module":         "github.com/admin/stub",
				"version":        "v1.0.0",
				"archive_sha256": sha256HexForTest(archiveBytes),
				"git_sha":        commitSHA,
				"manifest_json":  string(manifestBytes),
			})
		case "/v1/packages/github.com/admin/stub/@v/v1.0.0.pkg":
			http.Redirect(w, r, "/assets/calc.toolbox.pkg", http.StatusFound)
		case "/v1/packages/github.com/admin/stub/@v/v1.0.0.manifest":
			http.Redirect(w, r, "/assets/toolbox.pkg.json", http.StatusFound)
		case "/assets/calc.toolbox.pkg":
			_, _ = w.Write(archiveBytes)
		case "/assets/toolbox.pkg.json":
			_, _ = w.Write(manifestBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer registryTS.Close()

	toolsetPath := writeToolsetFile(t, map[string]any{
		"packages": map[string]string{"github.com/admin/stub": "v1.0.0"},
		"tools":    []map[string]string{{"tool": "github.com/admin/stub@v1.0.0/calc.add"}},
	})

	t.Setenv("TOOLBOX_REGISTRY", registryTS.URL)
	t.Setenv("GITHUB_BASE_URL", "http://127.0.0.1:1")
	t.Setenv("TOOLBOX_CACHE_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := run([]string{"install", "--toolset", toolsetPath}, &stdout, &stderr)
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
	if entry.ResolvedFrom != toolsetfile.ToolsetLockResolvedFromToolRegistry {
		t.Fatalf("resolved_from = %q, want %q", entry.ResolvedFrom, toolsetfile.ToolsetLockResolvedFromToolRegistry)
	}
}

func TestRunInstallAddsLatestDeclaredPackageAndCreatesToolsetFile(t *testing.T) {
	archiveBytes, manifestBytes := packSourceFixtureBytesWithModule(t, "calc", "github.com/admin/stub")
	const commitSHA = "dddddddddddddddddddddddddddddddddddddddd"
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

	toolsetPath := filepath.Join(t.TempDir(), defaultToolsetFilename)

	t.Setenv("GITHUB_BASE_URL", ts.URL)
	t.Setenv("TOOLBOX_CACHE_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := run([]string{"install", "--toolset", toolsetPath, "github.com/admin/stub"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "created toolset file: "+toolsetPath) {
		t.Fatalf("stdout = %q, want created toolset line", stdout.String())
	}
	if !strings.Contains(stdout.String(), "installed github.com/admin/stub@v1.2.0") {
		t.Fatalf("stdout = %q, want installed package line", stdout.String())
	}
	if !strings.Contains(stdout.String(), "resolved 3 tools") {
		t.Fatalf("stdout = %q, want resolved tool count", stdout.String())
	}

	file, err := toolsetfile.Load(toolsetPath)
	if err != nil {
		t.Fatalf("Load(%q): %v", toolsetPath, err)
	}
	if got := file.Packages["github.com/admin/stub"]; got != "v1.2.0" {
		t.Fatalf("packages[stub] = %q, want v1.2.0", got)
	}
	if len(file.Tools) != 0 {
		t.Fatalf("len(tools) = %d, want 0", len(file.Tools))
	}

	lock, err := toolsetfile.LoadLock(strings.TrimSuffix(toolsetPath, ".json") + ".lock")
	if err != nil {
		t.Fatalf("LoadLock(): %v", err)
	}
	if _, ok := lock.Packages["github.com/admin/stub@v1.2.0"]; !ok {
		t.Fatalf("lock packages = %#v, want github.com/admin/stub@v1.2.0", lock.Packages)
	}
}

func TestRunInstallExplicitVersionRewritesDeclaredPackageAndTools(t *testing.T) {
	archiveBytes, manifestBytes := packSourceFixtureBytesWithModule(t, "calc", "github.com/admin/stub")
	const commitSHA = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
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
	err := run([]string{"install", "--toolset", toolsetPath, "github.com/admin/stub@v1.2.0"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "installed github.com/admin/stub: v1.0.0 -> v1.2.0") {
		t.Fatalf("stdout = %q, want installed update line", stdout.String())
	}
	if !strings.Contains(stdout.String(), "resolved 3 tools") {
		t.Fatalf("stdout = %q, want resolved tool count", stdout.String())
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
}

func TestRunInstallUsesCommittedLockCacheHitWithoutRefetch(t *testing.T) {
	archiveBytes, manifestBytes := packSourceFixtureBytesWithModule(t, "calc", "github.com/admin/stub")
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
	if err := run([]string{"install", "--toolset", toolsetPath}, &stdout, &stderr); err != nil {
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
		t.Fatal("first install made no HTTP requests, want initial fetch before cache-hit verification")
	}

	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"install", "--toolset", toolsetPath}, &stdout, &stderr); err != nil {
		t.Fatalf("second run() error: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "resolved 3 tools") {
		t.Fatalf("second install stdout = %q, want resolved tool count", stdout.String())
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
		t.Fatalf("HTTP requests changed on second install: got %#v, want %#v", secondRunRequests, firstRunRequests)
	}
}

func TestRunInstallSiblingLocalOverlayPreservesLockfile(t *testing.T) {
	archiveBytes, manifestBytes := packSourceFixtureBytesWithModule(t, "calc", "example.com/acme/calc")
	githubIssuesDir := filepath.Join(t.TempDir(), "github-issues")
	if err := os.MkdirAll(githubIssuesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", githubIssuesDir, err)
	}
	copyFixtureDir(t, loadSourceFixtureDir(t, "github-issues"), githubIssuesDir)
	rewriteSourceFixtureModule(t, githubIssuesDir, "example.com/zeta/github-issues")

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
	err = run([]string{"install", "--toolset", toolsetPath}, &stdout, &stderr)
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

func TestRunMCPLoadsLocalOverlayToolsetAndServesTools(t *testing.T) {
	calls := stubSessionDaemon(t)

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
	rewriteSourceFixtureModule(t, packageDir, "example.com/acme/calc")

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
			[]string{"mcp", "--toolset", toolsetPath},
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "toolbox-cli-test", Version: "1.0.0"}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}
	initRes, err := c.Initialize(ctx, initReq)
	if err != nil {
		t.Fatalf("Initialize(): %v", err)
	}
	if initRes.ServerInfo.Name != "toolbox" {
		t.Fatalf("server name = %q, want toolbox", initRes.ServerInfo.Name)
	}

	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools(): %v", err)
	}
	if len(tools.Tools) != 5 {
		t.Fatalf("len(tools) = %d, want 5", len(tools.Tools))
	}
	if !hasToolNamed(tools.Tools, "calc.add") {
		t.Fatalf("tools = %#v, want calc.add", tools.Tools)
	}
	if !hasToolNamed(tools.Tools, "toolbox.search") {
		t.Fatalf("tools = %#v, want toolbox.search", tools.Tools)
	}
	if !hasToolNamed(tools.Tools, "toolbox.inspect") {
		t.Fatalf("tools = %#v, want toolbox.inspect", tools.Tools)
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
			t.Fatalf("mcp exited with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for mcp to exit")
	}
	if calls.Load() != 1 {
		t.Fatalf("ensureSessionDaemon() calls = %d, want 1", calls.Load())
	}
	logPath, err := latestMCPDebugLogPath()
	if err != nil {
		t.Fatalf("latestMCPDebugLogPath(): %v", err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", logPath, err)
	}
	logText := string(logBytes)
	if !strings.Contains(logText, "start kind=mcp") || !strings.Contains(logText, "exit kind=mcp") {
		t.Fatalf("mcp debug log missing lifecycle markers:\n%s", logText)
	}
}

func TestRunCodemodeMCPServesSuperTool(t *testing.T) {
	calls := stubSessionDaemon(t)

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
	rewriteSourceFixtureModule(t, packageDir, "example.com/acme/calc")

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
			[]string{"codemode", "mcp", "--toolset", toolsetPath},
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "toolbox-cli-test", Version: "1.0.0"}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}
	initRes, err := c.Initialize(ctx, initReq)
	if err != nil {
		t.Fatalf("Initialize(): %v", err)
	}
	if initRes.ServerInfo.Name != "toolbox" {
		t.Fatalf("server name = %q, want toolbox", initRes.ServerInfo.Name)
	}

	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools(): %v", err)
	}
	if len(tools.Tools) != 2 {
		t.Fatalf("len(tools) = %d, want 2", len(tools.Tools))
	}
	if !hasToolNamed(tools.Tools, "super_tool") {
		t.Fatalf("tools = %#v, want super_tool", tools.Tools)
	}
	if !hasToolNamed(tools.Tools, "new_super_tool_session") {
		t.Fatalf("tools = %#v, want new_super_tool_session", tools.Tools)
	}

	newReq := mcp.CallToolRequest{}
	newReq.Params.Name = "new_super_tool_session"
	newReq.Params.Arguments = map[string]any{
		codemodesession.IntentParam: "test intent",
	}
	newSessionResult, err := c.CallTool(ctx, newReq)
	if err != nil {
		t.Fatalf("CallTool(new_super_tool_session): %v", err)
	}
	newSessionText, ok := mcp.AsTextContent(newSessionResult.Content[0])
	if !ok {
		t.Fatalf("new_super_tool_session first content item = %#v, want text content", newSessionResult.Content[0])
	}
	tbSession := strings.TrimSpace(strings.SplitN(newSessionText.Text, "\n", 2)[0])

	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = "super_tool"
	callReq.Params.Arguments = map[string]any{
		"tb_session":             tbSession,
		"typescript_cell_source": "Object.keys($pkgMetadata).sort().join(',')",
	}
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
	if !strings.Contains(text.Text, "calc") {
		t.Fatalf("text content = %q, want calc metadata", text.Text)
	}

	_ = stdio.Close()
	_ = clientWrite.Close()
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("mcp exited with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for mcp to exit")
	}
	if calls.Load() != 1 {
		t.Fatalf("ensureSessionDaemon() calls = %d, want 1", calls.Load())
	}
	logPath, err := latestMCPDebugLogPath()
	if err != nil {
		t.Fatalf("latestMCPDebugLogPath(): %v", err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", logPath, err)
	}
	logText := string(logBytes)
	if !strings.Contains(logText, "start kind=codemode_mcp") || !strings.Contains(logText, "exit kind=codemode_mcp") {
		t.Fatalf("codemode mcp debug log missing lifecycle markers:\n%s", logText)
	}
}

func TestMCPServerNameForToolsetPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "toolbox.toolset.json", want: "toolbox"},
		{path: filepath.Join("tmp", "toolbox.toolset.json"), want: "toolbox"},
		{path: "support-agent.toolset.json", want: "support-agent"},
		{path: "support-agent.json", want: "support-agent"},
		{path: "support-agent.toolset", want: "support-agent"},
		{path: "", want: "toolbox"},
	}

	for _, tt := range tests {
		if got := mcpServerNameForToolsetPath(tt.path); got != tt.want {
			t.Fatalf("mcpServerNameForToolsetPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestBuildPackageCredentialPolicies_IsolatesLoadedPackages_EndToEnd(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	pkgADir := filepath.Join(workspace, "pkg-a")
	pkgBDir := filepath.Join(workspace, "pkg-b")
	if err := os.MkdirAll(filepath.Join(pkgADir, "tools"), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", pkgADir, err)
	}
	if err := os.MkdirAll(filepath.Join(pkgBDir, "tools"), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", pkgBDir, err)
	}

	writeJSONFile(t, filepath.Join(pkgADir, "toolbox.devpkg.json"), map[string]any{
		"module":        "example.com/acme/pkg-a",
		"name":          "pkg-a",
		"runtime":       "typescript-sandbox",
		"allowed_hosts": []string{"127.0.0.1"},
		"tools": []map[string]any{{
			"entry_ts":   "tools/pkg-a.get.ts",
			"idempotent": true,
			"effect":     "readOnly",
		}},
	})
	if err := os.WriteFile(filepath.Join(pkgADir, "tools", "pkg-a.get.ts"), []byte(`export default async function tool(url: string): Promise<string> {
  const response = await fetch(url);
  return await response.text();
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(pkg-a tool): %v", err)
	}

	writeJSONFile(t, filepath.Join(pkgBDir, "toolbox.devpkg.json"), map[string]any{
		"module":  "example.com/acme/pkg-b",
		"name":    "pkg-b",
		"runtime": "typescript-sandbox",
		"credentials": []map[string]any{{
			"name": "default",
			"type": "api_key",
			"inject": map[string]any{
				"hosts":       []string{"localhost"},
				"method":      "api_key_header",
				"header_name": "X-Pkg-B-Key",
				"path_prefix": "/api/",
			},
		}},
		"allowed_hosts": []string{"localhost"},
		"tools": []map[string]any{{
			"entry_ts":   "tools/pkg-b.get.ts",
			"idempotent": true,
			"effect":     "readOnly",
		}},
	})
	if err := os.WriteFile(filepath.Join(pkgBDir, "tools", "pkg-b.get.ts"), []byte(`export default async function tool(url: string): Promise<string> {
  const response = await fetch(url);
  return await response.text();
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(pkg-b tool): %v", err)
	}

	loaded, err := assembler.Load(context.Background(), nil, assembler.Declaration{
		Packages: []assembler.PackageDeclaration{
			{
				Module:     "example.com/acme/pkg-a",
				Version:    "v1.0.0",
				ReplaceDir: pkgADir,
			},
			{
				Module:     "example.com/acme/pkg-b",
				Version:    "v1.0.0",
				ReplaceDir: pkgBDir,
			},
		},
	})
	if err != nil {
		t.Fatalf("assembler.Load: %v", err)
	}

	repo := testutil.NewTestCredentialRepo()
	repo.Seed(map[string][]byte{
		credpath.Shared("example.com/acme/pkg-b", "default", "api_key"): []byte("LEAKED-IF-MERGED"),
	})

	prepared, err := toolset.PrepareTools(context.Background(), loaded.Tools(), toolset.Config{
		CredentialPolicySource: repo,
	})
	if err != nil {
		t.Fatalf("PrepareTools(final): %v", err)
	}

	var requestCount atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("unexpected upstream hit"))
	}))
	t.Cleanup(upstream.Close)

	blockedURL := strings.Replace(upstream.URL, "127.0.0.1", "localhost", 1) + "/api/blocked"

	args, err := jswire.Encode(map[string]any{
		"url": blockedURL,
	})
	if err != nil {
		t.Fatalf("encode args: %v", err)
	}
	_, err = invoke.Run(prepared, "pkgA.get", args)
	if err == nil {
		t.Fatal("expected allowlist error, got nil")
	}
	if !strings.Contains(err.Error(), "not in allowlist") {
		t.Fatalf("error = %v, want allowlist failure", err)
	}
	if got := requestCount.Load(); got != 0 {
		t.Fatalf("upstream request count = %d, want 0", got)
	}
}

func TestRunUpdateRejectsUnknownTargetArgument(t *testing.T) {
	toolsetPath := writeToolsetFile(t, map[string]any{
		"packages": map[string]string{"github.com/admin/stub": "v1.0.0"},
		"tools":    []map[string]string{{"tool": "github.com/admin/stub@v1.0.0/calc.add"}},
	})

	var stdout, stderr bytes.Buffer
	err := run([]string{"update", "--toolset", toolsetPath, "github.com/admin/missing"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run() error = nil, want unknown target error")
	}
	if !strings.Contains(err.Error(), `target "github.com/admin/missing" is not declared`) {
		t.Fatalf("error = %v, want unknown target context", err)
	}
}

func TestRunInstallMalformedLocalOverlayKeepsValidationContext(t *testing.T) {
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
	err = run([]string{"install", "--toolset", toolsetPath}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run() error = nil, want malformed local overlay error")
	}
	assertErrorContains(t, err, "prepare toolset file")
	assertErrorContains(t, err, "schema-validate toolset local file")
	assertErrorContains(t, err, file.LocalFilename())
}

func TestRunUpdateRewritesToolsetAndLockfile(t *testing.T) {
	archiveBytes, manifestBytes := packSourceFixtureBytesWithModule(t, "calc", "github.com/admin/stub")
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
	err := run([]string{"update", "--toolset", toolsetPath, "github.com/admin/stub"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "updated github.com/admin/stub: v1.0.0 -> v1.2.0") {
		t.Fatalf("stdout = %q, want update message", stdout.String())
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

func packSourceFixtureBytesWithModule(t *testing.T, fixtureName, module string) ([]byte, []byte) {
	t.Helper()

	workDir := filepath.Join(t.TempDir(), fixtureName)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", workDir, err)
	}
	copyFixtureDir(t, loadSourceFixtureDir(t, fixtureName), workDir)
	rewriteSourceFixtureModule(t, workDir, module)

	outDir := t.TempDir()
	result, err := packaging.Pack(workDir, outDir, tooldef.Version("v1.0.0"))
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

func rewriteSourceFixtureModule(t *testing.T, dir, module string) {
	t.Helper()

	manifestPath := filepath.Join(dir, packaging.DevManifestFilename)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", manifestPath, err)
	}

	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", manifestPath, err)
	}
	manifest["module"] = module
	writeJSONFile(t, manifestPath, manifest)
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
