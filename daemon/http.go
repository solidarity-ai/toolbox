package daemon

import (
	"strings"
)

const (
	DefaultBindAddress = "127.0.0.1:7331"
	BindAddressEnv     = "TOOLBOX_BIND_ADDRESS"
)

func BindAddress() (string, bool) {
	if bindAddress := strings.TrimSpace(getenv(BindAddressEnv)); bindAddress != "" {
		return bindAddress, true
	}
	return DefaultBindAddress, false
}

func BaseURL() string {
	bindAddress, _ := BindAddress()
	return "http://" + bindAddress
}
