package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func sameExecutable(peerPath string) error {
	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve self executable: %w", err)
	}
	selfPath, err = resolveExecutablePath(selfPath)
	if err != nil {
		return fmt.Errorf("resolve self executable: %w", err)
	}

	peerPath, err = resolveExecutablePath(peerPath)
	if err != nil {
		return fmt.Errorf("resolve peer executable: %w", err)
	}

	selfInfo, err := os.Stat(selfPath)
	if err != nil {
		return fmt.Errorf("stat self executable: %w", err)
	}
	peerInfo, err := os.Stat(peerPath)
	if err != nil {
		return fmt.Errorf("stat peer executable: %w", err)
	}
	if !os.SameFile(selfInfo, peerInfo) {
		return fmt.Errorf("peer executable mismatch: %s", peerPath)
	}
	return nil
}

func resolveExecutablePath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return path, nil
	}
	return "", err
}
