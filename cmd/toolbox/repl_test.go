package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReplUsesDefaultSQLitePathAndTypeScriptMode(t *testing.T) {
	tempDir := t.TempDir()
	withWorkingDir(t, tempDir)
	writeJSONFile(t, filepath.Join(tempDir, defaultToolsetFilename), map[string]any{
		"packages": map[string]any{},
		"tools":    []any{},
	})

	var stdout, stderr bytes.Buffer
	input := strings.NewReader("const value: number = 1\nvalue + 1\n:exit\n")
	if err := runWithIO([]string{"codemode", "repl"}, input, &stdout, &stderr); err != nil {
		t.Fatalf("runWithIO() error: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(tempDir, ".toolbox-session")); err != nil {
		t.Fatalf("Stat(.toolbox-session): %v", err)
	}
	if !strings.Contains(stdout.String(), "super_tool submits a code cell to a REPL") {
		t.Fatalf("stdout = %q, want super_tool instructions", stdout.String())
	}
	if !strings.Contains(stdout.String(), "$pkgMetadata") {
		t.Fatalf("stdout = %q, want pkgMetadata example", stdout.String())
	}
	if !strings.Contains(stdout.String(), "declare const $pkgMetadata: Record<string, {") {
		t.Fatalf("stdout = %q, want pkgMetadata d.ts", stdout.String())
	}
	if !strings.Contains(stdout.String(), "$last : any") {
		t.Fatalf("stdout = %q, want $last declaration", stdout.String())
	}
	if !strings.Contains(stdout.String(), "$val(index : number) : any") {
		t.Fatalf("stdout = %q, want $val declaration", stdout.String())
	}
	if !strings.Contains(stdout.String(), "cell 1") {
		t.Fatalf("stdout = %q, want first cell header", stdout.String())
	}
	if !strings.Contains(stdout.String(), "=> 2") {
		t.Fatalf("stdout = %q, want completion preview for second cell", stdout.String())
	}
}

func TestRunReplResumesLatestSessionFromFileFlag(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "custom.toolbox-session")
	toolsetPath := writeToolsetFile(t, map[string]any{
		"packages": map[string]any{},
		"tools":    []any{},
	})

	var firstOut, firstErr bytes.Buffer
	if err := runWithIO([]string{"codemode", "repl", "-t", toolsetPath, "-f", dbPath}, strings.NewReader("const value: number = 1\n:exit\n"), &firstOut, &firstErr); err != nil {
		t.Fatalf("first runWithIO() error: %v\nstdout=%s\nstderr=%s", err, firstOut.String(), firstErr.String())
	}
	if !strings.Contains(firstOut.String(), "resumed=false") {
		t.Fatalf("first stdout = %q, want resumed=false", firstOut.String())
	}

	var secondOut, secondErr bytes.Buffer
	if err := runWithIO([]string{"codemode", "repl", "-t", toolsetPath, "-f", dbPath}, strings.NewReader("value + 2\n:exit\n"), &secondOut, &secondErr); err != nil {
		t.Fatalf("second runWithIO() error: %v\nstdout=%s\nstderr=%s", err, secondOut.String(), secondErr.String())
	}
	if !strings.Contains(secondOut.String(), "resumed=true") {
		t.Fatalf("second stdout = %q, want resumed=true", secondOut.String())
	}
	if !strings.Contains(secondOut.String(), "=> 3") {
		t.Fatalf("second stdout = %q, want resumed completion preview", secondOut.String())
	}
}

func TestRunReplSupportsSubmitAndRejectsOtherCommands(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "submit.toolbox-session")
	toolsetPath := writeToolsetFile(t, map[string]any{
		"packages": map[string]any{},
		"tools":    []any{},
	})

	var stdout, stderr bytes.Buffer
	input := strings.NewReader(":submit\nconst value: number = 1\nvalue + 4\n.end\n:inspect nope\n:help\n:exit\n")
	if err := runWithIO([]string{"codemode", "repl", "-t", toolsetPath, "-f", dbPath}, input, &stdout, &stderr); err != nil {
		t.Fatalf("runWithIO() error: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	if !strings.Contains(stdout.String(), "=> 5") {
		t.Fatalf("stdout = %q, want multiline submit result", stdout.String())
	}
	if !strings.Contains(stdout.String(), "unknown command: :inspect nope") {
		t.Fatalf("stdout = %q, want unknown-command rejection", stdout.String())
	}
	if !strings.Contains(stdout.String(), "// REPL input") {
		t.Fatalf("stdout = %q, want help output", stdout.String())
	}
	if !strings.Contains(stdout.String(), "inspect(x : any) : string") {
		t.Fatalf("stdout = %q, want help footer", stdout.String())
	}
}

func TestRunReplPrintsConsoleLogsAtEndOfSubmitOutput(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "logs.toolbox-session")
	toolsetPath := writeToolsetFile(t, map[string]any{
		"packages": map[string]any{},
		"tools":    []any{},
	})

	var stdout, stderr bytes.Buffer
	input := strings.NewReader("console.log(\"ok\", { a: 1 }); 1\n:exit\n")
	if err := runWithIO([]string{"codemode", "repl", "-t", toolsetPath, "-f", dbPath}, input, &stdout, &stderr); err != nil {
		t.Fatalf("runWithIO() error: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	if !strings.Contains(stdout.String(), "--\nok [object Object]\n=> 1\n") {
		t.Fatalf("stdout = %q, want console logs footer after completion output", stdout.String())
	}
}

func withWorkingDir(t *testing.T, dir string) {
	t.Helper()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q): %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatalf("restore working directory to %q: %v", oldwd, err)
		}
	})
}
