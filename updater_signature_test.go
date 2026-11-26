package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifySignatureRejectsTampering(t *testing.T) {
	pub, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("test executable")
	digest := sha256.Sum256(data)
	sig := ed25519.Sign(privateKey, digest[:])
	path := filepath.Join(t.TempDir(), "test.exe")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(sig)
	if err := verifySignatureWithKey(path, pub, encoded); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered executable"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifySignatureWithKey(path, pub, encoded); err == nil {
		t.Fatal("tampered executable must fail signature verification")
	}
	if err := verifySignatureWithKey(path, pub, "invalid"); err == nil {
		t.Fatal("invalid signature encoding must fail")
	}
}

