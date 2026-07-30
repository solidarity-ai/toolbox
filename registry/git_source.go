package registry

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/solidarity-ai/toolbox/packaging"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

const defaultGitCloneURLPrefix = "https://"

// GitSourceFallback packages a tagged git checkout when no release assets are available.
type GitSourceFallback struct {
	URLPrefix     string
	PackerVersion tooldef.Version
}

func (s *GitSourceFallback) Fetch(ctx context.Context, module ModulePath, version Version) (FetchResult, error) {
	cloneRoot, err := os.MkdirTemp("", "toolbox-git-clone-*")
	if err != nil {
		return FetchResult{}, fmt.Errorf("create clone temp dir: %w", err)
	}
	defer os.RemoveAll(cloneRoot)

	outDir, err := os.MkdirTemp("", "toolbox-git-pack-*")
	if err != nil {
		return FetchResult{}, fmt.Errorf("create packaging temp dir: %w", err)
	}
	defer os.RemoveAll(outDir)

	checkoutDir := filepath.Join(cloneRoot, "repo")
	cloneURL := s.cloneURL(module)

	if version.IsPseudo() {
		err = s.clonePseudoVersion(ctx, module, version, cloneURL, checkoutDir)
	} else {
		err = s.cloneTaggedVersion(ctx, module, version, cloneURL, checkoutDir)
	}
	if err != nil {
		return FetchResult{}, err
	}

	gitSHA, err := runGitForVersion(ctx, checkoutDir, module, version, cloneURL, "rev-parse HEAD", "rev-parse", "HEAD")
	if err != nil {
		return FetchResult{}, err
	}
	if !gitCommitSHAPattern.MatchString(gitSHA) {
		return FetchResult{}, fmt.Errorf("git rev-parse HEAD %s@%s from %s returned invalid commit sha %q", module, version, cloneURL, gitSHA)
	}

	packed, err := packaging.Pack(checkoutDir, outDir, s.PackerVersion)
	if err != nil {
		return FetchResult{}, fmt.Errorf("pack cloned repo %s@%s: %w", module, version, err)
	}

	archiveBytes, err := os.ReadFile(packed.ArchivePath)
	if err != nil {
		return FetchResult{}, fmt.Errorf("read packaged archive %s: %w", packed.ArchivePath, err)
	}
	manifestBytes, err := os.ReadFile(packed.ManifestPath)
	if err != nil {
		return FetchResult{}, fmt.Errorf("read packaged manifest %s: %w", packed.ManifestPath, err)
	}

	metadata := ResolveMetadata{
		ArchiveSHA256: sha256Hex(archiveBytes),
		GitSHA:        strings.ToLower(gitSHA),
		ResolvedFrom:  ResolvedFromGitSource,
		ResolvedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	if err := metadata.Validate(); err != nil {
		return FetchResult{}, fmt.Errorf("git source %s@%s produced invalid metadata: %w", module, version, err)
	}

	return FetchResult{Archive: archiveBytes, Manifest: manifestBytes, Metadata: metadata}, nil
}

func (s *GitSourceFallback) cloneTaggedVersion(ctx context.Context, module ModulePath, version Version, cloneURL string, checkoutDir string) error {
	_, err := runGitForVersion(ctx, "", module, version, cloneURL, "clone", "clone", "--depth=1", "--branch", version.String(), cloneURL, checkoutDir)
	return err
}

func (s *GitSourceFallback) clonePseudoVersion(ctx context.Context, module ModulePath, version Version, cloneURL string, checkoutDir string) error {
	if _, err := runGitForVersion(ctx, "", module, version, cloneURL, "clone", "clone", cloneURL, checkoutDir); err != nil {
		return err
	}

	commit, err := runGitForVersion(ctx, checkoutDir, module, version, cloneURL, "rev-parse", "rev-parse", "--verify", version.PseudoCommit()+"^{commit}")
	if err != nil {
		return err
	}

	commitDate, err := runGitForVersion(ctx, checkoutDir, module, version, cloneURL, "show", "show", "-s", "--format=%cI", commit)
	if err != nil {
		return err
	}

	commitTime, err := time.Parse(time.RFC3339, commitDate)
	if err != nil {
		return fmt.Errorf("git show %s@%s returned invalid commit timestamp %q for commit %s: %w", module, version, commitDate, commit, err)
	}

	resolvedTimestamp := commitTime.UTC().Format("20060102150405")
	if resolvedTimestamp != version.PseudoTimestamp() {
		return fmt.Errorf("git show %s@%s timestamp mismatch for commit %s: pseudo-version timestamp %s, git commit timestamp %s", module, version, commit, version.PseudoTimestamp(), resolvedTimestamp)
	}

	if _, err := runGitForVersion(ctx, checkoutDir, module, version, cloneURL, "checkout", "checkout", commit); err != nil {
		return err
	}

	return nil
}

func runGitForVersion(ctx context.Context, dir string, module ModulePath, version Version, cloneURL string, step string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if err != nil {
		return "", wrapGitCommandError(fmt.Sprintf("git %s %s@%s from %s", step, module, version, cloneURL), err, trimmed)
	}
	return trimmed, nil
}

func wrapGitCommandError(prefix string, err error, output string) error {
	if looksLikeGitNotFound(output) {
		if output == "" {
			return fmt.Errorf("%s: %v: %w", prefix, err, ErrReleaseNotFound)
		}
		return fmt.Errorf("%s: %v: %s: %w", prefix, err, output, ErrReleaseNotFound)
	}
	if output == "" {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	return fmt.Errorf("%s: %w: %s", prefix, err, output)
}

func looksLikeGitNotFound(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "repository not found") ||
		strings.Contains(lower, "remote branch") && strings.Contains(lower, "not found in upstream origin") ||
		strings.Contains(lower, "couldn't find remote ref")
}

func (s *GitSourceFallback) cloneURL(module ModulePath) string {
	prefix := s.URLPrefix
	if strings.TrimSpace(prefix) == "" {
		prefix = defaultGitCloneURLPrefix
	}
	return prefix + module.String()
}

var _ PackageSource = (*GitSourceFallback)(nil)
