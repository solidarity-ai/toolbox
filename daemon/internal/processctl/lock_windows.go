//go:build windows

package processctl

import (
	"os"

	daemonplatform "github.com/solidarity-ai/toolbox/daemon/internal/processctl/platform"
)

func AcquireLaunchLock() (*os.File, error) {
	return nil, daemonplatform.ErrUnsupportedPlatform
}

func ReleaseLaunchLock(*os.File) error {
	return daemonplatform.ErrUnsupportedPlatform
}
