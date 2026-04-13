//go:build windows

package platform

func LaunchServer() error {
	return ErrUnsupportedPlatform
}

func IsProcessAlive(_ int) bool {
	return false
}

func CleanStale(_, _ string) error {
	return ErrUnsupportedPlatform
}
