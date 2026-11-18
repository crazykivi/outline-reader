package crypto

import (
	"encoding/base64"
	"errors"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const encryptedPrefix = "enc:"

func EncryptWithDPAPI(plaintext string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("DPAPI доступен только на Windows")
	}

	data := []byte(plaintext)
	in := windows.DataBlob{
		Size: uint32(len(data)),
		Data: &data[0],
	}
	var out windows.DataBlob

	err := windows.CryptProtectData(&in, nil, nil, 0, nil, 0, &out)
	if err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))

	result := make([]byte, out.Size)
	copy(result, unsafe.Slice(out.Data, out.Size))
	return encryptedPrefix + base64.StdEncoding.EncodeToString(result), nil
}

func DecryptWithDPAPI(encoded string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("DPAPI доступен только на Windows")
	}

	if !strings.HasPrefix(encoded, encryptedPrefix) {
		return "", errors.New("данные не зашифрованы")
	}

	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, encryptedPrefix))
	if err != nil {
		return "", err
	}

	in := windows.DataBlob{
		Size: uint32(len(data)),
		Data: &data[0],
	}
	var out windows.DataBlob

	err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, 0, &out)
	if err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))

	result := make([]byte, out.Size)
	copy(result, unsafe.Slice(out.Data, out.Size))
	return string(result), nil
}

func EncryptIfPossible(plaintext string) string {
	encrypted, err := EncryptWithDPAPI(plaintext)
	if err != nil {
		return plaintext
	}
	return encrypted
}

func DecryptIfPossible(data string) string {
	if !strings.HasPrefix(data, encryptedPrefix) {
		return data
	}
	decrypted, err := DecryptWithDPAPI(data)
	if err != nil {
		return data
	}
	return decrypted
}
