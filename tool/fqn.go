package tool

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
)

var (
	versionPattern       = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	pseudoVersionPattern = regexp.MustCompile(`^v0\.0\.0-([0-9]{14})-([0-9a-f]{12})$`)
	toolPathPattern      = regexp.MustCompile(`^[^.]+(?:\.[^.]+)*$`)
)

type ModulePath string

type Version string

type ToolPath string

type ToolFQN struct {
	Module  ModulePath
	Version Version
	Tool    ToolPath
}

type PackageVer struct {
	Module  ModulePath
	Version Version
}

func ParseModulePath(s string) (ModulePath, error) {
	if s == "" {
		return "", errors.New("module path is empty")
	}

	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("module path %q must have at least host/path", s)
	}
	if strings.Contains(parts[0], ".") == false {
		return "", fmt.Errorf("module path %q host %q must contain a dot", s, parts[0])
	}
	for _, part := range parts {
		if part == "" {
			return "", fmt.Errorf("module path %q contains an empty segment", s)
		}
	}
	return ModulePath(s), nil
}

func ParseVersion(s string) (Version, error) {
	if s == "" {
		return "", errors.New("version is empty")
	}
	if strings.HasPrefix(s, "v0.0.0-") {
		suffix := strings.TrimPrefix(s, "v0.0.0-")
		if suffix != "" && suffix[0] >= '0' && suffix[0] <= '9' {
			if pseudoVersionPattern.MatchString(s) {
				return Version(s), nil
			}
			return "", fmt.Errorf("invalid pseudo-version %q", s)
		}
	}
	if versionPattern.MatchString(s) && semver.IsValid(s) {
		return Version(s), nil
	}
	return "", fmt.Errorf("invalid version %q", s)
}

// CompareVersions returns -1, 0, or 1 when a is older than, equal to, or newer
// than b according to semantic-version precedence.
func CompareVersions(a, b Version) int {
	return semver.Compare(a.String(), b.String())
}

func SortVersionsDesc(versions []Version) {
	sort.Slice(versions, func(i, j int) bool {
		return CompareVersions(versions[i], versions[j]) > 0
	})
}

func (v Version) Major() string {
	return semver.Major(v.String())
}

func (v Version) MajorMinor() string {
	return semver.MajorMinor(v.String())
}

func ParseToolPath(s string) (ToolPath, error) {
	if s == "" {
		return "", errors.New("tool path is empty")
	}
	if !toolPathPattern.MatchString(s) {
		return "", fmt.Errorf("invalid tool path %q", s)
	}
	return ToolPath(s), nil
}

func ParseToolFQN(s string) (ToolFQN, error) {
	at := strings.IndexByte(s, '@')
	if at < 0 {
		return ToolFQN{}, fmt.Errorf("tool FQN %q is missing @", s)
	}

	remainder := s[at+1:]
	slash := strings.IndexByte(remainder, '/')
	if slash < 0 {
		return ToolFQN{}, fmt.Errorf("tool FQN %q is missing /toolpath", s)
	}

	module, err := ParseModulePath(s[:at])
	if err != nil {
		return ToolFQN{}, err
	}
	version, err := ParseVersion(remainder[:slash])
	if err != nil {
		return ToolFQN{}, err
	}
	tool, err := ParseToolPath(remainder[slash+1:])
	if err != nil {
		return ToolFQN{}, err
	}

	return ToolFQN{Module: module, Version: version, Tool: tool}, nil
}

func ParsePackageVer(s string) (PackageVer, error) {
	at := strings.IndexByte(s, '@')
	if at < 0 {
		return PackageVer{}, fmt.Errorf("package version %q is missing @", s)
	}

	module, err := ParseModulePath(s[:at])
	if err != nil {
		return PackageVer{}, err
	}
	version, err := ParseVersion(s[at+1:])
	if err != nil {
		return PackageVer{}, err
	}

	return PackageVer{Module: module, Version: version}, nil
}

func (m ModulePath) String() string {
	return string(m)
}

func (v Version) String() string {
	return string(v)
}

func (t ToolPath) String() string {
	return string(t)
}

func (f ToolFQN) String() string {
	return f.Module.String() + "@" + f.Version.String() + "/" + f.Tool.String()
}

func (p PackageVer) String() string {
	return p.Module.String() + "@" + p.Version.String()
}

func (v Version) IsPseudo() bool {
	return pseudoVersionPattern.MatchString(v.String())
}

func (v Version) IsRelease() bool {
	_, err := ParseVersion(v.String())
	return err == nil && !v.IsPseudo()
}

func (v Version) PseudoTimestamp() string {
	matches := pseudoVersionPattern.FindStringSubmatch(v.String())
	if len(matches) != 3 {
		return ""
	}
	return matches[1]
}

func (v Version) PseudoCommit() string {
	matches := pseudoVersionPattern.FindStringSubmatch(v.String())
	if len(matches) != 3 {
		return ""
	}
	return matches[2]
}
