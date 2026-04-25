package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/codemodesession"
)

func TestRunReplCreatesFreshTBSessionAndTypeScriptMode(t *testing.T) {
	calls := stubSessionDaemon(t)

	tempDir := t.TempDir()
	t.Setenv("TOOLBOX_SESSIONS_DIR", filepath.Join(tempDir, "sessions"))
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
	tbSession := parseTBSessionFromStdout(t, stdout.String())
	dbPath, err := codemodesession.SessionDBPath(tbSession)
	if err != nil {
		t.Fatalf("SessionDBPath(): %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("Stat(%q): %v", dbPath, err)
	}
	if !strings.Contains(stdout.String(), "super_tool submits a code cell to a notebook like environment") {
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
	if calls.Load() != 1 {
		t.Fatalf("ensureSessionDaemon() calls = %d, want 1", calls.Load())
	}
}

func TestRunReplResumesRequestedTBSession(t *testing.T) {
	calls := stubSessionDaemon(t)

	tempDir := t.TempDir()
	t.Setenv("TOOLBOX_SESSIONS_DIR", filepath.Join(tempDir, "sessions"))
	toolsetPath := writeToolsetFile(t, map[string]any{
		"packages": map[string]any{},
		"tools":    []any{},
	})

	var firstOut, firstErr bytes.Buffer
	if err := runWithIO([]string{"codemode", "repl", "-t", toolsetPath}, strings.NewReader("const value: number = 1\n:exit\n"), &firstOut, &firstErr); err != nil {
		t.Fatalf("first runWithIO() error: %v\nstdout=%s\nstderr=%s", err, firstOut.String(), firstErr.String())
	}
	if !strings.Contains(firstOut.String(), "resumed=false") {
		t.Fatalf("first stdout = %q, want resumed=false", firstOut.String())
	}
	tbSession := parseTBSessionFromStdout(t, firstOut.String())

	var secondOut, secondErr bytes.Buffer
	if err := runWithIO([]string{"codemode", "repl", "-t", toolsetPath, "--tb-session", tbSession}, strings.NewReader("value + 2\n:exit\n"), &secondOut, &secondErr); err != nil {
		t.Fatalf("second runWithIO() error: %v\nstdout=%s\nstderr=%s", err, secondOut.String(), secondErr.String())
	}
	if !strings.Contains(secondOut.String(), "resumed=true") {
		t.Fatalf("second stdout = %q, want resumed=true", secondOut.String())
	}
	if !strings.Contains(secondOut.String(), "=> 3") {
		t.Fatalf("second stdout = %q, want resumed completion preview", secondOut.String())
	}
	if calls.Load() != 2 {
		t.Fatalf("ensureSessionDaemon() calls = %d, want 2", calls.Load())
	}
}

func TestRunReplSupportsSubmitAndRejectsOtherCommands(t *testing.T) {
	calls := stubSessionDaemon(t)

	t.Setenv("TOOLBOX_SESSIONS_DIR", filepath.Join(t.TempDir(), "sessions"))
	toolsetPath := writeToolsetFile(t, map[string]any{
		"packages": map[string]any{},
		"tools":    []any{},
	})

	var stdout, stderr bytes.Buffer
	input := strings.NewReader(":submit\nconst value: number = 1\nvalue + 4\n.end\n:inspect nope\n:help\n:exit\n")
	if err := runWithIO([]string{"codemode", "repl", "-t", toolsetPath}, input, &stdout, &stderr); err != nil {
		t.Fatalf("runWithIO() error: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	if !strings.Contains(stdout.String(), "=> 5") {
		t.Fatalf("stdout = %q, want multiline submit result", stdout.String())
	}
	if !strings.Contains(stdout.String(), "unknown command: :inspect nope") {
		t.Fatalf("stdout = %q, want unknown-command rejection", stdout.String())
	}
	if !strings.Contains(stdout.String(), "// Notebook Input") {
		t.Fatalf("stdout = %q, want help output", stdout.String())
	}
	if !strings.Contains(stdout.String(), "inspect(x : any) : string") {
		t.Fatalf("stdout = %q, want help footer", stdout.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("ensureSessionDaemon() calls = %d, want 1", calls.Load())
	}
}

func TestRunReplInstructionsAliasPrintsInstructions(t *testing.T) {
	calls := stubSessionDaemon(t)

	t.Setenv("TOOLBOX_SESSIONS_DIR", filepath.Join(t.TempDir(), "sessions"))
	toolsetPath := writeToolsetFile(t, map[string]any{
		"packages": map[string]any{},
		"tools":    []any{},
	})

	var stdout, stderr bytes.Buffer
	input := strings.NewReader(":instructions\n:exit\n")
	if err := runWithIO([]string{"codemode", "repl", "-t", toolsetPath}, input, &stdout, &stderr); err != nil {
		t.Fatalf("runWithIO() error: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	if strings.Count(stdout.String(), "// Notebook Input") < 2 {
		t.Fatalf("stdout = %q, want instructions banner at startup and for :instructions", stdout.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("ensureSessionDaemon() calls = %d, want 1", calls.Load())
	}
}

func TestRunReplPrintsConsoleLogsAtEndOfSubmitOutput(t *testing.T) {
	calls := stubSessionDaemon(t)

	t.Setenv("TOOLBOX_SESSIONS_DIR", filepath.Join(t.TempDir(), "sessions"))
	toolsetPath := writeToolsetFile(t, map[string]any{
		"packages": map[string]any{},
		"tools":    []any{},
	})

	var stdout, stderr bytes.Buffer
	input := strings.NewReader("console.log(\"ok\", { a: 1 }); 1\n:exit\n")
	if err := runWithIO([]string{"codemode", "repl", "-t", toolsetPath}, input, &stdout, &stderr); err != nil {
		t.Fatalf("runWithIO() error: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	if !strings.Contains(stdout.String(), "--\nok [object Object]\n=> 1\n") {
		t.Fatalf("stdout = %q, want console logs footer after completion output", stdout.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("ensureSessionDaemon() calls = %d, want 1", calls.Load())
	}
}

func TestRunReplAwaitApprovalsReturnsNoOutstandingWhenIdle(t *testing.T) {
	calls := stubSessionDaemon(t)

	t.Setenv("TOOLBOX_SESSIONS_DIR", filepath.Join(t.TempDir(), "sessions"))
	toolsetPath := writeToolsetFile(t, map[string]any{
		"packages": map[string]any{},
		"tools":    []any{},
	})

	var stdout, stderr bytes.Buffer
	input := strings.NewReader(":await_approvals\n:exit\n")
	if err := runWithIO([]string{"codemode", "repl", "-t", toolsetPath}, input, &stdout, &stderr); err != nil {
		t.Fatalf("runWithIO() error: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	if !strings.Contains(stdout.String(), "(no outstanding approvals).") {
		t.Fatalf("stdout = %q, want no-outstanding approvals output", stdout.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("ensureSessionDaemon() calls = %d, want 1", calls.Load())
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

func parseTBSessionFromStdout(t testing.TB, stdout string) string {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "tb_session=") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			break
		}
		return strings.TrimPrefix(fields[0], "tb_session=")
	}
	t.Fatalf("stdout = %q, want tb_session line", stdout)
	return ""
}
