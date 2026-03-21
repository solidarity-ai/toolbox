package tooltest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	tswasixcli "github.com/solidarity-ai/toolbox/runtime/tswasixcli"
)

var buildOnce sync.Once
var buildErr error

// EnsureSandboxBinary builds the wasixcli-sandbox binary if it doesn't exist.
// Safe to call from multiple tests concurrently — only builds once.
func EnsureSandboxBinary(t testing.TB) {
	t.Helper()

	binaryPath := tswasixcli.ResolveHostBinaryPathForTest()
	if _, err := os.Stat(binaryPath); err == nil {
		return // already built
	}

	cargoManifest := sandboxCargoPath()
	if _, err := os.Stat(cargoManifest); err != nil {
		t.Fatalf("wasixcli-sandbox Cargo.toml not found at %s", cargoManifest)
	}

	buildOnce.Do(func() {
		cmd := exec.Command("cargo", "build", "--manifest-path", cargoManifest)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		buildErr = cmd.Run()
	})
	if buildErr != nil {
		t.Fatalf("failed to build wasixcli-sandbox: %v", buildErr)
	}
}

func sandboxCargoPath() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("tooltest: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "wasixcli-sandbox", "Cargo.toml")
}
