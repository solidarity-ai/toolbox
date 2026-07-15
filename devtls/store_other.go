//go:build !darwin && !windows && !linux

package devtls

func newNativeStore(nativeStoreConfig) (TrustStore, error) {
	return nil, ErrUnsupportedOS
}
