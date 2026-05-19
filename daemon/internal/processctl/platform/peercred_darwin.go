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
		xucred     *unix.Xucred
		controlErr error
	)
	if err := rawConn.Control(func(fd uintptr) {
		pid, controlErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		if controlErr != nil {
			return
		}
		xucred, controlErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return 0, fmt.Errorf("inspect peer credentials: %w", err)
	}
	if controlErr != nil {
		return 0, fmt.Errorf("lookup peer credentials: %w", controlErr)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("peer pid unavailable")
	}
	if xucred == nil {
		return 0, fmt.Errorf("peer credentials unavailable")
	}
	if int(xucred.Uid) != unix.Getuid() {
		return 0, fmt.Errorf("peer UID %d does not match current user UID %d", xucred.Uid, unix.Getuid())
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
