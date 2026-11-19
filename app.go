package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"outline-reader/crypto"
	"outline-reader/outline"
	"outline-reader/storage"
)

const fullSyncInterval = time.Hour
const maxBlobSize = 32 << 20

var attachmentRe = regexp.MustCompile(`attachments\.redirect\?id=([0-9a-fA-F-]{36})`)

type App struct {
	mu       sync.RWMutex
	opMu     sync.Mutex
	blobMu   sync.Mutex
	once     sync.Once
	initErr  error
	db       *storage.DB
	client   *outline.Client
	dbPath   string
	dataDir  string
	portable bool
	version  string
	ctx      context.Context
}

func NewApp() *App { return &App{} }

func (a *App) ensure() error {
	a.once.Do(func() { a.initErr = a.init() })
	return a.initErr
}

func (a *App) AppVersion() string {
	if a.version == "" {
		return "dev"
	}
	return a.version
}

func (a *App) init() error {
	if a.dbPath == "" {
		if dir := portableDataDir(exeDir()); dir != "" {
			a.dataDir = dir
			a.portable = true
		} else {
			base, err := os.UserConfigDir()
			if err != nil {
				base = "."
			}
			dir := filepath.Join(base, "OutlineReader")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				log.Printf("[APP] config dir: %v", err)
				dir = "."
			}
			a.dataDir = dir
		}
		a.dbPath = filepath.Join(a.dataDir, "cache.db")
	} else {
		a.dataDir = filepath.Dir(a.dbPath)
	}
	if a.portable {
		log.Printf("[APP] portable: база заблокирована до ввода пароля")
		return nil
	}
	keyHex, err := crypto.LoadOrCreateDPAPIKey(a.dataDir)
	if err != nil {
		return fmt.Errorf("ключ базы: %w", err)
	}
	return a.openDB(keyHex)
}

func (a *App) openDB(keyHex string) error {
	if storage.IsPlaintextDB(a.dbPath) {
		if err := storage.MigratePlaintext(a.dbPath, keyHex); err != nil {
			return fmt.Errorf("шифрование существующего кэша: %w", err)
		}
	}
	db, err := storage.NewDB(a.dbPath, keyHex)
	if err != nil {
		return err
	}
	a.setDB(db)
	s, err := db.GetSettings()
	if err == nil && s.APIURL != "" && s.APIKey != "" {
		a.setClient(outline.NewClient(s.APIURL, crypto.DecryptIfPossible(s.APIKey)))
	}
	log.Printf("[APP] база открыта, portable=%v", a.portable)
	return nil
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	_ = a.ensure()
	go a.syncLoop(ctx)
}

func (a *App) Context() context.Context { return a.ctx }

func (a *App) shutdown() {
	if db := a.getDB(); db != nil {
		_ = db.Close()
	}
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}

func portableDataDir(base string) string {
	if base == "" {
		return ""
	}
	dir := filepath.Join(base, "data")
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return ""
	}
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		log.Printf("[APP] portable dir is not writable, using user config: %v", err)
		return ""
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return dir
}

func instanceID(dataDir string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(filepath.Clean(dataDir))))
	return fmt.Sprintf("outline-reader-%08x", h.Sum32())
}

type StorageInfo struct {
	Portable bool   `json:"portable"`
	DataDir  string `json:"dataDir"`
	DBPath   string `json:"dbPath"`
}

func (a *App) StorageInfo() (StorageInfo, error) {
	if err := a.ensure(); err != nil {
		return StorageInfo{}, err
	}
	return StorageInfo{Portable: a.portable, DataDir: a.dataDir, DBPath: a.dbPath}, nil
}

type StorageStatus struct {
	Portable bool   `json:"portable"`
	Locked   bool   `json:"locked"`
	HasDB    bool   `json:"hasDB"`
	DataDir  string `json:"dataDir"`
}

func (a *App) StorageStatus() (StorageStatus, error) {
	if err := a.ensure(); err != nil {
		return StorageStatus{}, err
	}
	_, statErr := os.Stat(a.dbPath)
	return StorageStatus{
		Portable: a.portable,
		Locked:   a.getDB() == nil,
		HasDB:    statErr == nil,
		DataDir:  a.dataDir,
	}, nil
}

func (a *App) LockApp() error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.setClient(nil)
	if db := a.getDB(); db != nil {
		_ = db.Close()
		a.setDB(nil)
	}
	log.Printf("[APP] база заблокирована")
	return nil
}

func (a *App) UnlockDB(passphrase string) error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if err := a.ensure(); err != nil {
		return err
	}
	if a.getDB() != nil {
		return nil
	}
	if !a.portable {
		keyHex, err := crypto.LoadOrCreateDPAPIKey(a.dataDir)
		if err != nil {
			return err
		}
		return a.openDB(keyHex)
	}
	if len(passphrase) < 8 {
		return fmt.Errorf("пароль должен быть не короче 8 символов")
	}
	keyHex, err := crypto.DerivePortableKey(a.dataDir, passphrase)
	if err != nil {
		return err
	}
	return a.openDB(keyHex)
}

