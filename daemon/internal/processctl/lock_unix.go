//go:build !windows

package processctl

import (
	"fmt"
	"os"

	daemonpaths "github.com/solidarity-ai/toolbox/daemon/internal/processctl/paths"
	"golang.org/x/sys/unix"
)

func AcquireLaunchLock() (*os.File, error) {
	lockPath, err := daemonpaths.LockPath()
	if err != nil {
		return nil, err
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open daemon lock: %w", err)
	}
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("lock daemon launch: %w", err)
	}
	return lockFile, nil
}

func ReleaseLaunchLock(lockFile *os.File) error {
	if lockFile == nil {
		return nil
	}
	var unlockErr error
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_UN); err != nil {
		unlockErr = fmt.Errorf("unlock daemon launch: %w", err)
	}
	if err := lockFile.Close(); err != nil && unlockErr == nil {
		unlockErr = err
	}
	return unlockErr
}
