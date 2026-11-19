package crypto

import "strings"

const encryptedPrefix = "enc:"

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
