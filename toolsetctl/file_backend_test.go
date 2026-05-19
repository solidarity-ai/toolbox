package toolsetctl_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/testutil/fixtures"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetctl"
)

func TestFileBackendLoadsPreparedToolsAndBuiltins(t *testing.T) {
	path := writeLocalToolsetFile(t, "calc", true)
	consumer := &recordingPreparedToolConsumer{}

	backend, err := toolsetctl.NewFileBackend(context.Background(), toolsetctl.FileBackendOptions{
		ToolsetPath: path,
		Consumer:    consumer,
	})
	if err != nil {
		t.Fatalf("NewFileBackend(): %v", err)
	}

	prepared, err := backend.Prepared(context.Background())
	if err != nil {
		t.Fatalf("Prepared(): %v", err)
	}

	names := preparedToolNames(prepared.Tools())
	if !slices.Contains(names, "calc.add") {
		t.Fatalf("Prepared() tools = %v, want calc.add", names)
	}
	if !slices.Contains(names, "toolbox.search") {
		t.Fatalf("Prepared() tools = %v, want toolbox.search", names)
	}
	if !slices.Contains(names, "toolbox.inspect") {
		t.Fatalf("Prepared() tools = %v, want toolbox.inspect", names)
	}
	if !slices.Contains(names, "toolbox.install") {
		t.Fatalf("Prepared() tools = %v, want toolbox.install", names)
	}
	if !slices.Contains(names, "toolbox.uninstall") {
		t.Fatalf("Prepared() tools = %v, want toolbox.uninstall", names)
	}
	if !slices.Contains(names, "toolbox.auth") {
		t.Fatalf("Prepared() tools = %v, want toolbox.auth", names)
	}
	if consumer.callCount != 1 {
		t.Fatalf("consumer call count = %d, want 1", consumer.callCount)
	}
}

func TestFileBackendAllowedEffectsFiltersFinalSurface(t *testing.T) {
	path := writeLocalToolsetFile(t, "calc", true)

	backend, err := toolsetctl.NewFileBackend(context.Background(), toolsetctl.FileBackendOptions{
		ToolsetPath: path,
		AllowedEffects: map[tooldef.Effect]bool{
			tooldef.EffectReadOnly: true,
		},
	})
	if err != nil {
		t.Fatalf("NewFileBackend(): %v", err)
	}

	prepared, err := backend.Prepared(context.Background())
	if err != nil {
		t.Fatalf("Prepared(): %v", err)
	}

	names := preparedToolNames(prepared.Tools())
	if !slices.Contains(names, "calc.add") {
		t.Fatalf("Prepared() tools = %v, want calc.add", names)
	}
	if !slices.Contains(names, "toolbox.search") {
		t.Fatalf("Prepared() tools = %v, want toolbox.search", names)
	}
	if !slices.Contains(names, "toolbox.inspect") {
		t.Fatalf("Prepared() tools = %v, want toolbox.inspect", names)
	}
	if slices.Contains(names, "toolbox.install") {
		t.Fatalf("Prepared() tools = %v, do not want toolbox.install", names)
	}
	if slices.Contains(names, "toolbox.uninstall") {
		t.Fatalf("Prepared() tools = %v, do not want toolbox.uninstall", names)
	}
	if slices.Contains(names, "toolbox.auth") {
		t.Fatalf("Prepared() tools = %v, do not want toolbox.auth", names)
	}
}

func TestFileBackendUninstallUpdatesConsumerAndToolsetFile(t *testing.T) {
	path := writeLocalToolsetFile(t, "calc", true)
	consumer := &recordingPreparedToolConsumer{}

	backend, err := toolsetctl.NewFileBackend(context.Background(), toolsetctl.FileBackendOptions{
		ToolsetPath: path,
		Consumer:    consumer,
	})
	if err != nil {
		t.Fatalf("NewFileBackend(): %v", err)
	}

	prepared, err := backend.Uninstall(context.Background(), toolsetctl.UninstallRequest{Target: "calc"})
	if err != nil {
		t.Fatalf("Uninstall(): %v", err)
	}

	names := preparedToolNames(prepared.Tools())
	if slices.Contains(names, "calc.add") {
		t.Fatalf("Prepared() tools = %v, do not want calc.add after uninstall", names)
	}
	if !slices.Contains(names, "toolbox.search") {
		t.Fatalf("Prepared() tools = %v, want toolbox.search after uninstall", names)
	}
	if consumer.callCount != 2 {
		t.Fatalf("consumer call count = %d, want 2 after uninstall", consumer.callCount)
	}
	if slices.Contains(consumer.toolNames(), "calc.add") {
		t.Fatalf("consumer tools = %v, do not want calc.add after uninstall", consumer.toolNames())
	}

	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	var decoded struct {
		Packages map[string]string `json:"packages"`
	}
	if err := json.Unmarshal(file, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(toolset): %v", err)
	}
	if len(decoded.Packages) != 0 {
		t.Fatalf("packages = %#v, want empty after uninstall", decoded.Packages)
	}
}

func TestFileBackendNotifiesConsumerAfterUnlock(t *testing.T) {
	path := writeLocalToolsetFile(t, "calc", true)
	consumer := &reentrantPreparedToolConsumer{}

	backend, err := toolsetctl.NewFileBackend(context.Background(), toolsetctl.FileBackendOptions{
		ToolsetPath: path,
		Consumer:    consumer,
	})
	if err != nil {
		t.Fatalf("NewFileBackend(): %v", err)
	}
	consumer.backend = backend

	done := make(chan error, 1)
	go func() {
		_, err := backend.Uninstall(context.Background(), toolsetctl.UninstallRequest{Target: "calc"})
		if err != nil {
			done <- err
			return
		}
		done <- consumer.err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Uninstall(): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("consumer callback deadlocked while calling back into FileBackend")
	}
}

func preparedToolNames(tools []toolset.PreparedTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func writeLocalToolsetFile(t *testing.T, fixtureName string, allowToolsetManagement bool) string {
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

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%T): %v", value, err)
	}
	return data
}

type reentrantPreparedToolConsumer struct {
	backend *toolsetctl.FileBackend
	err     error
}

func (c *reentrantPreparedToolConsumer) SetPreparedTools(toolset.PreparedToolset) {
	if c.backend == nil {
		return
	}
	_, c.err = c.backend.Prepared(context.Background())
}
