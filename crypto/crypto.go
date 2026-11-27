package crypto

import "strings"

const encryptedPrefix = "enc:"

func IsWrapped(data string) bool { return strings.HasPrefix(data, encryptedPrefix) }

func UnwrapLegacy(data string) (string, error) {
	if !IsWrapped(data) {
		return data, nil
	}
	return DecryptWithDPAPI(data)
}
