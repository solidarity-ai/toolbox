package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestPackagePackHelp(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=TestPackagePackHelpProcess", "--", "package", "pack", "--help")
	command.Env = append(os.Environ(), "TOOLBOX_PACKAGE_HELP_PROCESS=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("package pack --help: %v\n%s", err, output)
	}
	for _, want := range []string{"[<package-dir>]", `--out="."`} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("help output does not contain %q:\n%s", want, output)
		}
	}
}

func TestPackagePackHelpProcess(t *testing.T) {
	if os.Getenv("TOOLBOX_PACKAGE_HELP_PROCESS") != "1" {
		return
	}
	if err := run(os.Args[len(os.Args)-3:], os.Stdout, os.Stderr); err != nil {
		t.Fatal(err)
	}
}

func TestPackagePackDefaultsToCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	oldDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDirectory) })

	var stdout, stderr bytes.Buffer
	err = run([]string{"package", "pack"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "released Toolbox build") {
		t.Fatalf("package pack error = %v, want unreleased-build rejection", err)
	}
}

func TestPackPackageCreatesArtifactsInRequestedOutputDirectory(t *testing.T) {
	source := t.TempDir()
	out := filepath.Join(t.TempDir(), "dist")
	writePackageTestFile(t, filepath.Join(source, "toolbox.devpkg.json"), `{
  "module": "example.com/greeter",
  "name": "greeter",
  "runtime": "typescript-sandbox",
  "tools": [{"entry_ts":"tools/greet.ts","effect":"readOnly","idempotent":true}]
}`)
	writePackageTestFile(t, filepath.Join(source, "tools", "greet.ts"), `
/** Greet somebody by name. */
export default function greet(input: {name: string}): {message: string} {
  return {message: "Hello " + input.name};
}`)

	var stdout bytes.Buffer
	err := packPackage(packagePackCmd{Directory: source, Out: out}, tooldef.Version("v1.2.3"), true, &stdout)
	if err != nil {
		t.Fatalf("packPackage: %v", err)
	}
	for _, name := range []string{"greeter.toolbox.pkg", "toolbox.pkg.json"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("expected %s: %v", name, err)
		}
	}
	if !strings.Contains(stdout.String(), "archive:") || !strings.Contains(stdout.String(), "manifest:") {
		t.Fatalf("output does not list both artifacts:\n%s", stdout.String())
	}
}

func TestPackPackageRejectsUnreleasedBuild(t *testing.T) {
	err := packPackage(packagePackCmd{Directory: ".", Out: "."}, "", false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "released Toolbox build") {
		t.Fatalf("packPackage error = %v, want unreleased-build rejection", err)
	}
}

func writePackageTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
