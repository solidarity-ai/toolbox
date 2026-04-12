package main

import (
	"context"
	"io"
	"log"
	"path/filepath"
	"strings"

	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"github.com/solidarity-ai/toolbox/codemodemcp"
	"github.com/solidarity-ai/toolbox/codemodesession"
	"github.com/solidarity-ai/toolbox/mcpserver"
	"github.com/solidarity-ai/toolbox/sdkbridge"
)

func runMCP(cmd mcpCmd, stdin io.Reader, stdout, stderr io.Writer) error {
	prepared, err := loadPreparedToolset(context.Background(), cmd.Toolset)
	if err != nil {
		return err
	}

	stdioServer := mcpgoserver.NewStdioServer(mcpserver.NewNamed(mcpServerNameForToolsetPath(cmd.Toolset), prepared))
	stdioServer.SetErrorLogger(log.New(stderr, "", log.LstdFlags))
	return stdioServer.Listen(context.Background(), stdin, stdout)
}

func runCodemodeMCP(cmd mcpCmd, stdin io.Reader, stdout, stderr io.Writer) error {
	prepared, err := loadPreparedToolset(context.Background(), cmd.Toolset)
	if err != nil {
		return err
	}

	stdioServer := mcpgoserver.NewStdioServer(codemodemcp.NewNamed(
		mcpServerNameForToolsetPath(cmd.Toolset),
		codemodesession.SessionConfig{PreparedTools: prepared},
	))
	stdioServer.SetErrorLogger(log.New(stderr, "", log.LstdFlags))
	return stdioServer.Listen(context.Background(), stdin, stdout)
}

func mcpServerNameForToolsetPath(path string) string {
	name := filepath.Base(strings.TrimSpace(path))
	if name == "" || name == "." {
		return "toolbox"
	}

	name = strings.TrimSuffix(name, ".json")
	name = strings.TrimSuffix(name, ".toolset")
	name = strings.TrimSpace(name)
	if name == "" || name == "." {
		return "toolbox"
	}
	return name
}

func runSDKBridgeServeStdio(stdin io.Reader, stdout, stderr io.Writer) error {
	_ = stderr
	resolver, err := newResolver()
	if err != nil {
		return err
	}

	bridge := sdkbridge.New(sdkbridge.Options{
		Resolver:               resolver,
		CredentialPolicySource: newCredentialPolicySource(),
	})
	return bridge.ServeStdio(context.Background(), stdin, stdout)
}
