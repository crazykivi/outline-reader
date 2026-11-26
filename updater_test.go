package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAssetTransport struct {
	serverURL string
}

func (t fakeAssetTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	local, err := url.Parse(t.serverURL)
	if err != nil {
		return nil, err
	}
	clone := req.Clone(req.Context())
	u := *req.URL
	u.Scheme, u.Host = local.Scheme, local.Host
	clone.URL = &u
	clone.Host = local.Host
	return http.DefaultTransport.RoundTrip(clone)
}

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
				{"name": name, "browser_download_url": "https://github.com/crazykivi/outline-reader/releases/download/test/exe"},
				{"name": name + ".sig", "browser_download_url": "https://github.com/crazykivi/outline-reader/releases/download/test/sig"},
				{"name": "sha256sums.txt", "browser_download_url": "https://github.com/crazykivi/outline-reader/releases/download/test/sums"},
			},
		})
	})
	mux.HandleFunc("/exe", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	})
	mux.HandleFunc("/sig", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, base64.StdEncoding.EncodeToString(make([]byte, 64)))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckForUpdateOffersNewVersion(t *testing.T) {
	srv := newFakeGitHub(t, "v9.9.9", []byte("MZfake"))
	a := NewApp()
	a.version, a.updateAPI = "1.0.0", srv.URL+"/latest"
	a.updateAssetTransport = fakeAssetTransport{serverURL: srv.URL}
	a.updateAssetTransport = fakeAssetTransport{serverURL: srv.URL}
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

func TestMarkUpdateReadyWithoutUpdateIsNoop(t *testing.T) {
	t.Setenv(updateReadyEnv, "")
	a := NewApp()
	require.NoError(t, a.markUpdateReady())
}

func TestMarkUpdateReadyCreatesMarkerOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready.flag")
	t.Setenv(updateReadyEnv, path)
	a := NewApp()

	require.NoError(t, a.markUpdateReady())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprint(os.Getpid()), string(data))
	assert.Empty(t, os.Getenv(updateReadyEnv))

	require.NoError(t, a.markUpdateReady())
}
