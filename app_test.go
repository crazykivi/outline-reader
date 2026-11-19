package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"outline-reader/storage"
)

type fakeOutline struct {
	mu         sync.Mutex
	docs       []map[string]any
	unexpected []string
}

func newFakeOutline(t *testing.T) (*fakeOutline, *httptest.Server) {
	t.Helper()
	ts := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	f := &fakeOutline{docs: []map[string]any{
		{"id": "d1", "title": "Root", "text": "# Hello", "url": "/doc/root-abc123", "urlId": "abc123", "parentDocumentId": nil, "collectionId": "c1", "updatedAt": ts},
		{"id": "d2", "title": "Child", "text": "child text", "url": "/doc/child-def456", "urlId": "def456", "parentDocumentId": "d1", "collectionId": "c1", "updatedAt": ts},
	}}
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": v})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth.info", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"ok":false,"error":"authentication_required","message":"Invalid token"}`))
			return
		}
		write(w, map[string]any{})
	})
	mux.HandleFunc("/api/collections.list", func(w http.ResponseWriter, r *http.Request) {
		write(w, []map[string]any{{"id": "c1", "name": "Wiki"}})
	})
	mux.HandleFunc("/api/collections.documents", func(w http.ResponseWriter, r *http.Request) {
		write(w, []map[string]any{{"id": "d1", "title": "Root", "url": "/doc/root-abc123",
			"children": []map[string]any{{"id": "d2", "title": "Child", "url": "/doc/child-def456", "children": []any{}}}}})
	})
	mux.HandleFunc("/api/documents.list", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		write(w, f.docs)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.unexpected = append(f.unexpected, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	a := NewApp()
	a.dbPath = filepath.Join(t.TempDir(), "test.db")
	ctx, cancel := context.WithCancel(context.Background())
	a.startup(ctx)
	t.Cleanup(func() {
		if db := a.getDB(); db != nil {
			_ = db.Close()
		}
	})
	t.Cleanup(cancel)
	return a
}

func TestSetConnectionValidationAndPersistence(t *testing.T) {
	_, srv1 := newFakeOutline(t)
	_, srv2 := newFakeOutline(t)
	a := newTestApp(t)

	err := a.SetConnection(srv1.URL, "bad")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid token")

	require.NoError(t, a.SetConnection(srv1.URL+"/", "good"))
	s, err := a.GetSettings()
	require.NoError(t, err)
	assert.Equal(t, srv1.URL, s.APIURL)
	assert.Equal(t, "good", s.APIKey)

	require.NoError(t, a.SetConnection(srv2.URL, "good"))
	s, err = a.GetSettings()
	require.NoError(t, err)
	assert.Equal(t, srv2.URL, s.APIURL)
}

func TestSyncTreeSearchAndURLID(t *testing.T) {
	_, srv := newFakeOutline(t)
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))

	res, err := a.SyncDocuments()
	require.NoError(t, err)
	assert.Equal(t, 2, res.Total)

	tree, err := a.GetTree()
	require.NoError(t, err)
	require.Len(t, tree, 1)
	assert.Equal(t, "Wiki", tree[0].Name)
	require.Len(t, tree[0].Nodes, 1)
	assert.Equal(t, "d1", tree[0].Nodes[0].OID)
	require.Len(t, tree[0].Nodes[0].Children, 1)
	assert.Equal(t, "d2", tree[0].Nodes[0].Children[0].OID)

	doc, err := a.GetDocument("d1")
	require.NoError(t, err)
	assert.Equal(t, "# Hello", doc.Text)
	assert.Equal(t, "/doc/root-abc123", doc.Url)

	byURL, err := a.GetDocumentByURLID("def456")
	require.NoError(t, err)
	assert.Equal(t, "d2", byURL.OutlineID)
	assert.Equal(t, "/doc/child-def456", byURL.Url)

	hits, err := a.SearchDocuments("child")
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "d2", hits[0].OutlineID)
}

func TestCheckForUpdatesAppliesChanges(t *testing.T) {
	f, srv := newFakeOutline(t)
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))
	_, err := a.SyncDocuments()
	require.NoError(t, err)

	titles, err := a.CheckForUpdates()
	require.NoError(t, err)
	assert.Empty(t, titles)

	f.mu.Lock()
	f.docs[0]["updatedAt"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	f.docs[0]["text"] = "# Updated"
	f.mu.Unlock()

	titles, err = a.CheckForUpdates()
	require.NoError(t, err)
	assert.Equal(t, []string{"Root"}, titles)

	doc, err := a.GetDocument("d1")
	require.NoError(t, err)
	assert.Equal(t, "# Updated", doc.Text)
	assert.Equal(t, "/doc/root-abc123", doc.Url)
}

func TestSyncWithoutURLField(t *testing.T) {
	f, srv := newFakeOutline(t)

	f.mu.Lock()
	for _, d := range f.docs {
		delete(d, "url")
	}
	f.mu.Unlock()

	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))
	res, err := a.SyncDocuments()
	require.NoError(t, err)
	assert.Equal(t, 2, res.Total)

	doc, err := a.GetDocument("d1")
	require.NoError(t, err)
	assert.Empty(t, doc.Url)
	assert.Equal(t, "abc123", doc.UrlID)
}

func TestResetAppWipesLocalOnly(t *testing.T) {
	f, srv := newFakeOutline(t)
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))
	_, err := a.SyncDocuments()
	require.NoError(t, err)

	require.NoError(t, a.ResetApp())

	s, err := a.GetSettings()
	require.NoError(t, err)
	assert.Empty(t, s.APIURL)
	assert.Empty(t, s.APIKey)

	tree, err := a.GetTree()
	require.NoError(t, err)
	assert.Empty(t, tree)

	_, err = a.GetDocument("d1")
	assert.Error(t, err)

	f.mu.Lock()
	assert.Empty(t, f.unexpected)
	f.mu.Unlock()

	require.NoError(t, a.SetConnection(srv.URL, "good"))
	res, err := a.SyncDocuments()
	require.NoError(t, err)
	assert.Equal(t, 2, res.Total)
}

func TestSettingsSurviveRestart(t *testing.T) {
	_, srv := newFakeOutline(t)
	path := filepath.Join(t.TempDir(), "cache.db")

	a := NewApp()
	a.dbPath = path
	require.NoError(t, a.SetConnection(srv.URL, "good"))
	_, err := a.SyncDocuments()
	require.NoError(t, err)
	require.NoError(t, a.getDB().Close())

	b := NewApp()
	b.dbPath = path
	t.Cleanup(func() {
		if db := b.getDB(); db != nil {
			_ = db.Close()
		}
	})

	s, err := b.GetSettings()
	require.NoError(t, err)
	assert.Equal(t, srv.URL, s.APIURL)
	assert.Equal(t, "good", s.APIKey)

	tree, err := b.GetTree()
	require.NoError(t, err)
	assert.Len(t, tree, 1)
}

func TestSyncRemovesDeletedDocuments(t *testing.T) {
	f, srv := newFakeOutline(t)
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))
	_, err := a.SyncDocuments()
	require.NoError(t, err)

	f.mu.Lock()
	f.docs = f.docs[:1]
	f.mu.Unlock()

	titles, err := a.CheckForUpdates()
	require.NoError(t, err)
	assert.Empty(t, titles)

	res, err := a.SyncDocuments()
	require.NoError(t, err)
	assert.Equal(t, 1, res.Total)
	assert.Equal(t, 1, res.Deleted)

	_, err = a.GetDocument("d2")
	assert.Error(t, err)

	hits, err := a.SearchDocuments("child")
	require.NoError(t, err)
	assert.Empty(t, hits)
}

func TestPortableDataDirDetection(t *testing.T) {
	base := t.TempDir()
	assert.Empty(t, portableDataDir(base))

	data := filepath.Join(base, "data")
	require.NoError(t, os.Mkdir(data, 0o755))
	assert.Equal(t, data, portableDataDir(base))

	entries, err := os.ReadDir(data)
	require.NoError(t, err)
	assert.Empty(t, entries)

	other := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(other, "data"), []byte("x"), 0o600))
	assert.Empty(t, portableDataDir(other))
}

func TestStorageInfoReportsPaths(t *testing.T) {
	a := newTestApp(t)
	info, err := a.StorageInfo()
	require.NoError(t, err)
	assert.False(t, info.Portable)
	assert.Equal(t, a.dbPath, info.DBPath)
	assert.Equal(t, filepath.Dir(a.dbPath), info.DataDir)
}

func TestEncryptedDBWrongKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	keyA := strings.Repeat("ab", 32)
	keyB := strings.Repeat("cd", 32)

	db, err := storage.NewDB(path, keyA)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	assert.False(t, storage.IsPlaintextDB(path))

	_, err = storage.NewDB(path, keyB)
	require.Error(t, err)

	db2, err := storage.NewDB(path, keyA)
	require.NoError(t, err)
	require.NoError(t, db2.Close())
}

func TestMigratePlaintext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	db, err := storage.NewDB(path, "")
	require.NoError(t, err)
	require.NoError(t, db.SaveSettings(&storage.Settings{APIURL: "https://x", APIKey: "k"}))
	require.NoError(t, db.Close())
	require.True(t, storage.IsPlaintextDB(path))

	key := strings.Repeat("ab", 32)
	require.NoError(t, storage.MigratePlaintext(path, key))
	assert.False(t, storage.IsPlaintextDB(path))

	db2, err := storage.NewDB(path, key)
	require.NoError(t, err)
	defer db2.Close()
	s, err := db2.GetSettings()
	require.NoError(t, err)
	assert.Equal(t, "https://x", s.APIURL)
	assert.Equal(t, "k", s.APIKey)
}

func TestSyncDiffCounters(t *testing.T) {
	f, srv := newFakeOutline(t)
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))

	res, err := a.SyncDocuments()
	require.NoError(t, err)
	assert.Equal(t, SyncResult{Total: 2, Added: 2}, res)

	res, err = a.SyncDocuments()
	require.NoError(t, err)
	assert.Equal(t, SyncResult{Total: 2}, res)

	f.mu.Lock()
	f.docs[0]["updatedAt"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	f.docs = f.docs[:1]
	f.mu.Unlock()

	res, err = a.SyncDocuments()
	require.NoError(t, err)
	assert.Equal(t, 1, res.Total)
	assert.Equal(t, 1, res.Updated)
	assert.Equal(t, 1, res.Deleted)

	st, err := a.GetSyncStats()
	require.NoError(t, err)
	assert.Equal(t, 1, st.Total)
	assert.Equal(t, 1, st.Deleted)
	assert.NotEmpty(t, st.LastSync)
}

func TestLockUnlockKeepsData(t *testing.T) {
	_, srv := newFakeOutline(t)
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))
	_, err := a.SyncDocuments()
	require.NoError(t, err)

	require.NoError(t, a.LockApp())
	st, err := a.StorageStatus()
	require.NoError(t, err)
	assert.True(t, st.Locked)
	_, err = a.GetDocument("d1")
	assert.Error(t, err)

	require.NoError(t, a.UnlockDB(""))
	doc, err := a.GetDocument("d1")
	require.NoError(t, err)
	assert.Equal(t, "Root", doc.Title)
}

func TestFavoritesAndRecents(t *testing.T) {
	_, srv := newFakeOutline(t)
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))
	_, err := a.SyncDocuments()
	require.NoError(t, err)

	_, err = a.GetDocument("d2")
	require.NoError(t, err)
	_, err = a.GetDocument("d1")
	require.NoError(t, err)

	rec, err := a.GetRecent()
	require.NoError(t, err)
	require.Len(t, rec, 2)
	assert.Equal(t, "d1", rec[0].OutlineID)

	on, err := a.ToggleFavorite("d2")
	require.NoError(t, err)
	assert.True(t, on)
	favs, err := a.GetFavorites()
	require.NoError(t, err)
	require.Len(t, favs, 1)
	assert.Equal(t, "d2", favs[0].OutlineID)
	is, err := a.IsFavorite("d2")
	require.NoError(t, err)
	assert.True(t, is)

	on, err = a.ToggleFavorite("d2")
	require.NoError(t, err)
	assert.False(t, on)
}

func TestServeImageFromCacheOffline(t *testing.T) {
	a := newTestApp(t)
	require.NoError(t, a.getDB().SaveBlob(&storage.Blob{ID: "att-1", Mime: "image/png", Data: []byte{1, 2, 3}, Size: 3}))

	req := httptest.NewRequest(http.MethodGet, "/img?id=att-1", nil)
	rec := httptest.NewRecorder()
	a.imageMiddleware(http.NotFoundHandler()).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Equal(t, []byte{1, 2, 3}, rec.Body.Bytes())

	req2 := httptest.NewRequest(http.MethodGet, "/img?id=missing", nil)
	rec2 := httptest.NewRecorder()
	a.imageMiddleware(http.NotFoundHandler()).ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusNotFound, rec2.Code)
}
