package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/solidarity-ai/toolbox/packaging"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

type ModulePath = tooldef.ModulePath

type Version = tooldef.Version

const cacheDirEnv = "TOOLBOX_CACHE_DIR"

// Cache stores packaged archives and manifests on local disk.
type Cache struct {
	root string
}

// NewCache returns a cache rooted at dir. When dir is empty it first consults
// TOOLBOX_CACHE_DIR, then falls back to os.UserCacheDir()/toolbox/pkg.
func NewCache(dir string) (*Cache, error) {
	root := dir
	if root == "" {
		root = os.Getenv(cacheDirEnv)
	}
	if root == "" {
		userCacheDir, err := os.UserCacheDir()
		if err != nil {
			return nil, fmt.Errorf("determine user cache dir: %w", err)
		}
		root = filepath.Join(userCacheDir, "toolbox", "pkg")
	}
	return &Cache{root: root}, nil
}

func (c *Cache) Has(module ModulePath, version Version) bool {
	paths := c.paths(module, version)
	for _, path := range []string{paths.archive, paths.manifest, paths.info} {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}

func (c *Cache) Put(module ModulePath, version Version, archiveBytes, manifestBytes []byte) error {
	paths := c.paths(module, version)
	if err := os.MkdirAll(filepath.Dir(paths.archive), 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	infoBytes, err := json.Marshal(struct {
		Version string `json:"version"`
	}{Version: version.String()})
	if err != nil {
		return fmt.Errorf("marshal cache info: %w", err)
	}

	for _, file := range []struct {
		path string
		data []byte
	}{
		{path: paths.archive, data: archiveBytes},
		{path: paths.manifest, data: manifestBytes},
		{path: paths.info, data: infoBytes},
	} {
		if err := os.WriteFile(file.path, file.data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", file.path, err)
		}
	}

	return nil
}

func (c *Cache) LoadArchive(module ModulePath, version Version, check func(tooldef.Package) error) (packaging.LoadedPackage, error) {
	paths := c.paths(module, version)
	return packaging.LoadArchive(paths.archive, paths.manifest, check)
}

func (c *Cache) ArchiveSHA256(module ModulePath, version Version) (string, error) {
	paths := c.paths(module, version)
	archiveBytes, err := os.ReadFile(paths.archive)
	if err != nil {
		return "", fmt.Errorf("read cached archive %s: %w", paths.archive, err)
	}
	return sha256Hex(archiveBytes), nil
}

// ListVersions returns cached versions for a module, sorted newest-first.
func (c *Cache) ListVersions(module ModulePath) ([]Version, error) {
	dir := filepath.Join(c.root, module.String(), "@v")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read cached versions for %s: %w", module, err)
	}

	versions := make([]Version, 0, len(entries))
	seen := make(map[Version]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".info") {
			continue
		}
		raw := strings.TrimSuffix(entry.Name(), ".info")
		version, err := tooldef.ParseVersion(raw)
		if err != nil {
			continue
		}
		if _, ok := seen[version]; ok {
			continue
		}
		seen[version] = struct{}{}
		versions = append(versions, version)
	}

	tooldef.SortVersionsDesc(versions)
	return versions, nil
}

type cachePaths struct {
	archive  string
	manifest string
	info     string
}

func (c *Cache) paths(module ModulePath, version Version) cachePaths {
	base := filepath.Join(c.root, module.String(), "@v", version.String())
	return cachePaths{
		archive:  base + ".pkg",
		manifest: base + ".manifest",
		info:     base + ".info",
	}
}
