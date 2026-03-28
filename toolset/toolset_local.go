package toolset

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

const toolsetLocalFilenameSuffix = ".toolset.local.json"

var (
	//go:embed toolbox.toolset.local.schema.json
	toolboxToolsetLocalSchemaJSON []byte

	resolvedToolboxToolsetLocalSchema = mustResolveSchema(toolboxToolsetLocalSchemaJSON)
)

// ToolsetLocalFile is the optional sibling *.toolset.local.json overlay for a
// declarative toolset file.
type ToolsetLocalFile struct {
	Replace map[string]string `json:"replace"`

	parsedReplace map[tooldef.ModulePath]string
	filename      string
}

func deriveToolsetLocalFilename(filename string) (string, error) {
	if !strings.HasSuffix(filename, toolsetFilenameSuffix) {
		return "", fmt.Errorf("derive toolset local filename from %q: filename must end with %q", filename, toolsetFilenameSuffix)
	}
	return strings.TrimSuffix(filename, toolsetFilenameSuffix) + toolsetLocalFilenameSuffix, nil
}

// LoadLocal reads a sibling *.toolset.local.json file, validates its JSON
// shape via the embedded schema, then applies semantic validation on each
// replace key.
func LoadLocal(filename string) (*ToolsetLocalFile, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read toolset local file %q: %w", filename, err)
	}

	var instance map[string]any
	if err := json.Unmarshal(data, &instance); err != nil {
		return nil, fmt.Errorf("parse toolset local file %q: %w", filename, err)
	}
	if err := resolvedToolboxToolsetLocalSchema.Validate(instance); err != nil {
		return nil, fmt.Errorf("schema-validate toolset local file %q: %w", filename, err)
	}

	var file ToolsetLocalFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse toolset local file %q: %w", filename, err)
	}
	if err := file.validate(); err != nil {
		return nil, fmt.Errorf("validate toolset local file %q: %w", filename, err)
	}

	file.filename = filename
	return &file, nil
}

func (f *ToolsetLocalFile) validate() error {
	if f == nil {
		return errors.New("nil toolset local file")
	}
	if f.Replace == nil {
		f.Replace = map[string]string{}
	}

	keys := make([]string, 0, len(f.Replace))
	for rawModule := range f.Replace {
		keys = append(keys, rawModule)
	}
	sort.Strings(keys)

	parsedReplace := make(map[tooldef.ModulePath]string, len(keys))
	for _, rawModule := range keys {
		module, err := tooldef.ParseModulePath(rawModule)
		if err != nil {
			return fmt.Errorf("replace[%q]: %w", rawModule, err)
		}
		parsedReplace[module] = f.Replace[rawModule]
	}

	f.parsedReplace = parsedReplace
	return nil
}

// SourceFilename returns the loaded *.toolset.local.json path.
func (f *ToolsetLocalFile) SourceFilename() string {
	if f == nil {
		return ""
	}
	return f.filename
}

// ReplacementDir returns the replacement directory declared for module, if any.
func (f *ToolsetLocalFile) ReplacementDir(module tooldef.ModulePath) (string, bool) {
	if f == nil {
		return "", false
	}
	dir, ok := f.parsedReplace[module]
	return dir, ok
}
