package main

import "testing"

func TestValidateReleaseAssetURL(t *testing.T) {
	valid := []string{
		"https://github.com/crazykivi/outline-reader/releases/download/v1.0.0/app.exe",
		"https://release-assets.githubusercontent.com/file",
		"https://objects.githubusercontent.com/file",
	}
	for _, raw := range valid {
		if err := validateReleaseAssetURL(raw); err != nil {
			t.Errorf("%q should be accepted: %v", raw, err)
		}
	}
	invalid := []string{
		"http://github.com/release.exe",
		"https://example.com/release.exe",
		"https://github.com.evil.example/release.exe",
		"https://user:pass@github.com/release.exe",
		"https://github.com:444/release.exe",
	}
	for _, raw := range invalid {
		if err := validateReleaseAssetURL(raw); err == nil {
			t.Errorf("%q should be rejected", raw)
		}
	}
}
