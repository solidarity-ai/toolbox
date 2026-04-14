//go:build windows

package platform

func StopAllRunningServers(func(string, ...any)) ([]int, error) {
	return nil, ErrUnsupportedPlatform
}
