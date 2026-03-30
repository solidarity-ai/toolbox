package toolsetfile

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/solidarity-ai/toolbox/registry"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
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
		panic(fmt.Errorf("toolsetfile: unmarshal embedded toolset schema: %w", err))
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(fmt.Errorf("toolsetfile: resolve embedded toolset schema: %w", err))
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

// SetPackageVersion updates the declared package version and rewrites any tool
// FQNs for that module to the same version, preserving tool order.
func (f *ToolsetFile) SetPackageVersion(module tooldef.ModulePath, version tooldef.Version) error {
	if f == nil {
		return fmt.Errorf("set package version: nil toolset file")
	}
	if f.Packages == nil {
		return fmt.Errorf("set package version %s: no packages declared", module)
	}
	key := module.String()
	if _, ok := f.Packages[key]; !ok {
		return fmt.Errorf("set package version %s: module is not declared in packages", module)
	}

	f.Packages[key] = version.String()
	for i := range f.Tools {
		if f.Tools[i].parsed.Module != module {
			continue
		}
		f.Tools[i].parsed.Version = version
		f.Tools[i].Tool = f.Tools[i].parsed.String()
	}
	return f.validate()
}

// Write validates the toolset contents and writes a stable JSON encoding while
// preserving the declared tool order.
func (f *ToolsetFile) Write(filename string) error {
	if f == nil {
		return fmt.Errorf("write toolset file %q: nil toolset file", filename)
	}
	data, err := f.encodeStable()
	if err != nil {
		return fmt.Errorf("validate toolset file %q: %w", filename, err)
	}

	tempFile, err := os.CreateTemp(filepath.Dir(filename), filepath.Base(filename)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write toolset file %q: %w", filename, err)
	}
	tempName := tempFile.Name()
	removeTemp := func() {
		_ = os.Remove(tempName)
	}
	defer removeTemp()

	if err := tempFile.Chmod(0o644); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("write toolset file %q: %w", filename, err)
	}
	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("write toolset file %q: %w", filename, err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("write toolset file %q: %w", filename, err)
	}
	if err := os.Rename(tempName, filename); err != nil {
		return fmt.Errorf("write toolset file %q: %w", filename, err)
	}

	f.filename = filename
	if lockFilename, err := deriveToolsetLockFilename(filename); err == nil {
		f.lockFilename = lockFilename
	}
	if localFilename, err := deriveToolsetLocalFilename(filename); err == nil {
		f.localFilename = localFilename
	}
	return nil
}

func (f *ToolsetFile) encodeStable() ([]byte, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}

	packageKeys := make([]string, 0, len(f.Packages))
	for rawModule := range f.Packages {
		packageKeys = append(packageKeys, rawModule)
	}
	sort.Strings(packageKeys)

	var buf bytes.Buffer
	buf.WriteString("{\n")
	buf.WriteString("  \"packages\": {")
	if len(packageKeys) > 0 {
		buf.WriteString("\n")
		for i, rawModule := range packageKeys {
			keyJSON, err := json.Marshal(rawModule)
			if err != nil {
				return nil, fmt.Errorf("marshal package key %q: %w", rawModule, err)
			}
			valueJSON, err := json.Marshal(f.Packages[rawModule])
			if err != nil {
				return nil, fmt.Errorf("marshal package version %q: %w", rawModule, err)
			}
			buf.WriteString("    ")
			buf.Write(keyJSON)
			buf.WriteString(": ")
			buf.Write(valueJSON)
			if i < len(packageKeys)-1 {
				buf.WriteString(",")
			}
			buf.WriteString("\n")
		}
		buf.WriteString("  },\n")
	} else {
		buf.WriteString("},\n")
	}

	buf.WriteString("  \"tools\": [")
	if len(f.Tools) > 0 {
		buf.WriteString("\n")
		for i, tool := range f.Tools {
			entryJSON, err := json.Marshal(struct {
				Tool string `json:"tool"`
			}{Tool: tool.Tool})
			if err != nil {
				return nil, fmt.Errorf("marshal tool entry %d: %w", i, err)
			}
			buf.WriteString("    ")
			buf.Write(entryJSON)
			if i < len(f.Tools)-1 {
				buf.WriteString(",")
			}
			buf.WriteString("\n")
		}
		buf.WriteString("  ]\n")
	} else {
		buf.WriteString("]\n")
	}
	buf.WriteString("}\n")
	return buf.Bytes(), nil
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
func (f *ToolsetFile) Resolve(ctx context.Context, resolver *registry.Resolver) (toolset.ResolvedToolset, error) {
	return f.ResolveWithConfig(ctx, resolver, toolset.Config{})
}

func (f *ToolsetFile) ResolveWithConfig(ctx context.Context, resolver *registry.Resolver, cfg toolset.Config) (toolset.ResolvedToolset, error) {
	if f == nil {
		return toolset.ResolvedToolset{}, fmt.Errorf("resolve toolset file: nil toolset file")
	}
	if f.parsedPackages == nil {
		return toolset.ResolvedToolset{}, fmt.Errorf("resolve toolset file: toolset file must be loaded and validated before resolve")
	}

	var existingLock *ToolsetLockFile
	if f.lockFilename != "" {
		loadedLock, err := LoadLock(f.lockFilename)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return toolset.ResolvedToolset{}, fmt.Errorf("resolve toolset file %q: %w", f.filename, err)
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
		return toolset.ResolvedToolset{}, fmt.Errorf("resolve toolset file %q: %w", f.filename, err)
	}

	builder := toolset.NewWithResolver(resolver)
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
				return toolset.ResolvedToolset{}, fmt.Errorf("resolve %s from local replace %q: %w", packageKey, localDir, err)
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
			return toolset.ResolvedToolset{}, err
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
			return toolset.ResolvedToolset{}, err
		}
	}

	return builder.Resolve(cfg)
}
