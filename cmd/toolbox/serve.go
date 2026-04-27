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

func runMCP(cmd mcpCmd, opts secretStoreOptions, stdin io.Reader, stdout, stderr io.Writer) (err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	debugLog := startMCPDebugLog("mcp", cmd.Toolset, cwd, stderr)
	defer func() {
		debugLog.Close(err)
	}()
	stderr = debugLog.ErrorWriter()

	sessionDelegate, err := ensureSessionDaemon("mcp", cwd, stderr)
	if err != nil {
		return err
	}
	defer sessionDelegate.Close()
	debugLog.Logf("daemon session registered mode=mcp")

	managed := mcpserver.NewManagedNamed(mcpServerNameForToolsetPath(cmd.Toolset))
	consumer := combinedPreparedToolConsumer{managed, sessionDelegate}
	backend, err := newFileToolsetBackend(context.Background(), cmd.Toolset, cmd.Effects, opts, consumer)
	if err != nil {
		return err
	}
	debugLog.Logf("toolset loaded toolset=%q effects=%q", cmd.Toolset, cmd.Effects)
	bindSecretEpochReload(sessionDelegate, stderr, func() error {
		debugLog.Logf("secret epoch changed; reloading toolset")
		_, err := backend.Reload(context.Background())
		if err != nil {
			debugLog.Logf("toolset reload error: %v", err)
		} else {
			debugLog.Logf("toolset reloaded")
		}
		return err
	})

	stdioServer := mcpgoserver.NewStdioServer(managed.Server())
	stdioServer.SetErrorLogger(log.New(stderr, "", log.LstdFlags))
	debugLog.Logf("stdio listen starting")
	return stdioServer.Listen(context.Background(), stdin, stdout)
}

func runCodemodeMCP(cmd mcpCmd, opts secretStoreOptions, stdin io.Reader, stdout, stderr io.Writer) (err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	debugLog := startMCPDebugLog("codemode_mcp", cmd.Toolset, cwd, stderr)
	defer func() {
		debugLog.Close(err)
	}()
	stderr = debugLog.ErrorWriter()

	sessionDelegate, err := ensureSessionDaemon("codemode_mcp", cwd, stderr)
	if err != nil {
		return err
	}
	defer sessionDelegate.Close()
	debugLog.Logf("daemon session registered mode=codemode_mcp")

	var managed *codemodemcp.ManagedServer
	if strings.TrimSpace(cmd.TBSession) == "" {
		managed, err = codemodemcp.OpenManagedNamed(context.Background(), mcpServerNameForToolsetPath(cmd.Toolset), cwd)
	} else {
		managed, err = codemodemcp.OpenManagedBoundNamed(context.Background(), mcpServerNameForToolsetPath(cmd.Toolset), cwd, cmd.TBSession)
		if err == nil && stderr != nil {
			_, _ = io.WriteString(stderr, "toolbox codemode mcp tb_session="+strings.TrimSpace(cmd.TBSession)+" locked=true\n")
		}
	}
	if err != nil {
		return err
	}
	defer managed.Close()
	debugLog.Logf("codemode manager opened locked=%t bound_tb_session=%q", strings.TrimSpace(cmd.TBSession) != "", strings.TrimSpace(cmd.TBSession))
	syncSessionBinding(sessionDelegate, managed)
	bindApprovalExecution(sessionDelegate, stderr, managed, func() error {
		return syncPendingApprovals(context.Background(), managed, sessionDelegate, stderr)
	}, debugLog.Logf)
	managed.SetAfterChange(func() {
		debugLog.Logf("codemode state changed; syncing daemon session")
		syncSessionBinding(sessionDelegate, managed)
		_ = syncPendingApprovals(context.Background(), managed, sessionDelegate, stderr)
	})
	syncSessionBinding(sessionDelegate, managed)
	_ = syncPendingApprovals(context.Background(), managed, sessionDelegate, stderr)

	consumer := combinedPreparedToolConsumer{managed, sessionDelegate}
	backend, err := newFileToolsetBackend(context.Background(), cmd.Toolset, cmd.Effects, opts, consumer)
	if err != nil {
		return err
	}
	debugLog.Logf("toolset loaded toolset=%q effects=%q", cmd.Toolset, cmd.Effects)
	bindSecretEpochReload(sessionDelegate, stderr, func() error {
		debugLog.Logf("secret epoch changed; reloading codemode toolset")
		_, err := backend.Reload(context.Background())
		if err != nil {
			debugLog.Logf("codemode toolset reload error: %v", err)
		} else {
			debugLog.Logf("codemode toolset reloaded")
		}
		return err
	})

	stdioServer := mcpgoserver.NewStdioServer(managed.Server())
	stdioServer.SetErrorLogger(log.New(stderr, "", log.LstdFlags))
	debugLog.Logf("stdio listen starting")
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
