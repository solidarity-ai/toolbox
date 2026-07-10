package toolpkgdiscovery

import (
	"strings"
	"testing"
)

func TestInspectDescriptionMentionsVersionRequirement(t *testing.T) {
	templates, err := builtinToolTemplates()
	if err != nil {
		t.Fatalf("builtinToolTemplates: %v", err)
	}
	for _, tmpl := range templates {
		if tmpl.Name != "toolbox.inspect" {
			continue
		}
		if !strings.Contains(tmpl.Description, `"@version" suffix is required`) {
			t.Fatalf("inspect description = %q, want @version requirement mentioned", tmpl.Description)
		}
		return
	}
	t.Fatal("toolbox.inspect template not found")
}
