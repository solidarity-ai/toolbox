package tswasmcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
)

type Request struct {
	WasmPath    string // Path to WASM file on disk (mutually exclusive with WasmBytes).
	WasmBytes   []byte // WASM binary content piped via stdin (mutually exclusive with WasmPath).
	Args        []string
	Runtime     string // "wasix-cli" or "wasip2-cli" (required).
	VFSSockPath string // Unix socket for shared VFS proxy (optional).
}

type Result struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

func Run(request Request) (Result, error) {
	return RunContext(context.Background(), request)
}

func RunContext(ctx context.Context, request Request) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if request.WasmPath == "" && len(request.WasmBytes) == 0 {
		return Result{}, fmt.Errorf("missing wasm path or bytes")
	}

	if request.Runtime == "" {
		return Result{}, fmt.Errorf("missing runtime")
	}

	wasmArg := request.WasmPath
	if len(request.WasmBytes) > 0 {
		wasmArg = "-"
	}

	cmdArgs := []string{"--runtime", request.Runtime, wasmArg}
	cmdArgs = append(cmdArgs, request.Args...)

	cmd := exec.CommandContext(ctx, resolveHostBinaryPath(), cmdArgs...)

	if request.VFSSockPath != "" {
		cmd.Env = append(cmd.Environ(), "TOOLBOX_VFS_SOCK="+request.VFSSockPath)
	}

	if len(request.WasmBytes) > 0 {
		cmd.Stdin = bytes.NewReader(request.WasmBytes)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err == nil {
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}

	return Result{}, fmt.Errorf("run wasmcli-sandbox: %w", err)
}

func resolveHostBinaryPath() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "wasmcli-sandbox"
	}

	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "wasmcli-sandbox", "target", "debug", "wasmcli-sandbox"))
}

// ResolveHostBinaryPathForTest exposes the host binary path for integration tests.
func ResolveHostBinaryPathForTest() string {
	return resolveHostBinaryPath()
}
