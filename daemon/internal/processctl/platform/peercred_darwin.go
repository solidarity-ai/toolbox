//go:build darwin

package platform

import (
	"bytes"
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

func VerifyPeerBinary(conn *net.UnixConn) (int, error) {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("get raw unix conn: %w", err)
	}

	var (
		pid        int
		controlErr error
	)
	if err := rawConn.Control(func(fd uintptr) {
		pid, controlErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	}); err != nil {
		return 0, fmt.Errorf("inspect peer credentials: %w", err)
	}
	if controlErr != nil {
		return 0, fmt.Errorf("lookup peer pid: %w", controlErr)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("peer pid unavailable")
	}

	peerPath, err := peerExecutablePath(pid)
	if err != nil {
		return 0, err
	}
	if err := sameExecutable(peerPath); err != nil {
		return 0, err
	}
	return pid, nil
}

func peerExecutablePath(pid int) (string, error) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", fmt.Errorf("kern.procargs2 pid %d: %w", pid, err)
	}
	if len(buf) <= 4 {
		return "", fmt.Errorf("kern.procargs2 pid %d returned %d bytes", pid, len(buf))
	}

	buf = buf[4:]
	if idx := bytes.IndexByte(buf, 0); idx >= 0 {
		buf = buf[:idx]
	}
	if len(buf) == 0 {
		return "", fmt.Errorf("kern.procargs2 pid %d returned empty path", pid)
	}
	return string(buf), nil
}
