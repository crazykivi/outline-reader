//go:build windows

package crypto

import (
	"encoding/base64"
	"errors"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func EncryptWithDPAPI(plaintext string) (string, error) {
	data := []byte(plaintext)
	if len(data) == 0 {
		return "", errors.New("пустые данные")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return "", err
	}
	defer func() { _, _ = windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data)))) }()
	result := make([]byte, out.Size)
	copy(result, unsafe.Slice(out.Data, out.Size))
	return encryptedPrefix + base64.StdEncoding.EncodeToString(result), nil
}

func DecryptWithDPAPI(encoded string) (string, error) {
	if !strings.HasPrefix(encoded, encryptedPrefix) {
		return "", errors.New("данные не зашифрованы")
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, encryptedPrefix))
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", errors.New("пустые данные")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return "", err
	}
	defer func() { _, _ = windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data)))) }()
	result := make([]byte, out.Size)
	copy(result, unsafe.Slice(out.Data, out.Size))
	return string(result), nil
}
