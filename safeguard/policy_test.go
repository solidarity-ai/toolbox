package safeguard

import (
	"errors"
	"testing"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestPolicyChecksExactRevocations(t *testing.T) {
	policy := Policy{
		SchemaVersion: SchemaVersion,
		Toolbox:       []ToolboxRevocation{{Version: "v1.0.0", Reason: "upgrade immediately"}},
		Packages: []PackageRevocation{{
			Module:  "github.com/include-tools/example",
			Version: "v1.2.3",
			Reason:  "credential leak",
		}},
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	for name, err := range map[string]error{
		"toolbox": policy.CheckToolbox("v1.0.0"),
		"package": policy.CheckPackage("github.com/Include-Tools/Example", "v1.2.3"),
	} {
		var blocked *BlockedError
		if !errors.As(err, &blocked) {
			t.Fatalf("%s error = %v, want *BlockedError", name, err)
		}
	}

	if err := policy.CheckToolbox("v1.0.1"); err != nil {
		t.Fatalf("unlisted toolbox error = %v", err)
	}
	if err := policy.CheckPackage(tooldef.ModulePath("github.com/include-tools/example"), tooldef.Version("v1.2.4")); err != nil {
		t.Fatalf("unlisted package error = %v", err)
	}
}

func TestPolicyValidateRejectsMalformedEntry(t *testing.T) {
	policy := Policy{
		SchemaVersion: SchemaVersion,
		Packages:      []PackageRevocation{{Module: "not-a-module", Version: "latest", Reason: "bad"}},
	}
	if err := policy.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want malformed entry error")
	}
}
