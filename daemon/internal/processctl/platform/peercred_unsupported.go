//go:build !linux && !darwin

package platform

import "net"

func VerifyPeerBinary(_ *net.UnixConn) (int, error) {
	return 0, ErrUnsupportedPlatform
}
