package toolset

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/solidarity-ai/toolbox/registry"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

//go:embed toolbox.toolset.schema.json
var toolboxToolsetSchemaJSON []byte

var resolvedToolboxToolsetSchema = mustResolveSchema(toolboxToolsetSchemaJSON)

// ToolEntry declares one tool included in a toolset file.
type ToolEntry struct {
	Tool string `json:"tool"`

	parsed tooldef.ToolFQN
}

// ToolsetFile is the minimal declarative *.toolset.json format.
type ToolsetFile struct {
	Packages map[string]string `json:"packages"`
	Tools    []ToolEntry       `json:"tools"`

	parsedPackages map[tooldef.ModulePath]tooldef.Version
	filename       string
	lockFilename   string
	localFilename  string
}

func mustResolveSchema(raw []byte) *jsonschema.Resolved {
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		panic(fmt.Errorf("toolset: unmarshal embedded toolset schema: %w", err))
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(fmt.Errorf("toolset: resolve embedded toolset schema: %w", err))
	}
	return resolved
}

// Load reads a *.toolset.json file, decodes it, and validates its package and
// tool references before any registry resolution happens.
func Load(filename string) (*ToolsetFile, error) {
	lockFilename, err := deriveToolsetLockFilename(filename)
	if err != nil {
		return nil, err
	}
	localFilename, err := deriveToolsetLocalFilename(filename)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read toolset file %q: %w", filename, err)
	}

	var instance map[string]any
	if err := json.Unmarshal(data, &instance); err != nil {
		return nil, fmt.Errorf("parse toolset file %q: %w", filename, err)
	}
	if err := resolvedToolboxToolsetSchema.Validate(instance); err != nil {
		return nil, fmt.Errorf("validate toolset file %q: %w", filename, err)
	}

	var file ToolsetFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse toolset file %q: %w", filename, err)
	}

	if err := file.validate(); err != nil {
		return nil, err
	}

	file.filename = filename
	file.lockFilename = lockFilename
	file.localFilename = localFilename
	return &file, nil
}

func (f *ToolsetFile) validate() error {
	keys := make([]string, 0, len(f.Packages))
	for rawModule := range f.Packages {
		keys = append(keys, rawModule)
	}
	sort.Strings(keys)

	parsedPackages := make(map[tooldef.ModulePath]tooldef.Version, len(keys))
	for _, rawModule := range keys {
		module, err := tooldef.ParseModulePath(rawModule)
		if err != nil {
			return fmt.Errorf("packages[%q]: %w", rawModule, err)
		}

		version, err := tooldef.ParseVersion(f.Packages[rawModule])
		if err != nil {
			return fmt.Errorf("packages[%q]: %w", rawModule, err)
		}

		parsedPackages[module] = version
	}

	for i := range f.Tools {
		parsedTool, err := tooldef.ParseToolFQN(f.Tools[i].Tool)
		if err != nil {
			return fmt.Errorf("tools[%d].tool: %w", i, err)
		}

		declaredVersion, ok := parsedPackages[parsedTool.Module]
		if !ok {
			return fmt.Errorf("tools[%d].tool: module %q is not declared in packages", i, parsedTool.Module)
		}
		if parsedTool.Version != declaredVersion {
			return fmt.Errorf("tools[%d].tool: version %q does not match declared package version %q for module %q", i, parsedTool.Version, declaredVersion, parsedTool.Module)
		}

		f.Tools[i].parsed = parsedTool
	}

	f.parsedPackages = parsedPackages
	return nil
}

// SourceFilename returns the loaded *.toolset.json path.
func (f *ToolsetFile) SourceFilename() string {
	if f == nil {
		return ""
	}
	return f.filename
}

// LockFilename returns the derived sibling *.toolset.lock path.
func (f *ToolsetFile) LockFilename() string {
	if f == nil {
		return ""
	}
	return f.lockFilename
}

