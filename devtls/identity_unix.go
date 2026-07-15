//go:build !windows

package devtls

import (
	"errors"
	"fmt"
	"os"
)

func protectIdentity(plain []byte) ([]byte, error)  { return cloneBytes(plain), nil }
func unprotectIdentity(data []byte) ([]byte, error) { return cloneBytes(data), nil }

func secureIdentityDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("identity parent is not a real directory")
	}
	return os.Chmod(path, 0o700)
}

func readIdentityFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("identity is not a regular file")
	}
	if permissions := info.Mode().Perm(); permissions&0o077 != 0 {
		return nil, fmt.Errorf("identity permissions are %#o, want no group or other access", permissions)
	}
	return os.ReadFile(path)
}

func replaceIdentityFile(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	return os.Chmod(to, 0o600)
}
