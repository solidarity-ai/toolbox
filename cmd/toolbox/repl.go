package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/solidarity-ai/toolbox/codemodesession"
)

func runRepl(cmd replCmd, opts secretStoreOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	sessionDelegate, err := ensureSessionDaemon("codemode_repl", cwd, stderr)
	if err != nil {
		return err
	}
	defer sessionDelegate.Close()

	ctx := context.Background()
	session, err := openReplSession(ctx, strings.TrimSpace(cmd.TBSession), cwd)
	if err != nil {
		return err
	}
	defer session.Close()
	setSessionBinding(sessionDelegate, session.TBSession(), true)
	bindApprovalExecution(sessionDelegate, stderr, session, func() error {
		return syncPendingApprovals(context.Background(), session, sessionDelegate, stderr)
	})
	_ = syncPendingApprovals(context.Background(), session, sessionDelegate, stderr)
	consumer := combinedPreparedToolConsumer{session, sessionDelegate}
	backend, err := newFileToolsetBackend(ctx, cmd.Toolset, cmd.Effects, opts, consumer)
	if err != nil {
		return err
	}
	bindSecretEpochReload(sessionDelegate, stderr, func() error {
		_, err := backend.Reload(context.Background())
		return err
	})

	fmt.Fprintln(stdout, "toolbox repl started")
	fmt.Fprintf(stdout, "tb_session=%s repl_session=%s resumed=%t language=ts\n", session.TBSession(), session.ID(), session.Resumed())
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
		case line == ":help", line == ":instructions":
			fmt.Fprintln(stdout, session.Instructions())
		case line == ":await_approvals":
			result, err := session.AwaitNextApproval(ctx)
			if err != nil {
				fmt.Fprintf(stdout, "await approvals error: %v\n", err)
			} else {
				fmt.Fprintln(stdout, result.Text())
			}
			_ = syncPendingApprovals(context.Background(), session, sessionDelegate, stderr)
		case line == ":submit":
			src, ok := readReplMultiline(scanner, stdout)
			if !ok {
				fmt.Fprintln(stdout, "submit cancelled")
				continue
			}
			_, _ = io.WriteString(stdout, session.Submit(ctx, src))
			_ = syncPendingApprovals(context.Background(), session, sessionDelegate, stderr)
		case strings.HasPrefix(line, ":"):
			fmt.Fprintf(stdout, "unknown command: %s\n", line)
		default:
			_, _ = io.WriteString(stdout, session.Submit(ctx, line))
			_ = syncPendingApprovals(context.Background(), session, sessionDelegate, stderr)
		}
	}
}

func runCodemodeSessionNew(stdout io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	session, err := codemodesession.CreateFresh(context.Background(), cwd, codemodesession.SessionConfig{})
	if err != nil {
		return err
	}
	defer session.Close()
	_, err = fmt.Fprintln(stdout, session.TBSession())
	return err
}

func openReplSession(ctx context.Context, tbSession, cwd string) (*codemodesession.Session, error) {
	if strings.TrimSpace(tbSession) == "" {
		return codemodesession.CreateFresh(ctx, cwd, codemodesession.SessionConfig{})
	}
	return codemodesession.OpenExisting(ctx, tbSession, cwd, codemodesession.SessionConfig{})
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
