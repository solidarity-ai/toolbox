package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/solidarity-ai/toolbox/codemodesession"
)

func runRepl(cmd replCmd, stdin io.Reader, stdout, stderr io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	sessionDelegate, err := ensureSessionDaemon("codemode_repl", cwd, stderr)
	if err != nil {
		return err
	}
	defer sessionDelegate.Close()

	sqlitePath := cmd.File
	if strings.TrimSpace(sqlitePath) == "" {
		sqlitePath = ".toolbox-session"
	}
	if !filepath.IsAbs(sqlitePath) {
		sqlitePath = filepath.Join(cwd, sqlitePath)
	}

	ctx := context.Background()
	session, err := codemodesession.OpenSQLite(ctx, sqlitePath, cwd, codemodesession.SessionConfig{})
	if err != nil {
		return err
	}
	defer session.Close()
	consumer := combinedPreparedToolConsumer{session, sessionDelegate}
	if _, err := newFileToolsetBackend(ctx, cmd.Toolset, cmd.Effects, consumer); err != nil {
		return err
	}

	fmt.Fprintln(stdout, "toolbox repl started")
	fmt.Fprintf(stdout, "session=%s sqlite=%s resumed=%t language=ts\n", session.ID(), sqlitePath, session.Resumed())
	fmt.Fprintln(stdout, session.Instructions())

	scanner := bufio.NewScanner(stdin)
	for {
		fmt.Fprint(stdout, "toolbox[ts]> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return err
			}
			fmt.Fprintln(stdout)
			return nil
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		switch {
		case line == ":exit":
			fmt.Fprintln(stdout, "bye")
			return nil
		case line == ":help":
			fmt.Fprintln(stdout, session.Instructions())
		case line == ":submit":
			src, ok := readReplMultiline(scanner, stdout)
			if !ok {
				fmt.Fprintln(stdout, "submit cancelled")
				continue
			}
			_, _ = io.WriteString(stdout, session.Submit(ctx, src))
		case strings.HasPrefix(line, ":"):
			fmt.Fprintf(stdout, "unknown command: %s\n", line)
		default:
			_, _ = io.WriteString(stdout, session.Submit(ctx, line))
		}
	}
}

func readReplMultiline(scanner *bufio.Scanner, out io.Writer) (string, bool) {
	fmt.Fprintln(out, "enter TypeScript, end with a line containing only .end")
	var lines []string
	for {
		fmt.Fprint(out, "... ")
		if !scanner.Scan() {
			return "", false
		}
		line := scanner.Text()
		if strings.TrimSpace(line) == ".end" {
			break
		}
		lines = append(lines, line)
	}
	src := strings.TrimSpace(strings.Join(lines, "\n"))
	if src == "" {
		return "", false
	}
	return src, true
}