func (a *App) syncLoop(ctx context.Context) {
	t := time.NewTicker(fullSyncInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if a.getClient() == nil {
				continue
			}
			if _, err := a.SyncDocuments(); err != nil {
				log.Printf("[SYNC] periodic: %v", err)
			}
		}
	}
}

func (a *App) setClient(c *outline.Client) { a.mu.Lock(); a.client = c; a.mu.Unlock() }
func (a *App) getClient() *outline.Client  { a.mu.RLock(); defer a.mu.RUnlock(); return a.client }
func (a *App) setDB(db *storage.DB)        { a.mu.Lock(); a.db = db; a.mu.Unlock() }
func (a *App) getDB() *storage.DB          { a.mu.RLock(); defer a.mu.RUnlock(); return a.db }

func (a *App) ready() error {
	if err := a.ensure(); err != nil {
		return err
	}
	if a.getDB() == nil {
		return fmt.Errorf("хранилище не инициализировано")
	}
	return nil
}

type ConnectionSettings struct {
	APIURL string `json:"apiURL"`
	APIKey string `json:"apiKey"`
}

func (a *App) GetSettings() (ConnectionSettings, error) {
	if err := a.ready(); err != nil {
		return ConnectionSettings{}, err
	}
	s, err := a.getDB().GetSettings()
	if err != nil {
		return ConnectionSettings{}, err
	}
	return ConnectionSettings{
		APIURL: s.APIURL,
		APIKey: crypto.DecryptIfPossible(s.APIKey),
	}, nil
}

func (a *App) GetLastSyncTime() string {
	if err := a.ready(); err != nil {
		return ""
	}
	t := a.getDB().GetLastSyncTime()
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func (a *App) SetConnection(apiURL, apiKey string) error {
	if err := a.ready(); err != nil {
		return err
	}
	apiURL = strings.TrimRight(strings.TrimSpace(apiURL), "/")
	apiKey = strings.TrimSpace(apiKey)
	if apiURL == "" || apiKey == "" {
		return fmt.Errorf("укажите URL сервера и API-ключ")
	}
	client := outline.NewClient(apiURL, apiKey)
	if err := client.CheckAuth(); err != nil {
		return fmt.Errorf("проверка подключения: %w", err)
	}
	storedKey := apiKey
	if !a.portable {
		storedKey = crypto.EncryptIfPossible(apiKey)
	}
	if err := a.getDB().SaveSettings(&storage.Settings{APIURL: apiURL, APIKey: storedKey}); err != nil {
		return err
	}
	a.setClient(client)
	log.Printf("[APP] connected to %s", apiURL)
	return nil
}

type TreeNode struct {
	OID      string     `json:"oid"`
	Title    string     `json:"title"`
	Children []TreeNode `json:"children"`
}

type CollectionTree struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Nodes []TreeNode `json:"nodes"`
}

func (a *App) GetTree() ([]CollectionTree, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	db := a.getDB()
	cols, err := db.GetCollections()
	if err != nil {
		return nil, err
	}
	docs, err := db.GetDocumentsMeta()
	if err != nil {
		return nil, err
	}
	byCol := make(map[string][]storage.Document)
	for _, d := range docs {
		byCol[d.CollectionID] = append(byCol[d.CollectionID], d)
	}
	out := make([]CollectionTree, 0, len(cols))
	for _, c := range cols {
		out = append(out, CollectionTree{ID: c.OutlineID, Name: c.Name, Nodes: buildNodes(byCol[c.OutlineID])})
	}
	return out, nil
}

func buildNodes(docs []storage.Document) []TreeNode {
	present := make(map[string]bool, len(docs))
	for _, d := range docs {
		present[d.OutlineID] = true
	}
	byParent := make(map[string][]storage.Document)
	for _, d := range docs {
		p := d.ParentID
		if p != "" && !present[p] {
			p = ""
		}
		byParent[p] = append(byParent[p], d)
	}
	seen := make(map[string]bool, len(docs))
	var build func(parent string, depth int) []TreeNode
	build = func(parent string, depth int) []TreeNode {
		out := make([]TreeNode, 0)
		if depth > 32 {
			return out
		}
		for _, d := range byParent[parent] {
			if seen[d.OutlineID] {
				continue
			}
			seen[d.OutlineID] = true
			out = append(out, TreeNode{OID: d.OutlineID, Title: d.Title, Children: build(d.OutlineID, depth+1)})
		}
		return out
	}
	return build("", 0)
}

