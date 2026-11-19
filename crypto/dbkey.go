package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
)

func LoadOrCreateDPAPIKey(dataDir string) (string, error) {
	keyPath := filepath.Join(dataDir, "db.key")
	if raw, err := os.ReadFile(keyPath); err == nil {
		key, err := UnprotectBytes(raw)
		if err != nil {
			return "", fmt.Errorf("db.key не расшифровывается (другой пользователь/машина?): %w", err)
		}
		if len(key) != 32 {
			return "", errors.New("db.key: неверная длина ключа")
		}
		return hex.EncodeToString(key), nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	enc, err := ProtectBytes(key)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(keyPath, enc, 0o600); err != nil {
		return "", err
	}
	log.Printf("[CRYPTO] создан новый ключ базы (DPAPI)")
	return hex.EncodeToString(key), nil
}

func DerivePortableKey(dataDir, passphrase string) (string, error) {
	saltPath := filepath.Join(dataDir, "db.salt")
	salt, err := os.ReadFile(saltPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		salt = make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return "", err
		}
		if err := os.WriteFile(saltPath, salt, 0o600); err != nil {
			return "", err
		}
	}
	if len(salt) != 16 {
		return "", errors.New("db.salt повреждён")
	}
	key := argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 4, 32)
	return hex.EncodeToString(key), nil
}
