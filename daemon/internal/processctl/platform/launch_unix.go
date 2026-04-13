//go:build !windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	daemonpaths "github.com/solidarity-ai/toolbox/daemon/internal/processctl/paths"
)

func LaunchServer() error {
	exePath, err := DaemonExecutablePath()
	if err != nil {
		return fmt.Errorf("resolve daemon executable: %w", err)
	}

	dir, err := daemonpaths.Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create daemon dir: %w", err)
	}

	logPath, err := daemonpaths.LogPath()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer logFile.Close()

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer devNull.Close()

	cmd := exec.Command(exePath, "_daemon", "serve")
	cmd.Stdin = devNull
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	ApplySysProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return nil
}

func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func CleanStale(sockPath, pidPath string) error {
	for _, path := range []string{sockPath, pidPath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	return nil
}
