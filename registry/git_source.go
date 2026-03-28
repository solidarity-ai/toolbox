package registry

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/solidarity-ai/toolbox/packaging"
)

const defaultGitCloneURLPrefix = "https://"

// GitSourceFallback packages a tagged git checkout when no release assets are available.
type GitSourceFallback struct {
	URLPrefix string
}

func (s *GitSourceFallback) Fetch(ctx context.Context, module ModulePath, version Version) ([]byte, []byte, error) {
	cloneRoot, err := os.MkdirTemp("", "toolbox-git-clone-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create clone temp dir: %w", err)
	}
	defer os.RemoveAll(cloneRoot)

	outDir, err := os.MkdirTemp("", "toolbox-git-pack-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create packaging temp dir: %w", err)
	}
	defer os.RemoveAll(outDir)

	checkoutDir := filepath.Join(cloneRoot, "repo")
	tag := version.String()
	cloneURL := s.cloneURL(module)

	cmd := exec.CommandContext(ctx, "git", "clone", "--depth=1", "--branch", tag, cloneURL, checkoutDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, nil, fmt.Errorf("git clone %s@%s from %s: %w: %s", module, tag, cloneURL, err, strings.TrimSpace(string(output)))
	}

	packed, err := packaging.Pack(checkoutDir, outDir)
	if err != nil {
		return nil, nil, fmt.Errorf("pack cloned repo %s@%s: %w", module, tag, err)
	}

	archiveBytes, err := os.ReadFile(packed.ArchivePath)
	if err != nil {
		return nil, nil, fmt.Errorf("read packaged archive %s: %w", packed.ArchivePath, err)
	}
	manifestBytes, err := os.ReadFile(packed.ManifestPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read packaged manifest %s: %w", packed.ManifestPath, err)
	}

	return archiveBytes, manifestBytes, nil
}

func (s *GitSourceFallback) cloneURL(module ModulePath) string {
	prefix := s.URLPrefix
	if strings.TrimSpace(prefix) == "" {
		prefix = defaultGitCloneURLPrefix
	}
	return prefix + module.String()
}

var _ PackageSource = (*GitSourceFallback)(nil)
