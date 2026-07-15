//go:build windows

package devtls

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var dpapiIdentityHeader = []byte("TOOLBOX-DEVTLS-DPAPI\x00")

func protectIdentity(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("identity is empty")
	}
	in := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	ciphertext := unsafe.Slice(out.Data, out.Size)
	data := make([]byte, 0, len(dpapiIdentityHeader)+len(ciphertext))
	data = append(data, dpapiIdentityHeader...)
	data = append(data, ciphertext...)
	return data, nil
}

func unprotectIdentity(data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, dpapiIdentityHeader) {
		return nil, errors.New("identity is not DPAPI protected")
	}
	ciphertext := data[len(dpapiIdentityHeader):]
	if len(ciphertext) == 0 {
		return nil, errors.New("protected identity is empty")
	}
	in := windows.DataBlob{Size: uint32(len(ciphertext)), Data: &ciphertext[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func secureIdentityDirectory(path string) error {
	return os.MkdirAll(path, 0o700)
}

func readIdentityFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("identity is not a regular file")
	}
	return os.ReadFile(path)
}

func replaceIdentityFile(from, to string) error {
	fromPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return fmt.Errorf("encode temporary identity path: %w", err)
	}
	toPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return fmt.Errorf("encode identity path: %w", err)
	}
	return windows.MoveFileEx(fromPtr, toPtr, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
