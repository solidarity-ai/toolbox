package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/solidarity-ai/toolbox/daemon"
)

type mcpDebugLog struct {
	path       string
	file       *os.File
	logger     *log.Logger
	errWriter  io.Writer
	startedAt  time.Time
	serverKind string
}

func startMCPDebugLog(serverKind, toolsetPath, cwd string, stderr io.Writer) *mcpDebugLog {
	out := &mcpDebugLog{
		errWriter:  stderr,
		startedAt:  time.Now(),
		serverKind: strings.TrimSpace(serverKind),
	}
	if stderr == nil {
		out.errWriter = io.Discard
	}

	logDir, err := mcpDebugLogDir()
	if err != nil {
		_, _ = fmt.Fprintf(out.errWriter, "toolbox mcp debug log unavailable: %v\n", err)
		return out
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		_, _ = fmt.Fprintf(out.errWriter, "toolbox mcp debug log unavailable: create %s: %v\n", logDir, err)
		return out
	}

	base := strings.TrimSpace(filepath.Base(toolsetPath))
	if base == "" || base == "." {
		base = "toolbox"
	}
	filename := fmt.Sprintf(
		"%s-%s-pid%d-%s.log",
		out.startedAt.UTC().Format("20060102T150405Z"),
		sanitizeLogName(serverKind),
		os.Getpid(),
		sanitizeLogName(strings.TrimSuffix(strings.TrimSuffix(base, ".json"), ".toolset")),
	)
	path := filepath.Join(logDir, filename)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		_, _ = fmt.Fprintf(out.errWriter, "toolbox mcp debug log unavailable: open %s: %v\n", path, err)
		return out
	}

	out.path = path
	out.file = file
	out.logger = log.New(file, "", log.LstdFlags|log.Lmicroseconds|log.LUTC)
	out.errWriter = io.MultiWriter(out.errWriter, file)
	_ = os.WriteFile(filepath.Join(logDir, "latest-"+sanitizeLogName(serverKind)+".path"), []byte(path+"\n"), 0o600)

	out.Logf("start kind=%s pid=%d cwd=%q toolset=%q", serverKind, os.Getpid(), cwd, toolsetPath)
	return out
}

func (l *mcpDebugLog) ErrorWriter() io.Writer {
	if l == nil || l.errWriter == nil {
		return io.Discard
	}
	return l.errWriter
}

func (l *mcpDebugLog) Logf(format string, args ...any) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Printf(format, args...)
}

func (l *mcpDebugLog) Close(err error) {
	if l == nil || l.file == nil {
		return
	}
	if err != nil {
		l.Logf("exit kind=%s elapsed=%s error=%v", l.serverKind, time.Since(l.startedAt).Round(time.Millisecond), err)
	} else {
		l.Logf("exit kind=%s elapsed=%s", l.serverKind, time.Since(l.startedAt).Round(time.Millisecond))
	}
	_ = l.file.Close()
	l.file = nil
}

func mcpDebugLogDir() (string, error) {
	dir, err := daemon.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "logs", "mcp"), nil
}

func sanitizeLogName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "toolbox"
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-._")
	if out == "" {
		return "toolbox"
	}
	return out
}

func runDaemonLogs(cmd daemonLogsCmd, stdout io.Writer) error {
	path, err := daemon.LogPath()
	if cmd.MCP {
		path, err = latestMCPDebugLogPath()
	}
	if err != nil {
		return err
	}
	if cmd.Path {
		_, err := fmt.Fprintln(stdout, path)
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "==> %s <==\n", path); err != nil {
		return err
	}
	return writeTail(stdout, path, cmd.Tail)
}

func latestMCPDebugLogPath() (string, error) {
	dir, err := mcpDebugLogDir()
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no MCP debug logs found in %s", dir)
		}
		return "", err
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("no MCP debug logs found in %s", dir)
	}
	sort.Slice(paths, func(i, j int) bool {
		left, leftErr := os.Stat(paths[i])
		right, rightErr := os.Stat(paths[j])
		if leftErr == nil && rightErr == nil && !left.ModTime().Equal(right.ModTime()) {
			return left.ModTime().After(right.ModTime())
		}
		return paths[i] > paths[j]
	})
	return paths[0], nil
}

func writeTail(stdout io.Writer, path string, lineCount int) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if lineCount > 0 && len(lines) > lineCount {
		lines = lines[len(lines)-lineCount:]
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return nil
}
