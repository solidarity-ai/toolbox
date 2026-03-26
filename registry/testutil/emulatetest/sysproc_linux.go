//go:build linux

package emulatetest

import (
	"os/exec"
	"syscall"
)

func applyPlatformSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