// LocalFilename returns the derived sibling *.toolset.local.json path.
func (f *ToolsetFile) LocalFilename() string {
	if f == nil {
		return ""
	}
	return f.localFilename
}

// LoadLocal loads the optional sibling *.toolset.local.json overlay. Missing
// overlays are treated as absent rather than invalid.
func (f *ToolsetFile) LoadLocal() (*ToolsetLocalFile, error) {
	if f == nil {
		return nil, fmt.Errorf("load toolset local file: nil toolset file")
	}
	if f.localFilename == "" {
		return nil, fmt.Errorf("load toolset local file: toolset file must be loaded before local overlay can be derived")
	}

	file, err := LoadLocal(f.localFilename)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return file, nil
}

// Resolve materializes the declared packages through the same builder and
// registry path used by imperative toolset construction while loading,
// verifying, and rewriting the sibling lockfile only after all packages have
// resolved successfully.
func (f *ToolsetFile) Resolve(ctx context.Context, resolver *registry.Resolver) (ResolvedToolset, error) {
	if f == nil {
		return ResolvedToolset{}, fmt.Errorf("resolve toolset file: nil toolset file")
	}
	if f.parsedPackages == nil {
		return ResolvedToolset{}, fmt.Errorf("resolve toolset file: toolset file must be loaded and validated before resolve")
	}

	var existingLock *ToolsetLockFile
	if f.lockFilename != "" {
		loadedLock, err := LoadLock(f.lockFilename)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return ResolvedToolset{}, fmt.Errorf("resolve toolset file %q: %w", f.filename, err)
			}
		} else {
			existingLock = loadedLock
		}
	}
	if existingLock == nil {
		existingLock = &ToolsetLockFile{Packages: map[string]ToolsetLockEntry{}}
	}

	local, err := f.LoadLocal()
	if err != nil {
		return ResolvedToolset{}, fmt.Errorf("resolve toolset file %q: %w", f.filename, err)
	}

	builder := NewWithResolver(resolver)
	updatedLock := &ToolsetLockFile{Packages: make(map[string]ToolsetLockEntry, len(existingLock.Packages))}
	for packageKey, entry := range existingLock.Packages {
		updatedLock.Packages[packageKey] = entry
	}
	modules := make([]tooldef.ModulePath, 0, len(f.parsedPackages))
	for module := range f.parsedPackages {
		modules = append(modules, module)
	}
	sort.Slice(modules, func(i, j int) bool {
		return modules[i].String() < modules[j].String()
	})

	for _, module := range modules {
		version := f.parsedPackages[module]
		packageKey := fmt.Sprintf("%s@%s", module, version)

		if localDir, ok := local.ReplacementDirAbs(module); ok {
			if err := builder.AddFromDir(localDir); err != nil {
				return ResolvedToolset{}, fmt.Errorf("resolve %s from local replace %q: %w", packageKey, localDir, err)
			}
			continue
		}

		var expected *registry.ResolveMetadata
		if existing, ok := existingLock.Packages[packageKey]; ok {
			expected = &registry.ResolveMetadata{
				ArchiveSHA256: existing.ArchiveSHA256,
				GitSHA:        existing.GitSHA,
				ResolvedFrom:  registry.ResolvedFrom(existing.ResolvedFrom),
				ResolvedAt:    existing.ResolvedAt,
			}
		}

		metadata, err := builder.AddFromRegistryWithExpected(ctx, module.String(), version.String(), expected)
		if err != nil {
			return ResolvedToolset{}, err
		}
		updatedLock.Packages[packageKey] = ToolsetLockEntry{
			ArchiveSHA256: metadata.ArchiveSHA256,
			GitSHA:        metadata.GitSHA,
			ResolvedFrom:  ToolsetLockResolvedFrom(metadata.ResolvedFrom),
			ResolvedAt:    metadata.ResolvedAt,
		}
	}

	if f.lockFilename != "" {
		if err := updatedLock.Write(f.lockFilename); err != nil {
			return ResolvedToolset{}, err
		}
	}

	return builder.Resolve(), nil
}
