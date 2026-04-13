//go:build toolbox_daemon_test

package platform

import (
	"os"
	"path/filepath"
	"strings"
)

func DaemonExecutablePath() (string, error) {
	if override := strings.TrimSpace(os.Getenv(DaemonExecutableEnv)); override != "" {
		return filepath.Abs(override)
	}
	return os.Executable()
}
