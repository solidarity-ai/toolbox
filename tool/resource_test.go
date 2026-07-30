package tool_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/microsoft/typescript-go/toolbox"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestResolveResourceUses(t *testing.T) {
	resources := []tooldef.Resource{
		{Path: "workbook", Params: []tooldef.ResourceParam{{Name: "path", BindingName: "workbook"}}},
		{Path: "workbook.sheet", Params: []tooldef.ResourceParam{{Name: "sheet", BindingName: "worksheet"}}},
		{Path: "workbook.sheet.range", Params: []tooldef.ResourceParam{{Name: "start", BindingName: "range_start"}, {Name: "end", BindingName: "range_end"}}},
	}

	tests := []struct {
		name     string
		toolName string
		source   string
		selected []bool
		wantErr  string
	}{
		{
			name:     "collection method leaves deepest selector unselected",
			toolName: "workbook.sheet.list",
			source:   "export default function tool(path: string): string[] { return []; }",
			selected: []bool{true, false},
		},
		{
			name:     "member method selects nested resource",
			toolName: "workbook.sheet.get",
			source:   "export default function tool(path: string, sheet: string): string { return sheet; }",
			selected: []bool{true, true},
		},
		{
			name:     "multi argument selector",
			toolName: "workbook.sheet.range.get",
			source:   "export default function tool(path: string, sheet: string, start: string, end: string): string { return start + end; }",
			selected: []bool{true, true, true},
		},
		{
			name:     "method parameters follow selector prefix",
			toolName: "workbook.sheet.search",
			source:   "export default function tool(path: string, sheet: string, query: string): string { return query; }",
			selected: []bool{true, true},
		},
		{
			name:     "reordered resource chain",
			toolName: "workbook.sheet.get",
			source:   "export default function tool(sheet: string, path: string): string { return sheet; }",
			wantErr:  "ordered signature prefix [path sheet]",
		},
		{
			name:     "method parameter interleaves selectors",
			toolName: "workbook.sheet.search",
			source:   "export default function tool(path: string, query: string, sheet: string): string { return query; }",
			wantErr:  "ordered signature prefix [path sheet]",
		},
		{
			name:     "method parameter precedes selector",
			toolName: "workbook.open",
			source:   "export default function tool(mode: string, path: string): string { return mode; }",
			wantErr:  "ordered signature prefix [path]",
		},
		{
			name:     "multi argument selector declaration order",
			toolName: "workbook.sheet.range.get",
			source:   "export default function tool(path: string, sheet: string, end: string, start: string): string { return start + end; }",
			wantErr:  "ordered signature prefix [path sheet start end]",
		},
		{
			name:     "partial selector",
			toolName: "workbook.sheet.range.get",
			source:   "export default function tool(path: string, sheet: string, start: string): string { return start; }",
			wantErr:  "partially selects resource",
		},
		{
			name:     "selected child without parent",
			toolName: "workbook.sheet.get",
			source:   "export default function tool(sheet: string): string { return sheet; }",
			wantErr:  "after leaving an ancestor resource unselected",
		},
		{
			name:     "only deepest resource may remain unselected",
			toolName: "workbook.sheet.range.list",
			source:   "export default function tool(path: string): string[] { return []; }",
			wantErr:  "leaves non-deepest resource",
		},
		{
			name:     "optional selector",
			toolName: "workbook.open",
			source:   "export default function tool(path?: string): string { return path ?? ''; }",
			wantErr:  "must be required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uses, err := tooldef.ResolveResourceUses(resources, tt.toolName, extractSignature(t, tt.source))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ResolveResourceUses() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveResourceUses() error: %v", err)
			}
			if len(uses) != len(tt.selected) {
				t.Fatalf("uses len = %d, want %d", len(uses), len(tt.selected))
			}
			for i, selected := range tt.selected {
				if uses[i].Selected != selected {
					t.Fatalf("uses[%d].Selected = %v, want %v", i, uses[i].Selected, selected)
				}
			}
		})
	}
}

func TestCloneResourceUses_IsolatesNestedValues(t *testing.T) {
	original := []tooldef.ResourceUse{{
		Resource: tooldef.Resource{
			Path:   "workbook",
			Params: []tooldef.ResourceParam{{Name: "path", BindingName: "workbook"}},
		},
		Selected: true,
	}}
	cloned := tooldef.CloneResourceUses(original)

	cloned[0].Selected = false
	cloned[0].Resource.Path = "changed"
	cloned[0].Resource.Params[0].Name = "changed"
	if !original[0].Selected || original[0].Resource.Path != "workbook" || original[0].Resource.Params[0].Name != "path" {
		t.Fatalf("clone mutation changed original: %#v", original)
	}

	original[0].Resource.Params[0].BindingName = "changed"
	if cloned[0].Resource.Params[0].BindingName != "workbook" {
		t.Fatalf("original mutation changed clone: %#v", cloned)
	}
}

