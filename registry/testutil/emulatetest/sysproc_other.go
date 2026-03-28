//go:build !linux

package emulatetest

import "os/exec"

func applyPlatformSysProcAttr(cmd *exec.Cmd) {}
