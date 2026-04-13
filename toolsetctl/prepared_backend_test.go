package toolsetctl_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetctl"
)

func TestPreparedBackendDisablesToolsetManagementTools(t *testing.T) {
	backend := toolsetctl.NewPreparedBackend(toolset.PreparedToolset{}, nil)

	if backend.EnableToolsForToolsetManagement() {
		t.Fatal("EnableToolsForToolsetManagement() = true, want false")
	}
}

func TestPreparedBackendPackageDiscoveryDefaultsFalseAndCanBeEnabled(t *testing.T) {
	backend := toolsetctl.NewPreparedBackend(toolset.PreparedToolset{}, nil)
	if backend.EnableToolsForPackageDiscovery() {
		t.Fatal("EnableToolsForPackageDiscovery() = true, want false by default")
	}

	backend.SetEnableToolsForPackageDiscovery(true)
	if !backend.EnableToolsForPackageDiscovery() {
		t.Fatal("EnableToolsForPackageDiscovery() = false, want true after SetEnableToolsForPackageDiscovery")
	}
}

func TestPreparedBackendPreparedReturnsStoredSnapshot(t *testing.T) {
	want := toolset.NewPreparedToolset([]assembler.LoadedTool{
		{Name: "calc.add", PackageMeta: &tooldef.Package{Name: "calc"}},
	})
	backend := toolsetctl.NewPreparedBackend(want, nil)

	got, err := backend.Prepared(context.Background())
	if err != nil {
		t.Fatalf("Prepared() error: %v", err)
	}
	if len(got.Tools()) != len(want.Tools()) {
		t.Fatalf("Prepared() tool count = %d, want %d", len(got.Tools()), len(want.Tools()))
	}
}

func TestPreparedBackendPreparedIncludesPackageDiscoveryToolsWhenEnabled(t *testing.T) {
	base := toolset.NewPreparedToolset([]assembler.LoadedTool{
		{Name: "calc.add", PackageMeta: &tooldef.Package{Name: "calc"}},
	})
	backend := toolsetctl.NewPreparedBackend(base, nil)
	backend.SetEnableToolsForPackageDiscovery(true)

	got, err := backend.Prepared(context.Background())
	if err != nil {
		t.Fatalf("Prepared() error: %v", err)
	}

	var names []string
	for _, tool := range got.Tools() {
		names = append(names, tool.Name)
	}
	if !slices.Contains(names, "calc.add") {
		t.Fatalf("Prepared() tools = %v, want calc.add", names)
	}
	if !slices.Contains(names, "toolbox.search") {
		t.Fatalf("Prepared() tools = %v, want toolbox.search", names)
	}
	if !slices.Contains(names, "toolbox.inspect") {
		t.Fatalf("Prepared() tools = %v, want toolbox.inspect", names)
	}
}

func TestPreparedBackendPreparedIncludesToolsetManagementToolsWhenEnabled(t *testing.T) {
	base := toolset.NewPreparedToolset([]assembler.LoadedTool{
		{Name: "calc.add", PackageMeta: &tooldef.Package{Name: "calc"}},
	})
	backend := toolsetctl.NewPreparedBackend(base, nil)
	backend.SetEnableToolsForToolsetManagement(true)

	got, err := backend.Prepared(context.Background())
	if err != nil {
		t.Fatalf("Prepared() error: %v", err)
	}

	var names []string
	for _, tool := range got.Tools() {
		names = append(names, tool.Name)
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
}

func TestPreparedBackendSetPreparedReplacesStoredSnapshot(t *testing.T) {
	backend := toolsetctl.NewPreparedBackend(toolset.PreparedToolset{}, nil)
	next := toolset.NewPreparedToolset([]assembler.LoadedTool{
		{Name: "calc.add", PackageMeta: &tooldef.Package{Name: "calc"}},
		{Name: "calc.sub", PackageMeta: &tooldef.Package{Name: "calc"}},
	})

	backend.SetPrepared(next)

	got, err := backend.Prepared(context.Background())
	if err != nil {
		t.Fatalf("Prepared() error: %v", err)
	}
	if len(got.Tools()) != 2 {
		t.Fatalf("Prepared() tool count = %d, want 2", len(got.Tools()))
	}
}

func TestPreparedBackendInstallReturnsUnsupportedError(t *testing.T) {
	backend := toolsetctl.NewPreparedBackend(toolset.PreparedToolset{}, nil)

	_, err := backend.Install(context.Background(), toolsetctl.InstallRequest{Package: "example.com/pkg"})
	if err == nil {
		t.Fatal("Install() error = nil, want unsupported error")
	}
	if !errors.Is(err, toolsetctl.ErrManagementToolsUnsupported) {
		t.Fatalf("Install() error = %v, want management unsupported", err)
	}
}

func TestPreparedBackendPushesPreparedToolsToConsumer(t *testing.T) {
	consumer := &recordingPreparedToolConsumer{}
	base := toolset.NewPreparedToolset([]assembler.LoadedTool{
		{Name: "calc.add", PackageMeta: &tooldef.Package{Name: "calc"}},
	})

	backend := toolsetctl.NewPreparedBackend(base, consumer)
	if consumer.callCount != 1 {
		t.Fatalf("consumer call count = %d, want 1 after construction", consumer.callCount)
	}
	if !slices.Equal(consumer.toolNames(), []string{"calc.add"}) {
		t.Fatalf("consumer tools = %v, want [calc.add]", consumer.toolNames())
	}

	backend.SetEnableToolsForPackageDiscovery(true)
	if consumer.callCount != 2 {
		t.Fatalf("consumer call count = %d, want 2 after enabling discovery", consumer.callCount)
	}
	if !slices.Contains(consumer.toolNames(), "toolbox.search") {
		t.Fatalf("consumer tools = %v, want toolbox.search", consumer.toolNames())
	}

	backend.SetEnableToolsForToolsetManagement(true)
	if consumer.callCount != 3 {
		t.Fatalf("consumer call count = %d, want 3 after enabling management", consumer.callCount)
	}
	if !slices.Contains(consumer.toolNames(), "toolbox.install") {
		t.Fatalf("consumer tools = %v, want toolbox.install", consumer.toolNames())
	}

	next := toolset.NewPreparedToolset([]assembler.LoadedTool{
		{Name: "calc.sub", PackageMeta: &tooldef.Package{Name: "calc"}},
	})
	backend.SetPrepared(next)
	if consumer.callCount != 4 {
		t.Fatalf("consumer call count = %d, want 4 after SetPrepared", consumer.callCount)
	}
	if !slices.Contains(consumer.toolNames(), "calc.sub") {
		t.Fatalf("consumer tools = %v, want calc.sub", consumer.toolNames())
	}
}

type recordingPreparedToolConsumer struct {
	prepared  toolset.PreparedToolset
	callCount int
}

func (c *recordingPreparedToolConsumer) SetPreparedTools(prepared toolset.PreparedToolset) {
	c.prepared = prepared
	c.callCount++
}

func (c *recordingPreparedToolConsumer) toolNames() []string {
	var names []string
	for _, tool := range c.prepared.Tools() {
		names = append(names, tool.Name)
	}
	return names
}