func TestCompileResourceAPI_CollapsesSelectedResourceWithoutVisibleParams(t *testing.T) {
	plan, err := tooldef.CompileResourceAPI([]tooldef.ResourceAPITool{{
		Name: "workbook.sheet.get",
		Uses: []tooldef.ResourceAPIUse{
			{Path: "workbook", Selected: true},
			{Path: "workbook.sheet", Params: []string{"sheet"}, Selected: true},
		},
	}})
	if err != nil {
		t.Fatalf("CompileResourceAPI() error: %v", err)
	}
	if len(plan.Root.Children) != 1 || plan.Root.Children[0].Resource == nil {
		t.Fatalf("root children = %#v, want workbook resource", plan.Root.Children)
	}
	workbook := plan.Root.Children[0].Resource
	if workbook.Callable {
		t.Fatal("workbook resource is callable with no visible selector params")
	}
	if len(workbook.Collection.Children) != 1 || workbook.Collection.Children[0].Resource == nil {
		t.Fatalf("workbook collection children = %#v, want sheet resource", workbook.Collection.Children)
	}
	if !workbook.Collection.Children[0].Resource.Callable {
		t.Fatal("sheet selector should remain callable")
	}
}

func TestCompileResourceAPI_RejectsMixedCollapsedAndCallableResource(t *testing.T) {
	_, err := tooldef.CompileResourceAPI([]tooldef.ResourceAPITool{
		{Name: "workbook.get", Uses: []tooldef.ResourceAPIUse{{Path: "workbook", Selected: true}}},
		{Name: "workbook.update", Uses: []tooldef.ResourceAPIUse{{Path: "workbook", Params: []string{"path"}, Selected: true}}},
	})
	if err == nil || !strings.Contains(err.Error(), "incompatible selector parameters") {
		t.Fatalf("CompileResourceAPI() error = %v, want incompatible selector parameters", err)
	}
}

func TestResolvePackageResources_CallableCollectionMembers(t *testing.T) {
	tests := []struct {
		name            string
		collectionNames []string
		wantErr         string
	}{
		{name: "intrinsic collision gets escape method", collectionNames: []string{"name"}},
		{name: "then is reserved", collectionNames: []string{"then"}, wantErr: `member "then" is reserved`},
		{name: "proto is reserved", collectionNames: []string{"__proto__"}, wantErr: `member "__proto__" is reserved`},
		{name: "to string is reserved", collectionNames: []string{"toString"}, wantErr: `member "toString" is reserved`},
		{name: "value of is reserved", collectionNames: []string{"valueOf"}, wantErr: `member "valueOf" is reserved`},
		{name: "object ownership inspection is reserved", collectionNames: []string{"hasOwnProperty"}, wantErr: `member "hasOwnProperty" is reserved`},
		{name: "legacy getter hook is reserved", collectionNames: []string{"__defineGetter__"}, wantErr: `member "__defineGetter__" is reserved`},
		{name: "escape method cannot be shadowed", collectionNames: []string{"name", "_name"}, wantErr: `member "_name" conflicts with intrinsic escape`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkg := &tooldef.Package{
				Resources: []tooldef.Resource{{Path: "workbook", Params: []tooldef.ResourceParam{{Name: "path", BindingName: "workbook"}}}},
				Tools: []tooldef.PackageTool{{
					EntryTS: "tools/workbook.get.ts",
					Sig:     extractSignature(t, "export default function tool(path: string): string { return path; }"),
				}},
			}
			for _, name := range tt.collectionNames {
				pkg.Tools = append(pkg.Tools, tooldef.PackageTool{
					EntryTS: "tools/workbook." + name + ".ts",
					Sig:     extractSignature(t, "export default function tool(): string { return ''; }"),
				})
			}
			err := tooldef.ResolvePackageResources(pkg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ResolvePackageResources() error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ResolvePackageResources() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestResolvePackageResources_ReservedSelectedMember(t *testing.T) {
	pkg := &tooldef.Package{
		Resources: []tooldef.Resource{{Path: "workbook", Params: []tooldef.ResourceParam{{Name: "path", BindingName: "workbook"}}}},
		Tools: []tooldef.PackageTool{{
			EntryTS: "tools/workbook.then.ts",
			Sig:     extractSignature(t, "export default function tool(path: string): string { return path; }"),
		}},
	}
	err := tooldef.ResolvePackageResources(pkg)
	if err == nil || !strings.Contains(err.Error(), `member "then" is reserved`) {
		t.Fatalf("ResolvePackageResources() error = %v, want selected-resource then rejection", err)
	}
}

func extractSignature(t testing.TB, source string) *toolbox.FuncSignature {
	t.Helper()
	files := fstest.MapFS{
		"tool.ts": &fstest.MapFile{Data: []byte(source), Mode: fs.FileMode(0o644)},
	}
	meta, err := toolbox.ExtractToolMetadata(context.Background(), toolbox.ExtractInput{Files: files, Entry: "tool.ts"})
	if err != nil {
		t.Fatalf("ExtractToolMetadata() error: %v", err)
	}
	if meta == nil || meta.Sig == nil {
		t.Fatal("ExtractToolMetadata() returned no signature")
	}
	return meta.Sig
}
