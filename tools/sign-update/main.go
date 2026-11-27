package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	file := flag.String("file", "", "path to release executable")
	output := flag.String("output", "", "path to signature output")
	flag.Parse()
	if *file == "" || *output == "" {
		fatal("both -file and -output are required")
	}
	seed, err := base64.StdEncoding.DecodeString(os.Getenv("UPDATE_SIGNING_KEY"))
	if err != nil || len(seed) != ed25519.SeedSize {
		fatal("UPDATE_SIGNING_KEY must be base64-encoded 32-byte Ed25519 seed")
	}
	f, err := os.Open(*file)
	if err != nil {
		fatal(err.Error())
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, f)
	closeErr := f.Close()
	if copyErr != nil {
		fatal(copyErr.Error())
	}
	if closeErr != nil {
		fatal(closeErr.Error())
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	signature := ed25519.Sign(privateKey, h.Sum(nil))
	if err := os.WriteFile(*output, []byte(base64.StdEncoding.EncodeToString(signature)+"\n"), 0644); err != nil {
		fatal(err.Error())
	}
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