func (a *App) GetDocument(outlineID string) (*storage.Document, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	doc, err := a.getDB().GetDocumentByOutlineID(outlineID)
	if err == nil {
		_ = a.getDB().TouchRecent(outlineID)
	}
	return doc, err
}

func (a *App) GetDocumentByURLID(urlID string) (*storage.Document, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	doc, err := a.getDB().GetDocumentByURLID(urlID)
	if err == nil {
		_ = a.getDB().TouchRecent(doc.OutlineID)
	}
	return doc, err
}

func (a *App) SearchDocuments(query string) ([]storage.Document, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return []storage.Document{}, nil
	}
	return a.getDB().SearchDocuments(query)
}

func (a *App) GetRecent() ([]storage.Document, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a.getDB().GetRecentDocs()
}

func (a *App) GetFavorites() ([]storage.Document, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a.getDB().GetFavoriteDocs()
}

func (a *App) ToggleFavorite(oid string) (bool, error) {
	if err := a.ready(); err != nil {
		return false, err
	}
	return a.getDB().ToggleFavorite(oid)
}

func (a *App) IsFavorite(oid string) (bool, error) {
	if err := a.ready(); err != nil {
		return false, err
	}
	return a.getDB().IsFavorite(oid), nil
}

type SyncResult struct {
	Total   int `json:"total"`
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Deleted int `json:"deleted"`
}

func (a *App) SyncDocuments() (SyncResult, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	return a.syncLocked()
}

func (a *App) syncLocked() (SyncResult, error) {
	if err := a.ready(); err != nil {
		return SyncResult{}, err
	}
	client := a.getClient()
	if client == nil {
		return SyncResult{}, fmt.Errorf("подключение не настроено")
	}
	db := a.getDB()
	cols, err := client.Collections()
	if err != nil {
		return SyncResult{}, err
	}
	content, err := client.AllDocuments()
	if err != nil {
		return SyncResult{}, err
	}
	byID := make(map[string]outline.Document, len(content))
	var maxUpdated time.Time
	for _, d := range content {
		byID[d.ID] = d
		if d.UpdatedAt.After(maxUpdated) {
			maxUpdated = d.UpdatedAt
		}
	}
	docs := make([]storage.Document, 0, len(content))
	used := make(map[string]bool, len(content))
	pos := 0
	add := func(d outline.Document, colID, parent string) {
		pos++
		docs = append(docs, storage.Document{
			OutlineID: d.ID, CollectionID: colID, ParentID: parent,
			UrlID: d.UrlID, Url: d.URL, Title: d.Title, Text: d.Text,
			Position: pos, UpdatedAt: d.UpdatedAt,
		})
	}
	for _, col := range cols {
		nodes, err := client.CollectionTree(col.ID)
		if err != nil {
			log.Printf("[SYNC] tree %s: %v", col.Name, err)
			nodes = nil
		}
		var walk func(nodes []outline.NavNode, parent string)
		walk = func(nodes []outline.NavNode, parent string) {
			for _, n := range nodes {
				if d, ok := byID[n.ID]; ok && !used[n.ID] {
					used[n.ID] = true
					add(d, col.ID, parent)
				}
				walk(n.Children, n.ID)
			}
		}
		walk(nodes, "")
	}
	for _, d := range content {
		if !used[d.ID] {
			add(d, d.CollectionID, d.ParentID)
		}
	}
	scols := make([]storage.Collection, 0, len(cols))
	for i, c := range cols {
		scols = append(scols, storage.Collection{OutlineID: c.ID, Name: c.Name, Position: i})
	}
	old, _ := db.GetDocumentsMeta()
	oldMap := make(map[string]time.Time, len(old))
	for _, d := range old {
		oldMap[d.OutlineID] = d.UpdatedAt
	}
	res := SyncResult{Total: len(docs)}
	newSet := make(map[string]bool, len(docs))
	for _, d := range docs {
		newSet[d.OutlineID] = true
		if prev, ok := oldMap[d.OutlineID]; !ok {
			res.Added++
		} else if !d.UpdatedAt.Equal(prev) {
			res.Updated++
		}
	}
	for id := range oldMap {
		if !newSet[id] {
			res.Deleted++
		}
	}
	if err := db.ReplaceAll(scols, docs); err != nil {
		return SyncResult{}, err
	}
	if maxUpdated.IsZero() {
		maxUpdated = time.Now()
	}
	_ = db.SetSyncState(maxUpdated, res.Added, res.Updated, res.Deleted)
	log.Printf("[SYNC] collections=%d documents=%d +%d ~%d -%d", len(scols), len(docs), res.Added, res.Updated, res.Deleted)
	go a.prefetchBlobs(docs)
	return res, nil
}

