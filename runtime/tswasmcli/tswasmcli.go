package tswasmcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	guestEnvControlVar    = "TOOLBOX_GUEST_ENV_JSON"
	guestMountsControlVar = "TOOLBOX_GUEST_MOUNTS_JSON"
	vfsSockControlVar     = "TOOLBOX_VFS_SOCK"
	proxyHostsGuestPath   = "/etc/hosts"
	proxyVirtualHostname  = "proxy.toolbox.internal"
)

type Mount struct {
	HostPath  string `json:"hostPath"`
	GuestPath string `json:"guestPath"`
}

type Request struct {
	WasmPath    string // Path to WASM file on disk (mutually exclusive with WasmBytes).
	WasmBytes   []byte // WASM binary content piped via stdin (mutually exclusive with WasmPath).
	Args        []string
	Runtime     string            // "wasix-cli" or "wasip2-cli" (required).
	VFSSockPath string            // Unix socket for shared VFS proxy (optional).
	Env         map[string]string // Guest-visible env vars (optional).
	Mounts      []Mount           // Additional guest-visible directory mounts (optional).
}

type Result struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

var commandFactory = exec.Command

func Run(request Request) (Result, error) {
	if request.WasmPath == "" && len(request.WasmBytes) == 0 {
		return Result{}, fmt.Errorf("missing wasm path or bytes")
	}

	if request.Runtime == "" {
		return Result{}, fmt.Errorf("missing runtime")
	}

	if err := validateRequest(request); err != nil {
		return Result{}, err
	}

	wasmArg := request.WasmPath
	if len(request.WasmBytes) > 0 {
		wasmArg = "-"
	}

	cmdArgs := []string{"--runtime", request.Runtime, wasmArg}
	cmdArgs = append(cmdArgs, request.Args...)

	cmd := commandFactory(resolveHostBinaryPath(), cmdArgs...)

	env, err := commandEnv(request)
	if err != nil {
		return Result{}, err
	}
	cmd.Env = env

	if len(request.WasmBytes) > 0 {
		cmd.Stdin = bytes.NewReader(request.WasmBytes)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()

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

func validateRequest(request Request) error {
	for i, mount := range request.Mounts {
		if strings.TrimSpace(mount.HostPath) == "" {
			return fmt.Errorf("mount %d: missing host path", i)
		}
		if strings.TrimSpace(mount.GuestPath) == "" {
			return fmt.Errorf("mount %d: missing guest path", i)
		}
		if !filepath.IsAbs(mount.HostPath) {
			return fmt.Errorf("mount %d: host path must be absolute: %s", i, mount.HostPath)
		}
		if !strings.HasPrefix(mount.GuestPath, "/") {
			return fmt.Errorf("mount %d: guest path must be absolute: %s", i, mount.GuestPath)
		}
		info, err := os.Stat(mount.HostPath)
		if err != nil {
			return fmt.Errorf("mount %d: stat host path %s: %w", i, mount.HostPath, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("mount %d: host path must be a directory: %s", i, mount.HostPath)
		}
	}

	httpsProxy := strings.TrimSpace(request.Env["HTTPS_PROXY"])
	sslCertFile := strings.TrimSpace(request.Env["SSL_CERT_FILE"])
	if httpsProxy == "" && sslCertFile == "" {
		return nil
	}
	if httpsProxy == "" || sslCertFile == "" {
		return fmt.Errorf("proxy configuration requires both HTTPS_PROXY and SSL_CERT_FILE")
	}
	if !guestPathCoveredByMounts(sslCertFile, request.Mounts) {
		return fmt.Errorf("proxy configuration requires a mounted guest path for SSL_CERT_FILE=%s", sslCertFile)
	}
	proxyURL, err := url.Parse(httpsProxy)
	if err != nil {
		return fmt.Errorf("proxy configuration: parse HTTPS_PROXY: %w", err)
	}
	if strings.EqualFold(proxyURL.Hostname(), proxyVirtualHostname) && !guestPathCoveredByMounts(proxyHostsGuestPath, request.Mounts) {
		return fmt.Errorf("proxy configuration requires a mounted guest path for %s when HTTPS_PROXY uses %s", proxyHostsGuestPath, proxyVirtualHostname)
	}
	return nil
}

func guestPathCoveredByMounts(guestPath string, mounts []Mount) bool {
	cleanGuestPath := pathCleanForGuest(guestPath)
	for _, mount := range mounts {
		mountRoot := pathCleanForGuest(mount.GuestPath)
		if cleanGuestPath == mountRoot || strings.HasPrefix(cleanGuestPath, mountRoot+"/") {
			return true
		}
	}
	return false
}

func pathCleanForGuest(path string) string {
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." {
		return "/"
	}
	if !strings.HasPrefix(clean, "/") {
		return "/" + clean
	}
	return clean
}

func commandEnv(request Request) ([]string, error) {
	env := append([]string{}, os.Environ()...)
	if request.VFSSockPath != "" {
		env = append(env, vfsSockControlVar+"="+request.VFSSockPath)
	}
	if len(request.Env) > 0 {
		payload, err := json.Marshal(request.Env)
		if err != nil {
			return nil, fmt.Errorf("marshal guest env: %w", err)
		}
		env = append(env, guestEnvControlVar+"="+string(payload))
		hostEnv, err := hostRuntimeEnv(request.Env, request.Mounts)
		if err != nil {
			return nil, err
		}
		for _, key := range SortedEnvKeysForTest(hostEnv) {
			env = append(env, key+"="+hostEnv[key])
		}
	}
	if len(request.Mounts) > 0 {
		payload, err := json.Marshal(request.Mounts)
		if err != nil {
			return nil, fmt.Errorf("marshal guest mounts: %w", err)
		}
		env = append(env, guestMountsControlVar+"="+string(payload))
	}
	return env, nil
}

func hostRuntimeEnv(guestEnv map[string]string, mounts []Mount) (map[string]string, error) {
	hostEnv := make(map[string]string, len(guestEnv))
	for key, value := range guestEnv {
		hostValue, err := translateGuestEnvValueForHost(key, value, mounts)
		if err != nil {
			return nil, err
		}
		hostEnv[key] = hostValue
	}
	return hostEnv, nil
}

func translateGuestEnvValueForHost(key, value string, mounts []Mount) (string, error) {
	switch key {
	case "HTTPS_PROXY", "HTTP_PROXY":
		parsed, err := url.Parse(value)
		if err != nil {
			return "", fmt.Errorf("translate %s for host runtime: %w", key, err)
		}
		if strings.EqualFold(parsed.Hostname(), proxyVirtualHostname) {
			parsed.Host = net.JoinHostPort("127.0.0.1", parsed.Port())
			return parsed.String(), nil
		}
		return value, nil
	case "SSL_CERT_FILE":
		hostPath, ok := translateGuestPathToHost(value, mounts)
		if !ok {
			return "", fmt.Errorf("translate SSL_CERT_FILE for host runtime: no host mount for %s", value)
		}
		return hostPath, nil
	default:
		return value, nil
	}
}

func translateGuestPathToHost(guestPath string, mounts []Mount) (string, bool) {
	cleanGuestPath := pathCleanForGuest(guestPath)
	for _, mount := range mounts {
		mountRoot := pathCleanForGuest(mount.GuestPath)
		if cleanGuestPath == mountRoot {
			return mount.HostPath, true
		}
		if strings.HasPrefix(cleanGuestPath, mountRoot+"/") {
			rel := strings.TrimPrefix(cleanGuestPath, mountRoot+"/")
			return filepath.Join(mount.HostPath, filepath.FromSlash(rel)), true
		}
	}
	return "", false
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

// SortedEnvKeysForTest returns stable env key ordering for tests.
func SortedEnvKeysForTest(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
