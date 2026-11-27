//go:build !windows

package crypto

import "errors"

func EncryptWithDPAPI(plaintext string) (string, error) {
	return "", errors.New("DPAPI доступен только на Windows")
}

func DecryptWithDPAPI(encoded string) (string, error) {
	return "", errors.New("DPAPI доступен только на Windows")
}
