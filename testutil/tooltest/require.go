package tooltest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/solidarity-ai/toolbox/runtime/tswasmcli"
)

var buildOnce sync.Once
var buildErr error

// EnsureSandboxBinary builds the wasmcli-sandbox binary if it doesn't exist.
// Safe to call from multiple tests concurrently — only builds once.
func EnsureSandboxBinary(t testing.TB) {
	t.Helper()

	binaryPath := tswasmcli.ResolveHostBinaryPathForTest()
	if _, err := os.Stat(binaryPath); err == nil {
		return // already built
	}

	cargoManifest := sandboxCargoPath()
	if _, err := os.Stat(cargoManifest); err != nil {
		t.Fatalf("wasmcli-sandbox Cargo.toml not found at %s", cargoManifest)
	}

	buildOnce.Do(func() {
		cmd := exec.Command("cargo", "build", "--manifest-path", cargoManifest)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		buildErr = cmd.Run()
	})
	if buildErr != nil {
		t.Fatalf("failed to build wasmcli-sandbox: %v", buildErr)
	}
}

func sandboxCargoPath() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("tooltest: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "wasmcli-sandbox", "Cargo.toml")
}
