//go:build !toolbox_daemon_test

package platform

import "os"

func DaemonExecutablePath() (string, error) {
	return os.Executable()
}
