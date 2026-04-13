package processctl

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	daemonpaths "github.com/solidarity-ai/toolbox/daemon/internal/processctl/paths"
	daemonplatform "github.com/solidarity-ai/toolbox/daemon/internal/processctl/platform"
)

func EnsureRuntimeDir() error {
	dir, err := daemonpaths.Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create daemon dir: %w", err)
	}
	return nil
}

func ReadPID() (int, error) {
	pidPath, err := daemonpaths.PIDPath()
	if err != nil {
		return 0, err
	}
	return ReadPIDFile(pidPath)
}

func ReadPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse daemon pid: %w", err)
	}
	return pid, nil
}

func CleanStale() error {
	socketPath, err := daemonpaths.SocketPath()
	if err != nil {
		return err
	}
	pidPath, err := daemonpaths.PIDPath()
	if err != nil {
		return err
	}
	return daemonplatform.CleanStale(socketPath, pidPath)
}

func Launch() error {
	return daemonplatform.LaunchServer()
}

func IsProcessAlive(pid int) bool {
	return daemonplatform.IsProcessAlive(pid)
}
