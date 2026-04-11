package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// VersionSource lists available versions for a module from a specific backing source.
type VersionSource interface {
	ListVersions(ctx context.Context, module ModulePath) ([]Version, error)
}

func (s *GitHubReleaseSource) ListVersions(ctx context.Context, module ModulePath) ([]Version, error) {
	owner, repo, err := githubRepoForModule(module)
	if err != nil {
		return nil, err
	}

	path := fmt.Sprintf("/repos/%s/%s/releases", url.PathEscape(owner), url.PathEscape(repo))
	requestURL := s.requestURL(path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build releases request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", requestURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GET %s: read response body: %w", requestURL, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("list releases for %s/%s: %w", owner, repo, ErrReleaseNotFound)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("GET %s: unexpected status %d: %s", requestURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var releases []githubRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("GET %s: decode response: %w", requestURL, err)
	}

	versions := make([]Version, 0, len(releases))
	seen := make(map[Version]struct{}, len(releases))
	for _, release := range releases {
		version, err := tooldef.ParseVersion(release.TagName)
		if err != nil {
			continue
		}
		if _, ok := seen[version]; ok {
			continue
		}
		seen[version] = struct{}{}
		versions = append(versions, version)
	}
	sortVersionsDesc(versions)
	return versions, nil
}

func (s *GitSourceFallback) ListVersions(ctx context.Context, module ModulePath) ([]Version, error) {
	cloneURL := s.cloneURL(module)
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--tags", cloneURL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, wrapGitCommandError(fmt.Sprintf("git ls-remote tags %s from %s", module, cloneURL), err, strings.TrimSpace(string(output)))
	}

	versions := make([]Version, 0)
	seen := make(map[Version]struct{})
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ref := fields[1]
		if !strings.HasPrefix(ref, "refs/tags/") {
			continue
		}
		tag := strings.TrimPrefix(ref, "refs/tags/")
		if strings.HasSuffix(tag, "^{}") {
			tag = strings.TrimSuffix(tag, "^{}")
		}
		version, err := tooldef.ParseVersion(tag)
		if err != nil {
			continue
		}
		if _, ok := seen[version]; ok {
			continue
		}
		seen[version] = struct{}{}
		versions = append(versions, version)
	}
	sortVersionsDesc(versions)
	return versions, nil
}

var semverStrictPattern = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z.-]+))?(?:\+.*)?$`)

type parsedSemver struct {
	major int
	minor int
	patch int
	pre   string
}

func sortVersionsDesc(versions []Version) {
	sort.Slice(versions, func(i, j int) bool {
		return compareVersions(versions[i], versions[j]) > 0
	})
}

func compareVersions(a, b Version) int {
	if a == b {
		return 0
	}
	if a.IsPseudo() && b.IsPseudo() {
		if cmp := strings.Compare(a.PseudoTimestamp(), b.PseudoTimestamp()); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.PseudoCommit(), b.PseudoCommit())
	}
	if a.IsPseudo() {
		return -1
	}
	if b.IsPseudo() {
		return 1
	}

	pa, oka := parseSemver(a)
	pb, okb := parseSemver(b)
	if oka && okb {
		if pa.major != pb.major {
			return intCompare(pa.major, pb.major)
		}
		if pa.minor != pb.minor {
			return intCompare(pa.minor, pb.minor)
		}
		if pa.patch != pb.patch {
			return intCompare(pa.patch, pb.patch)
		}
		return comparePrerelease(pa.pre, pb.pre)
	}

	return strings.Compare(a.String(), b.String())
}

func parseSemver(v Version) (parsedSemver, bool) {
	m := semverStrictPattern.FindStringSubmatch(v.String())
	if len(m) != 5 {
		return parsedSemver{}, false
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return parsedSemver{}, false
	}
	minor, err := strconv.Atoi(m[2])
	if err != nil {
		return parsedSemver{}, false
	}
	patch, err := strconv.Atoi(m[3])
	if err != nil {
		return parsedSemver{}, false
	}
	return parsedSemver{major: major, minor: minor, patch: patch, pre: m[4]}, true
}

func comparePrerelease(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}

	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := 0; i < len(aParts) && i < len(bParts); i++ {
		if aParts[i] == bParts[i] {
			continue
		}
		aNum, aNumOK := parseNumericIdentifier(aParts[i])
		bNum, bNumOK := parseNumericIdentifier(bParts[i])
		switch {
		case aNumOK && bNumOK:
			return intCompare(aNum, bNum)
		case aNumOK:
			return -1
		case bNumOK:
			return 1
		default:
			return strings.Compare(aParts[i], bParts[i])
		}
	}
	return intCompare(len(aParts), len(bParts))
}

func parseNumericIdentifier(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

func intCompare(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

var _ VersionSource = (*GitHubReleaseSource)(nil)
var _ VersionSource = (*GitSourceFallback)(nil)
