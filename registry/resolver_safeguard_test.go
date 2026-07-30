package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/solidarity-ai/toolbox/safeguard"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

type packageGuardFunc func(context.Context, tooldef.ModulePath, tooldef.Version) error

func (f packageGuardFunc) CheckPackage(ctx context.Context, module tooldef.ModulePath, version tooldef.Version) error {
	return f(ctx, module, version)
}

func TestResolverGuardBlocksCachedPackageBeforeLoad(t *testing.T) {
	archiveBytes, manifestBytes := loadDistFixtureBytes(t, "calc-dist")
	module := ModulePath("example.com/acme/calc")
	version := Version("v1.2.3")
	cache := newTempCache(t)
	if err := cache.Put(module, version, archiveBytes, manifestBytes); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	source := &mockSource{}
	resolver := NewResolver(cache, source)
	resolver.SetPackageGuard(packageGuardFunc(func(context.Context, tooldef.ModulePath, tooldef.Version) error {
		return &safeguard.BlockedError{Subject: module.String() + "@" + version.String(), Reason: "known vulnerable package"}
	}))

	_, err := resolver.Resolve(context.Background(), module, version)
	var blocked *safeguard.BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("Resolve() error = %v, want *safeguard.BlockedError", err)
	}
	if source.called != 0 {
		t.Fatalf("source calls = %d, want 0", source.called)
	}
}

func TestResolverGuardFiltersBlockedVersions(t *testing.T) {
	module := ModulePath("example.com/acme/calc")
	source := &mockSource{versions: []Version{"v1.2.3", "v1.2.2"}}
	resolver := NewResolver(newTempCache(t), source)
	resolver.SetPackageGuard(packageGuardFunc(func(_ context.Context, _ tooldef.ModulePath, version tooldef.Version) error {
		if version == "v1.2.3" {
			return &safeguard.BlockedError{Subject: module.String() + "@" + version.String(), Reason: "known vulnerable package"}
		}
		return nil
	}))

	versions, err := resolver.ListVersions(context.Background(), module)
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	if len(versions) != 1 || versions[0] != "v1.2.2" {
		t.Fatalf("ListVersions() = %v, want [v1.2.2]", versions)
	}
}
