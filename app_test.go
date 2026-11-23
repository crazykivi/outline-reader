package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"outline-reader/storage"
)

type fakeOutline struct {
	mu          sync.Mutex
	docs        []map[string]any
	drafts      []map[string]any
	collections []map[string]any
	unexpected  []string
	block       chan struct{}
	entered     chan struct{}
	listCalls   int
	infoCalls   int
}

func num(v any) float64 {
	n, _ := v.(float64)
	return n
}

func newFakeOutline(t *testing.T) (*fakeOutline, *httptest.Server) {
	t.Helper()
	ts := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	f := &fakeOutline{
		docs: []map[string]any{
			{"id": "d1", "title": "Root", "text": "# Hello", "url": "/doc/root-abc123", "urlId": "abc123", "parentDocumentId": nil, "collectionId": "c1", "updatedAt": ts},
			{"id": "d2", "title": "Child", "text": "child text", "url": "/doc/child-def456", "urlId": "def456", "parentDocumentId": "d1", "collectionId": "c1", "updatedAt": ts},
		},
		collections: []map[string]any{{"id": "c1", "name": "Wiki"}},
		entered:     make(chan struct{}, 1),
	}
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": v})
	}
	payload := func(r *http.Request) map[string]any {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		return p
	}
	page := func(items []map[string]any, p map[string]any) []map[string]any {
		offset := int(num(p["offset"]))
		limit := int(num(p["limit"]))
		if limit <= 0 {
			limit = 100
		}
		if offset > len(items) {
			offset = len(items)
		}
		end := offset + limit
		if end > len(items) {
			end = len(items)
		}
		return items[offset:end]
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
		p := payload(r)
		f.mu.Lock()
		cols := append([]map[string]any(nil), f.collections...)
		f.mu.Unlock()
		write(w, page(cols, p))
	})
	mux.HandleFunc("/api/collections.documents", func(w http.ResponseWriter, r *http.Request) {
		p := payload(r)
		id, _ := p["id"].(string)
		f.mu.Lock()
		docs := append([]map[string]any(nil), f.docs...)
		f.mu.Unlock()
		children := map[string][]map[string]any{}
		var roots []map[string]any
		for _, d := range docs {
			if d["collectionId"] != id {
				continue
			}
			n := map[string]any{"id": d["id"], "title": d["title"], "url": d["url"], "children": []map[string]any{}}
			parent, _ := d["parentDocumentId"].(string)
			if parent == "" {
				roots = append(roots, n)
			} else {
				children[parent] = append(children[parent], n)
			}
		}
		var attach func(ns []map[string]any)
		attach = func(ns []map[string]any) {
			for _, n := range ns {
				kids := children[n["id"].(string)]
				n["children"] = kids
				attach(kids)
			}
		}
		attach(roots)
		write(w, roots)
	})
	mux.HandleFunc("/api/documents.list", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.listCalls++
		block := f.block
		f.mu.Unlock()
		if block != nil {
			select {
			case f.entered <- struct{}{}:
			default:
			}
			select {
			case <-block:
			case <-r.Context().Done():
				return
			}
		}
		p := payload(r)
		f.mu.Lock()
		docs := append([]map[string]any(nil), f.docs...)
		f.mu.Unlock()
		if p["sort"] == "updatedAt" {
			sort.Slice(docs, func(i, j int) bool {
				a, _ := docs[i]["updatedAt"].(string)
				b, _ := docs[j]["updatedAt"].(string)
				return a > b
			})
		}
		write(w, page(docs, p))
	})
	mux.HandleFunc("/api/documents.drafts", func(w http.ResponseWriter, r *http.Request) {
		p := payload(r)
		f.mu.Lock()
		drafts := append([]map[string]any(nil), f.drafts...)
		f.mu.Unlock()
		write(w, page(drafts, p))
	})
	mux.HandleFunc("/api/documents.info", func(w http.ResponseWriter, r *http.Request) {
		p := payload(r)
		id, _ := p["id"].(string)
		f.mu.Lock()
		f.infoCalls++
		for _, d := range f.docs {
			if d["id"] == id {
				f.mu.Unlock()
				write(w, d)
				return
			}
		}
		for _, d := range f.drafts {
			if d["id"] == id {
				f.mu.Unlock()
				write(w, d)
				return
			}
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"ok":false,"error":"not_found"}`))
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
	require.NoError(t, a.UnlockDB("test-master-pass"))
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
	assert.True(t, s.HasKey)

	require.NoError(t, a.SetConnection(srv2.URL, ""))
	s, err = a.GetSettings()
	require.NoError(t, err)
	assert.Equal(t, srv2.URL, s.APIURL)
	assert.True(t, s.HasKey)

	raw, err := a.getDB().GetSettings()
	require.NoError(t, err)
	assert.Equal(t, "good", raw.APIKey)
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

func TestSyncUpdatedTitles(t *testing.T) {
	f, srv := newFakeOutline(t)
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))
	_, _, err := a.syncGuarded(false, true)
	require.NoError(t, err)

	_, titles, err := a.syncGuarded(false, true)
	require.NoError(t, err)
	assert.Empty(t, titles)

	f.mu.Lock()
	f.docs[0]["updatedAt"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	f.docs[0]["text"] = "# Updated"
	f.mu.Unlock()

	_, titles, err = a.syncGuarded(false, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"Root"}, titles)

	doc, err := a.GetDocument("d1")
	require.NoError(t, err)
	assert.Equal(t, "# Updated", doc.Text)
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
	require.NoError(t, a.UnlockDB("test-master-pass"))
	s, err := a.GetSettings()
	require.NoError(t, err)
	assert.Empty(t, s.APIURL)
	assert.False(t, s.HasKey)

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
	require.NoError(t, a.UnlockDB("test-master-pass"))
	require.NoError(t, a.SetConnection(srv.URL, "good"))
	_, err := a.SyncDocuments()
	require.NoError(t, err)
	require.NoError(t, a.getDB().Close())

	b := NewApp()
	b.dbPath = path
	require.NoError(t, b.UnlockDB("test-master-pass"))
	t.Cleanup(func() {
		if db := b.getDB(); db != nil {
			_ = db.Close()
		}
	})

	s, err := b.GetSettings()
	require.NoError(t, err)
	assert.Equal(t, srv.URL, s.APIURL)
	assert.True(t, s.HasKey)
	raw, err := b.getDB().GetSettings()
	require.NoError(t, err)
	assert.Equal(t, "good", raw.APIKey)

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
	defer func() { _ = db2.Close() }()
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

	require.NoError(t, a.UnlockDB("test-master-pass"))
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

func TestUnlockProgressiveThrottle(t *testing.T) {
	_, srv := newFakeOutline(t)
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))
	require.NoError(t, a.LockApp())

	require.Error(t, a.UnlockDB("wrong-pass-1"))
	require.Error(t, a.UnlockDB("wrong-pass-2"))
	assert.Equal(t, 0, a.UnlockRetryAfter())

	require.Error(t, a.UnlockDB("wrong-pass-3"))
	assert.Greater(t, a.UnlockRetryAfter(), 0)

	err := a.UnlockDB("test-master-pass")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "слишком много")
}

func TestCollectionsPagination(t *testing.T) {
	f, srv := newFakeOutline(t)
	f.mu.Lock()
	f.docs = nil
	f.collections = nil
	for i := 0; i < 250; i++ {
		f.collections = append(f.collections, map[string]any{"id": fmt.Sprintf("c%d", i), "name": fmt.Sprintf("Col %d", i)})
	}
	f.mu.Unlock()
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))
	_, err := a.SyncDocuments()
	require.NoError(t, err)
	tree, err := a.GetTree()
	require.NoError(t, err)
	assert.Len(t, tree, 250)
}

func TestIncrementalSyncSkipsUnchanged(t *testing.T) {
	f, srv := newFakeOutline(t)
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))
	_, err := a.SyncDocuments()
	require.NoError(t, err)

	f.mu.Lock()
	f.listCalls = 0
	f.infoCalls = 0
	f.docs[0]["updatedAt"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	f.docs[0]["text"] = "# Changed"
	f.mu.Unlock()

	res, err := a.SyncDocuments()
	require.NoError(t, err)
	assert.Equal(t, 1, res.Updated)
	assert.Equal(t, 0, res.Added)

	f.mu.Lock()
	assert.Equal(t, 0, f.infoCalls)
	assert.Equal(t, 1, f.listCalls)
	f.mu.Unlock()

	doc, err := a.GetDocument("d2")
	require.NoError(t, err)
	assert.Equal(t, "child text", doc.Text)
}

func TestDraftsSurviveSync(t *testing.T) {
	f, srv := newFakeOutline(t)
	f.mu.Lock()
	f.drafts = []map[string]any{{"id": "dr1", "title": "Draft", "text": "draft text", "urlId": "dr1slug", "collectionId": "c1", "updatedAt": time.Now().UTC().Format(time.RFC3339)}}
	f.mu.Unlock()
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))
	_, err := a.SyncDocuments()
	require.NoError(t, err)
	_, err = a.GetDocument("dr1")
	require.NoError(t, err)
	_, err = a.SyncDocuments()
	require.NoError(t, err)
	doc, err := a.GetDocument("dr1")
	require.NoError(t, err)
	assert.Equal(t, "draft text", doc.Text)
}

func TestLockDuringSyncIsInstant(t *testing.T) {
	f, srv := newFakeOutline(t)
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))

	block := make(chan struct{})
	f.mu.Lock()
	f.block = block
	f.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := a.SyncDocuments()
		done <- err
	}()

	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("sync did not reach documents.list")
	}

	start := time.Now()
	require.NoError(t, a.LockApp())
	assert.Less(t, time.Since(start), 500*time.Millisecond)

	close(block)
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("sync did not finish")
	}

	st, err := a.StorageStatus()
	require.NoError(t, err)
	assert.True(t, st.Locked)
}

func TestIdleLock(t *testing.T) {
	_, srv := newFakeOutline(t)
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))
	a.mu.Lock()
	a.idleTimeout = time.Minute
	a.lastActivity = time.Now().Add(-2 * time.Minute)
	a.mu.Unlock()
	a.checkIdle()
	st, err := a.StorageStatus()
	require.NoError(t, err)
	assert.True(t, st.Locked)
}

func TestNotifyActivityPreventsIdleLock(t *testing.T) {
	_, srv := newFakeOutline(t)
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))
	a.mu.Lock()
	a.idleTimeout = time.Minute
	a.lastActivity = time.Now().Add(-2 * time.Minute)
	a.mu.Unlock()
	a.NotifyActivity()
	a.checkIdle()
	st, err := a.StorageStatus()
	require.NoError(t, err)
	assert.False(t, st.Locked)
}

func newUnlockedApp(t *testing.T) *App {
	t.Helper()
	a := NewApp()
	a.dbPath = filepath.Join(t.TempDir(), "cache.db")
	require.NoError(t, a.UnlockDB("test-master-pass"))
	t.Cleanup(func() {
		if db := a.getDB(); db != nil {
			_ = db.Close()
		}
	})
	return a
}

func TestIncrementalSyncKeepsFullWatermark(t *testing.T) {
	a := newUnlockedApp(t)
	db := a.getDB()
	full := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, db.SetLastFullSync(full))

	for i := 0; i < 3; i++ {
		require.NoError(t, db.SetSyncState(full.Add(time.Duration(i+1)*time.Minute), 0, 0, 0))
		assert.WithinDuration(t, full, db.GetLastFullSync(), 2*time.Second)
		assert.False(t, a.needFullSync())
	}
}

func TestPrefsPersistAcrossLock(t *testing.T) {
	a := newUnlockedApp(t)
	require.NoError(t, a.SetPrefs(map[string]string{
		"theme": "light", "sideWidth": "320", "expanded": `["d1"]`, "bogus": "x",
	}))
	require.NoError(t, a.LockApp())
	require.NoError(t, a.UnlockDB("test-master-pass"))

	p, err := a.GetPrefs()
	require.NoError(t, err)
	assert.Equal(t, "light", p["theme"])
	assert.Equal(t, "320", p["sideWidth"])
	assert.Equal(t, `["d1"]`, p["expanded"])
	assert.NotContains(t, p, "bogus")

	require.NoError(t, a.SetPrefs(map[string]string{"theme": "dark"}))
	p, err = a.GetPrefs()
	require.NoError(t, err)
	assert.Equal(t, "dark", p["theme"])
	assert.Equal(t, "320", p["sideWidth"])
}

func TestLegacyPlaintextCacheIsEncryptedInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	db, err := storage.NewDB(path, "")
	require.NoError(t, err)
	require.NoError(t, db.SaveSettings(&storage.Settings{APIURL: "https://old.example", APIKey: "legacy-key"}))
	require.NoError(t, db.Close())
	require.True(t, storage.IsPlaintextDB(path))

	a := NewApp()
	a.dbPath = path
	t.Cleanup(func() {
		if d := a.getDB(); d != nil {
			_ = d.Close()
		}
	})

	st, err := a.StorageStatus()
	require.NoError(t, err)
	assert.True(t, st.Legacy)

	require.NoError(t, a.UnlockDB("brand-new-master"))
	assert.False(t, storage.IsPlaintextDB(path))

	s, err := a.GetSettings()
	require.NoError(t, err)
	assert.Equal(t, "https://old.example", s.APIURL)
	assert.True(t, s.HasKey)

	st, err = a.StorageStatus()
	require.NoError(t, err)
	assert.False(t, st.Legacy)
}

func TestUnreadableStoredKeyIsSurfaced(t *testing.T) {
	a := newUnlockedApp(t)
	require.NoError(t, a.getDB().SaveSettings(&storage.Settings{
		APIURL: "https://old.example", APIKey: "enc:AAECAwQ=",
	}))

	require.NoError(t, a.LockApp())
	require.NoError(t, a.UnlockDB("test-master-pass"))

	s, err := a.GetSettings()
	require.NoError(t, err)
	assert.False(t, s.HasKey)
	assert.True(t, s.KeyLost)
	assert.Nil(t, a.getClient())

	raw, err := a.getDB().GetSettings()
	require.NoError(t, err)
	assert.Empty(t, raw.APIKey)
}

func TestContextIsNotExposedToBindings(t *testing.T) {
	_, ok := reflect.TypeOf(NewApp()).MethodByName("Context")
	assert.False(t, ok)
}

func TestFullSyncWatermarkEndToEnd(t *testing.T) {
	_, srv := newFakeOutline(t)
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))

	_, err := a.SyncDocumentsFull()
	require.NoError(t, err)
	full := a.getDB().GetLastFullSync()
	require.False(t, full.IsZero())

	for i := 0; i < 3; i++ {
		_, err := a.SyncDocuments()
		require.NoError(t, err)
		assert.WithinDuration(t, full, a.getDB().GetLastFullSync(), 2*time.Second)
		assert.False(t, a.needFullSync())
	}
}

func TestLockUnlockWhileSyncInFlight(t *testing.T) {
	f, srv := newFakeOutline(t)
	a := newTestApp(t)
	require.NoError(t, a.SetConnection(srv.URL, "good"))

	block := make(chan struct{})
	f.mu.Lock()
	f.block = block
	f.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := a.SyncDocuments()
		done <- err
	}()

	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("sync did not reach documents.list")
	}

	require.NoError(t, a.LockApp())
	require.NoError(t, a.UnlockDB("test-master-pass"))
	close(block)
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("sync did not finish")
	}

	res, err := a.SyncDocuments()
	require.NoError(t, err)
	assert.Equal(t, 2, res.Total)

	doc, err := a.GetDocument("d1")
	require.NoError(t, err)
	assert.Equal(t, "Root", doc.Title)
}
