package main

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"github.com/solidarity-ai/toolbox/codemodemcp"
	"github.com/solidarity-ai/toolbox/mcpserver"
	"github.com/solidarity-ai/toolbox/sdkbridge"
	"github.com/solidarity-ai/toolbox/toolsetctl"
)

type sdkBridgeServer interface {
	ReloadFileBackedToolsets(context.Context) error
	ServeStdio(context.Context, io.Reader, io.Writer) error
}

var newSDKBridgeServer = func(opts sdkbridge.Options) sdkBridgeServer {
	return sdkbridge.New(opts)
}

func runMCP(cmd mcpCmd, opts secretStoreOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	sessionDelegate, err := ensureSessionDaemon("mcp", cwd, stderr)
	if err != nil {
		return err
	}
	defer sessionDelegate.Close()

	managed := mcpserver.NewManagedNamed(mcpServerNameForToolsetPath(cmd.Toolset))
	consumer := combinedPreparedToolConsumer{managed, sessionDelegate}
	backend, err := newFileToolsetBackend(context.Background(), cmd.Toolset, cmd.Effects, opts, consumer)
	if err != nil {
		return err
	}
	bindSecretEpochReload(sessionDelegate, stderr, func() error {
		_, err := backend.Reload(context.Background())
		return err
	})

	stdioServer := mcpgoserver.NewStdioServer(managed.Server())
	stdioServer.SetErrorLogger(log.New(stderr, "", log.LstdFlags))
	return stdioServer.Listen(context.Background(), stdin, stdout)
}

func runCodemodeMCP(cmd mcpCmd, opts secretStoreOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	sessionDelegate, err := ensureSessionDaemon("codemode_mcp", cwd, stderr)
	if err != nil {
		return err
	}
	defer sessionDelegate.Close()

	managed, err := codemodemcp.OpenManagedNamed(context.Background(), mcpServerNameForToolsetPath(cmd.Toolset), cwd)
	if err != nil {
		return err
	}
	defer managed.Close()

	consumer := combinedPreparedToolConsumer{managed, sessionDelegate}
	backend, err := newFileToolsetBackend(context.Background(), cmd.Toolset, cmd.Effects, opts, consumer)
	if err != nil {
		return err
	}
	bindSecretEpochReload(sessionDelegate, stderr, func() error {
		_, err := backend.Reload(context.Background())
		return err
	})

	stdioServer := mcpgoserver.NewStdioServer(managed.Server())
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

func runSDKBridgeServeStdio(opts secretStoreOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	sessionDelegate, err := ensureSessionDaemon("sdkbridge", cwd, stderr)
	if err != nil {
		return err
	}
	defer sessionDelegate.Close()
	resolver, err := newResolver()
	if err != nil {
		return err
	}
	repo := newCredentialRepository(opts)

	bridge := newSDKBridgeServer(sdkbridge.Options{
		Resolver:               resolver,
		CredentialPolicySource: repo,
		CredentialRepository:   repo,
		SearchClientFactory:    func() (toolsetctl.SearchClient, error) { return newToolRegistrySearchClient() },
		PreparedToolsConsumer:  sessionDelegate,
	})
	bindSecretEpochReload(sessionDelegate, stderr, func() error {
		return bridge.ReloadFileBackedToolsets(context.Background())
	})
	return bridge.ServeStdio(context.Background(), stdin, stdout)
}
