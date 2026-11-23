package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newFakeGitHub(t *testing.T, tag string, payload []byte) *httptest.Server {
	t.Helper()
	name := "outline-reader-" + tag + "-windows-amd64.exe"
	sum := sha256.Sum256(payload)
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": tag,
			"assets": []map[string]any{
				{"name": name, "browser_download_url": srv.URL + "/exe"},
				{"name": "sha256sums.txt", "browser_download_url": srv.URL + "/sums"},
			},
		})
	})
	mux.HandleFunc("/exe", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckForUpdateOffersNewVersion(t *testing.T) {
	srv := newFakeGitHub(t, "v9.9.9", []byte("MZfake"))
	a := NewApp()
	a.version, a.updateAPI = "1.0.0", srv.URL+"/latest"
	info, err := a.CheckForUpdate()
	require.NoError(t, err)
	assert.True(t, info.Available)
	assert.Equal(t, "9.9.9", info.Version)
	assert.Len(t, info.SHA256, 64)
}

func TestCheckForUpdateSameVersionAndDev(t *testing.T) {
	srv := newFakeGitHub(t, "v1.0.0", []byte("MZfake"))
	a := NewApp()
	a.version, a.updateAPI = "1.0.0", srv.URL+"/latest"
	info, err := a.CheckForUpdate()
	require.NoError(t, err)
	assert.False(t, info.Available)

	a.version = ""
	info, err = a.CheckForUpdate()
	require.NoError(t, err)
	assert.False(t, info.Available)
	assert.NotEmpty(t, info.Note)
}

func TestInstallRejectsUnverifiedURL(t *testing.T) {
	a := NewApp()
	require.Error(t, a.DownloadAndInstallUpdate("https://example.com/evil.exe"))
}

func TestDownloadUpdateRejectsBadChecksum(t *testing.T) {
	srv := newFakeGitHub(t, "v9.9.9", []byte("MZfake"))
	a := NewApp()
	a.version, a.updateAPI = "1.0.0", srv.URL+"/latest"
	info, err := a.CheckForUpdate()
	require.NoError(t, err)
	info.SHA256 = strings.Repeat("0", 64)
	_, err = a.downloadUpdate(&info)
	require.Error(t, err)
}

func TestNewerVersion(t *testing.T) {
	assert.True(t, newerVersion("v1.2.0", "1.1.9"))
	assert.False(t, newerVersion("v1.2.0", "1.2.0"))
	assert.True(t, newerVersion("v1.2.0", "1.2.0-beta.1"))
	assert.False(t, newerVersion("v1.2.0", "dev"))
}
