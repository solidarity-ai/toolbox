//go:build linux

package platform

import (
	"fmt"
	"net"
	"os"

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
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err != nil {
			controlErr = err
			return
		}
		if int(cred.Uid) != os.Getuid() {
			controlErr = fmt.Errorf("peer UID %d does not match current user UID %d", cred.Uid, os.Getuid())
			return
		}
		pid = int(cred.Pid)
	}); err != nil {
		return 0, fmt.Errorf("inspect peer credentials: %w", err)
	}
	if controlErr != nil {
		return 0, fmt.Errorf("lookup peer credentials: %w", controlErr)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("peer pid unavailable")
	}

	peerPath, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return 0, fmt.Errorf("read peer executable: %w", err)
	}
	if err := sameExecutable(peerPath); err != nil {
		return 0, err
	}
	return pid, nil
}
