//go:build !linux && !windows

package platform

import (
	"os/exec"
	"syscall"
)

func ApplySysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
