package main

import (
	"context"
	"io"
	"log"

	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"github.com/solidarity-ai/toolbox/mcpserver"
	"github.com/solidarity-ai/toolbox/sdkbridge"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetfile"
)

func runMCP(cmd mcpCmd, stdin io.Reader, stdout, stderr io.Writer) error {
	resolver, err := newResolver()
	if err != nil {
		return err
	}
	ts, err := toolsetfile.Load(cmd.Toolset)
	if err != nil {
		return err
	}

	prepared, err := ts.Prepare(context.Background(), resolver, toolset.Config{
		CredentialPolicySource: newCredentialPolicySource(),
	})
	if err != nil {
		return err
	}

	stdioServer := mcpgoserver.NewStdioServer(mcpserver.New(prepared))
	stdioServer.SetErrorLogger(log.New(stderr, "", log.LstdFlags))
	return stdioServer.Listen(context.Background(), stdin, stdout)
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