func (a *App) CheckForUpdates() ([]string, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if err := a.ready(); err != nil {
		return nil, err
	}
	client := a.getClient()
	if client == nil {
		return nil, nil
	}
	last := a.getDB().GetLastSyncTime()
	if last.IsZero() {
		_, err := a.syncLocked()
		return nil, err
	}
	updated, err := client.UpdatedSince(last)
	if err != nil {
		return nil, err
	}
	if len(updated) == 0 {
		return nil, nil
	}
	titles := make([]string, 0, len(updated))
	for _, d := range updated {
		titles = append(titles, d.Title)
	}
	if _, err := a.syncLocked(); err != nil {
		return nil, err
	}
	return titles, nil
}

type SyncStats struct {
	LastSync string `json:"lastSync"`
	Total    int    `json:"total"`
	Added    int    `json:"added"`
	Updated  int    `json:"updated"`
	Deleted  int    `json:"deleted"`
}

func (a *App) GetSyncStats() (SyncStats, error) {
	if err := a.ready(); err != nil {
		return SyncStats{}, err
	}
	db := a.getDB()
	s := db.GetSyncState()
	out := SyncStats{Total: int(db.CountDocuments()), Added: s.Added, Updated: s.Updated, Deleted: s.Deleted}
	if !s.LastSync.IsZero() {
		out.LastSync = s.LastSync.UTC().Format(time.RFC3339)
	}
	return out, nil
}

func (a *App) prefetchBlobs(docs []storage.Document) {
	if !a.blobMu.TryLock() {
		return
	}
	defer a.blobMu.Unlock()
	client, db := a.getClient(), a.getDB()
	if client == nil || db == nil {
		return
	}
	want := map[string]bool{}
	for _, d := range docs {
		for _, m := range attachmentRe.FindAllStringSubmatch(d.Text, -1) {
			want[m[1]] = true
		}
	}
	have, _ := db.BlobIDs()
	haveSet := map[string]bool{}
	for _, id := range have {
		haveSet[id] = true
	}
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	_ = db.DeleteBlobsNotIn(ids)
	ok, fail := 0, 0
	for id := range want {
		if haveSet[id] {
			continue
		}
		resp, err := client.Attachment(id)
		if err != nil {
			fail++
			log.Printf("[BLOB] %s: %v", id, err)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxBlobSize+1))
		ct := resp.Header.Get("Content-Type")
		_ = resp.Body.Close()
		if err != nil || int64(len(data)) > maxBlobSize {
			fail++
			continue
		}
		if ct == "" {
			ct = "application/octet-stream"
		}
		if err := db.SaveBlob(&storage.Blob{ID: id, Mime: ct, Data: data, Size: int64(len(data)), FetchedAt: time.Now()}); err != nil {
			fail++
			continue
		}
		ok++
	}
	if ok+fail > 0 {
		log.Printf("[BLOB] закэшировано=%d ошибок=%d всего=%d", ok, fail, len(want))
	}
}

type noStoreWriter struct{ http.ResponseWriter }

func (w noStoreWriter) WriteHeader(code int) {
	w.Header().Set("Cache-Control", "no-store")
	w.ResponseWriter.WriteHeader(code)
}

func (w noStoreWriter) Write(b []byte) (int, error) {
	w.Header().Set("Cache-Control", "no-store")
	return w.ResponseWriter.Write(b)
}

func (a *App) imageMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/img" {
			a.serveImage(w, r)
			return
		}
		next.ServeHTTP(noStoreWriter{w}, r)
	})
}

func (a *App) serveImage(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	if db := a.getDB(); db != nil {
		if b, err := db.GetBlob(id); err == nil {
			w.Header().Set("Content-Type", b.Mime)
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(b.Data)
			return
		}
	}
	client := a.getClient()
	if client == nil {
		http.Error(w, "offline: attachment not cached", http.StatusNotFound)
		return
	}
	resp, err := client.Attachment(id)
	if err != nil {
		log.Printf("[IMG] %v", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBlobSize+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	if int64(len(data)) <= maxBlobSize {
		if db := a.getDB(); db != nil {
			_ = db.SaveBlob(&storage.Blob{ID: id, Mime: ct, Data: data, Size: int64(len(data)), FetchedAt: time.Now()})
		}
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (a *App) ResetApp() error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if err := a.ensure(); err != nil {
		return err
	}
	a.setClient(nil)
	if old := a.getDB(); old != nil {
		_ = old.Close()
		a.setDB(nil)
	}
	for _, suf := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(a.dbPath + suf)
	}
	_ = os.Remove(filepath.Join(a.dataDir, "db.salt"))
	if a.portable {
		log.Printf("[APP] storage reset, база снова заблокирована")
		return nil
	}
	_ = os.Remove(filepath.Join(a.dataDir, "db.key"))
	keyHex, err := crypto.LoadOrCreateDPAPIKey(a.dataDir)
	if err != nil {
		return err
	}
	return a.openDB(keyHex)
}
