package paths

import (
	"os"
	"path/filepath"
	"strings"
)

const DaemonDirEnv = "TOOLBOX_DAEMON_DIR"

// Dir returns the daemon runtime directory.
func Dir() (string, error) {
	if override := strings.TrimSpace(os.Getenv(DaemonDirEnv)); override != "" {
		return filepath.Clean(override), nil
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(configDir, "toolbox"), nil
}

// SocketPath returns the daemon Unix socket path.
func SocketPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.sock"), nil
}

// PIDPath returns the daemon pid file path.
func PIDPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.pid"), nil
}

// LockPath returns the daemon launch lock path.
func LockPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.lock"), nil
}

// LogPath returns the daemon log file path.
func LogPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.log"), nil
}
