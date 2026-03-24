package service_test

import (
	"context"
	"testing"

	"github.com/solidarity-ai/toolbox/service"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
)

func TestServiceDiscoveryReturnsAgentViewTools(t *testing.T) {
	t.Parallel()

	resolved := tooltest.CalcToolset(t)
	svc := service.NewService(resolved)

	result, err := svc.DiscoverTools(context.Background())
	if err != nil {
		t.Fatalf("discover tools: %v", err)
	}

	if len(result.Tools) == 0 {
		t.Fatal("expected at least one tool from discovery")
	}

	// Verify calc.add is present
	found := false
	for _, tool := range result.Tools {
		if tool.Name == "calc.add" {
			found = true
			if tool.Description == "" {
				t.Fatal("expected non-empty description")
			}
			break
		}
	}
	if !found {
		t.Fatal("expected calc.add in discovery result")
	}
}

func TestServiceDiscoveryReturnsAllCalcTools(t *testing.T) {
	t.Parallel()

	resolved := tooltest.CalcToolset(t)
	svc := service.NewService(resolved)

	result, err := svc.DiscoverTools(context.Background())
	if err != nil {
		t.Fatalf("discover tools: %v", err)
	}

	names := map[string]bool{}
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}

	for _, want := range []string{"calc.add", "calc.sub", "calc.asyncAdd"} {
		if !names[want] {
			t.Fatalf("expected tool %q in discovery, got %v", want, names)
		}
	}
}
