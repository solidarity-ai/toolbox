package tooltest

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/solidarity-ai/toolbox/toolset"
)

func githubIssuesFixtureDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("tooltest: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "fixtures", "toolbox.pkgs", "github-issues")
}

// GithubIssuesBuilder returns a *toolset.Builder loaded with the github-issues fixture package.
func GithubIssuesBuilder(t testing.TB) *toolset.Builder {
	t.Helper()

	builder := toolset.New()
	if err := builder.AddFromDir(githubIssuesFixtureDir()); err != nil {
		t.Fatalf("add github-issues package dir: %v", err)
	}
	return builder
}
