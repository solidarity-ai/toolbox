package transport

import (
	"net"

	daemonplatform "github.com/solidarity-ai/toolbox/daemon/internal/processctl/platform"
)

type verifiedListener struct {
	net.Listener
}

func NewVerifiedListener(listener net.Listener) net.Listener {
	if listener == nil {
		return nil
	}
	return &verifiedListener{Listener: listener}
}

func (l *verifiedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		unixConn, ok := conn.(*net.UnixConn)
		if !ok {
			_ = conn.Close()
			continue
		}
		if _, err := daemonplatform.VerifyPeerBinary(unixConn); err != nil {
			_ = conn.Close()
			continue
		}
		return conn, nil
	}
}
