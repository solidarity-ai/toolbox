package toolsetfile

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

const (
	toolsetFilenameSuffix     = ".toolset.json"
	toolsetLockFilenameSuffix = ".toolset.lock"
)

var (
	//go:embed toolbox.toolset.lock.schema.json
	toolboxToolsetLockSchemaJSON []byte

	resolvedToolboxToolsetLockSchema = mustResolveSchema(toolboxToolsetLockSchemaJSON)
	sha256HexPattern                 = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	gitCommitSHAPattern              = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
)

type ToolsetLockResolvedFrom string

const (
	ToolsetLockResolvedFromGitHubRelease ToolsetLockResolvedFrom = "github-release"
	ToolsetLockResolvedFromGitSource     ToolsetLockResolvedFrom = "git-source"
	ToolsetLockResolvedFromToolRegistry  ToolsetLockResolvedFrom = "tool-registry"
)

// ToolsetLockEntry records the resolved provenance and integrity metadata for
// one locked package version.
type ToolsetLockEntry struct {
	ArchiveSHA256 string                  `json:"archive_sha256"`
	GitSHA        string                  `json:"git_sha"`
	ResolvedFrom  ToolsetLockResolvedFrom `json:"resolved_from"`
	ResolvedAt    string                  `json:"resolved_at"`
}

// ToolsetLockFile is the sibling *.toolset.lock contract for a declarative
// toolset file.
type ToolsetLockFile struct {
	Packages map[string]ToolsetLockEntry `json:"packages"`

	parsedPackages map[tooldef.PackageVer]ToolsetLockEntry
}

func deriveToolsetLockFilename(filename string) (string, error) {
	if !strings.HasSuffix(filename, toolsetFilenameSuffix) {
		return "", fmt.Errorf("derive toolset lock filename from %q: filename must end with %q", filename, toolsetFilenameSuffix)
	}
	return strings.TrimSuffix(filename, toolsetFilenameSuffix) + toolsetLockFilenameSuffix, nil
}

// LoadLock reads a sibling *.toolset.lock file, validates its JSON shape via
// the embedded schema, then applies semantic validation on each keyed entry.
func LoadLock(filename string) (*ToolsetLockFile, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read toolset lock file %q: %w", filename, err)
	}

	var instance map[string]any
	if err := json.Unmarshal(data, &instance); err != nil {
		return nil, fmt.Errorf("parse toolset lock file %q: %w", filename, err)
	}
	if err := resolvedToolboxToolsetLockSchema.Validate(instance); err != nil {
		return nil, fmt.Errorf("schema-validate toolset lock file %q: %w", filename, err)
	}

	var file ToolsetLockFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse toolset lock file %q: %w", filename, err)
	}
	if err := file.validate(); err != nil {
		return nil, fmt.Errorf("validate toolset lock file %q: %w", filename, err)
	}

	return &file, nil
}

func (f *ToolsetLockFile) validate() error {
	if f == nil {
		return errors.New("nil toolset lock file")
	}
	if f.Packages == nil {
		f.Packages = map[string]ToolsetLockEntry{}
	}

	keys := make([]string, 0, len(f.Packages))
	for rawPackage := range f.Packages {
		keys = append(keys, rawPackage)
	}
	sort.Strings(keys)

	parsedPackages := make(map[tooldef.PackageVer]ToolsetLockEntry, len(keys))
	for _, rawPackage := range keys {
		pkg, err := tooldef.ParsePackageVer(rawPackage)
		if err != nil {
			return fmt.Errorf("packages[%q]: %w", rawPackage, err)
		}
		if err := f.Packages[rawPackage].validate(rawPackage); err != nil {
			return fmt.Errorf("packages[%q]: %w", rawPackage, err)
		}
		parsedPackages[pkg] = f.Packages[rawPackage]
	}

	f.parsedPackages = parsedPackages
	return nil
}

func (e ToolsetLockEntry) validate(_ string) error {
	if !sha256HexPattern.MatchString(e.ArchiveSHA256) {
		return fmt.Errorf("archive_sha256 %q must be a 64-character hex sha256", e.ArchiveSHA256)
	}
	if !gitCommitSHAPattern.MatchString(e.GitSHA) {
		return fmt.Errorf("git_sha %q must be a 40-character hex git commit", e.GitSHA)
	}
	switch e.ResolvedFrom {
	case ToolsetLockResolvedFromGitHubRelease, ToolsetLockResolvedFromGitSource, ToolsetLockResolvedFromToolRegistry:
		// okay
	default:
		return fmt.Errorf("resolved_from %q is invalid", e.ResolvedFrom)
	}
	if _, err := time.Parse(time.RFC3339, e.ResolvedAt); err != nil {
		return fmt.Errorf("resolved_at %q must be RFC3339: %w", e.ResolvedAt, err)
	}
	return nil
}

// Write validates the lockfile contents and writes a stable JSON encoding to
// disk without relying on map iteration order.
func (f *ToolsetLockFile) Write(filename string) error {
	data, err := f.encodeStable()
	if err != nil {
		return fmt.Errorf("validate toolset lock file %q: %w", filename, err)
	}

	tempFile, err := os.CreateTemp(filepath.Dir(filename), filepath.Base(filename)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write toolset lock file %q: %w", filename, err)
	}
	tempName := tempFile.Name()
	removeTemp := func() {
		_ = os.Remove(tempName)
	}
	defer removeTemp()

	if err := tempFile.Chmod(0o644); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("write toolset lock file %q: %w", filename, err)
	}
	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("write toolset lock file %q: %w", filename, err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("write toolset lock file %q: %w", filename, err)
	}
	if err := os.Rename(tempName, filename); err != nil {
		return fmt.Errorf("write toolset lock file %q: %w", filename, err)
	}
	return nil
}

func (f *ToolsetLockFile) encodeStable() ([]byte, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(f.Packages))
	for rawPackage := range f.Packages {
		keys = append(keys, rawPackage)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString("{\n")
	if len(keys) == 0 {
		buf.WriteString("  \"packages\": {}\n")
		buf.WriteString("}\n")
		return buf.Bytes(), nil
	}

	buf.WriteString("  \"packages\": {\n")
	for i, rawPackage := range keys {
		keyJSON, err := json.Marshal(rawPackage)
		if err != nil {
			return nil, fmt.Errorf("marshal package key %q: %w", rawPackage, err)
		}
		entryJSON, err := json.Marshal(f.Packages[rawPackage])
		if err != nil {
			return nil, fmt.Errorf("marshal package %q: %w", rawPackage, err)
		}

		buf.WriteString("    ")
		buf.Write(keyJSON)
		buf.WriteString(": ")
		buf.Write(entryJSON)
		if i < len(keys)-1 {
			buf.WriteString(",")
		}
		buf.WriteString("\n")
	}
	buf.WriteString("  }\n")
	buf.WriteString("}\n")
	return buf.Bytes(), nil
}
