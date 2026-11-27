package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
)

const masterSaltFile = "db.salt"

func masterKeyParams(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, 4, 128*1024, 4, 32)
}

func CreateMasterKey(dataDir, passphrase string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dataDir, masterSaltFile), salt, 0o600); err != nil {
		return "", err
	}
	key := masterKeyParams(passphrase, salt)
	log.Printf("[CRYPTO] мастер-пароль установлен, база создана")
	return hex.EncodeToString(key), nil
}

func DeriveMasterKey(dataDir, passphrase string) (string, error) {
	salt, err := os.ReadFile(filepath.Join(dataDir, masterSaltFile))
	if err != nil {
		return "", errors.New("соль мастер-пароля не найдена: база не инициализирована")
	}
	if len(salt) != 16 {
		return "", errors.New("db.salt повреждён")
	}
	return hex.EncodeToString(masterKeyParams(passphrase, salt)), nil
}

func HasMasterSalt(dataDir string) bool {
	st, err := os.Stat(filepath.Join(dataDir, masterSaltFile))
	return err == nil && st.Size() == 16
}
