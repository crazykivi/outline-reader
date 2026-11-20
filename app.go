package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"outline-reader/crypto"
	"outline-reader/outline"
	"outline-reader/storage"
)

const syncInterval = time.Minute
const fullSyncInterval = 24 * time.Hour
const syncOverlap = 5 * time.Minute
const idleCheckInterval = 15 * time.Second
const maxBlobSize = 32 << 20

var attachmentRe = regexp.MustCompile(`attachments\.redirect\?id=([0-9a-fA-F-]{36})`)
var errSyncInProgress = errors.New("синхронизация уже выполняется")

type App struct {
	mu           sync.RWMutex
	opMu         sync.Mutex
	syncMu       sync.Mutex
	blobMu       sync.Mutex
	once         sync.Once
	initErr      error
	db           *storage.DB
	client       *outline.Client
	dbPath       string
	dataDir      string
	portable     bool
	version      string
	ctx          context.Context
	uiEvents     bool
	syncCancel   context.CancelFunc
	blobCancel   context.CancelFunc
	lastActivity time.Time
	idleTimeout  time.Duration
}

type unlockGuard struct {
	Fails int   `json:"fails"`
	Until int64 `json:"until"`
}

func NewApp() *App { return &App{idleTimeout: 5 * time.Minute} }

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
	a.cleanupLeftovers()
	log.Printf("[APP] база заблокирована, нужен мастер-пароль (portable=%v)", a.portable)
	return nil
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

func (a *App) cleanupLeftovers() {
	plain := a.dbPath + ".plain"
	if _, err := os.Stat(plain); err != nil {
		return
	}
	suffixes := []string{"", "-wal", "-shm", "-journal"}
	if dbFileExists(a.dbPath) && !storage.IsPlaintextDB(a.dbPath) {
		for _, suf := range suffixes {
			_ = os.Remove(plain + suf)
		}
		log.Printf("[DB] удалена plaintext-копия после завершённой миграции")
	}
}

func (a *App) guardPath() string { return filepath.Join(a.dataDir, "unlock.guard") }

func (a *App) readGuard() unlockGuard {
	var g unlockGuard
	if b, err := os.ReadFile(a.guardPath()); err == nil {
		_ = json.Unmarshal(b, &g)
	}
	return g
}

func (a *App) writeGuard(g unlockGuard) {
	if b, err := json.Marshal(g); err == nil {
		_ = os.WriteFile(a.guardPath(), b, 0o600)
	}
}

func (a *App) clearGuard() { _ = os.Remove(a.guardPath()) }

func guardDelay(fails int) time.Duration {
	if fails <= 2 {
		return 0
	}
	d := 5 * time.Second
	for i := 3; i < fails && d < 5*time.Minute; i++ {
		d *= 2
	}
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

func (a *App) UnlockRetryAfter() int {
	if err := a.ensure(); err != nil {
		return 0
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	rem := a.readGuard().Until - time.Now().Unix()
	if rem < 0 {
		rem = 0
	}
	return int(rem)
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	_ = a.ensure()
	a.NotifyActivity()
	go a.syncLoop(ctx)
	go a.idleLoop(ctx)
	if a.uiEvents {
		go a.watchSession(ctx)
	}
}

func (a *App) Context() context.Context { return a.ctx }

func (a *App) shutdown() {
	a.cancelWork()
	if db := a.getDB(); db != nil {
		_ = db.Close()
	}
}

func (a *App) emit(name string, data ...any) {
	if a.uiEvents && a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, name, data...)
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
	Legacy   bool   `json:"legacy"`
	DataDir  string `json:"dataDir"`
}

func dbFileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Size() > 0
}

func (a *App) StorageStatus() (StorageStatus, error) {
	if err := a.ensure(); err != nil {
		return StorageStatus{}, err
	}
	hasDB := dbFileExists(a.dbPath)
	return StorageStatus{
		Portable: a.portable,
		Locked:   a.getDB() == nil,
		HasDB:    hasDB,
		Legacy:   hasDB && !crypto.HasMasterSalt(a.dataDir),
		DataDir:  a.dataDir,
	}, nil
}

func (a *App) cancelWork() {
	a.mu.Lock()
	if a.syncCancel != nil {
		a.syncCancel()
		a.syncCancel = nil
	}
	if a.blobCancel != nil {
		a.blobCancel()
		a.blobCancel = nil
	}
	a.mu.Unlock()
}

func (a *App) LockApp() error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.cancelWork()
	a.setClient(nil)
	db := a.getDB()
	if db == nil {
		return nil
	}
	_ = db.Close()
	a.setDB(nil)
	log.Printf("[APP] база заблокирована")
	a.emit("app:locked")
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
	if g := a.readGuard(); time.Now().Unix() < g.Until {
		return fmt.Errorf("слишком много попыток, повторите через %d с", g.Until-time.Now().Unix())
	}
	if !dbFileExists(a.dbPath) {
		if len(passphrase) < 8 {
			return fmt.Errorf("мастер-пароль должен быть не короче 8 символов")
		}
		keyHex, err := crypto.CreateMasterKey(a.dataDir, passphrase)
		if err != nil {
			return err
		}
		if err := a.openDB(keyHex); err != nil {
			return err
		}
		a.clearGuard()
		a.NotifyActivity()
		return nil
	}
	if !crypto.HasMasterSalt(a.dataDir) {
		return fmt.Errorf("база создана без мастер-пароля (старый формат): очистите локальную базу и создайте заново")
	}
	if len(passphrase) < 8 {
		return fmt.Errorf("введите мастер-пароль")
	}
	keyHex, err := crypto.DeriveMasterKey(a.dataDir, passphrase)
	if err != nil {
		return err
	}
	if err := a.openDB(keyHex); err != nil {
		g := a.readGuard()
		g.Fails++
		sec := int64(guardDelay(g.Fails) / time.Second)
		g.Until = time.Now().Unix() + sec
		a.writeGuard(g)
		log.Printf("[AUTH] неверный мастер-пароль, попытка %d, блокировка %d с", g.Fails, sec)
		return fmt.Errorf("неверный мастер-пароль или база повреждена")
	}
	a.clearGuard()
	a.NotifyActivity()
	return nil
}

func (a *App) NotifyActivity() {
	a.mu.Lock()
	a.lastActivity = time.Now()
	a.mu.Unlock()
}

func (a *App) checkIdle() {
	if a.getDB() == nil {
		return
	}
	a.mu.RLock()
	last := a.lastActivity
	timeout := a.idleTimeout
	a.mu.RUnlock()
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if time.Since(last) > timeout {
		log.Printf("[APP] idle lock")
		_ = a.LockApp()
	}
}

func (a *App) idleLoop(ctx context.Context) {
	t := time.NewTicker(idleCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.checkIdle()
		}
	}
}

func (a *App) syncLoop(ctx context.Context) {
	t := time.NewTicker(syncInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			db := a.getDB()
			if db == nil || a.getClient() == nil {
				continue
			}
			full := time.Since(db.GetLastFullSync()) >= fullSyncInterval
			res, titles, err := a.syncGuarded(full, false)
			if errors.Is(err, errSyncInProgress) {
				continue
			}
			if err != nil {
				log.Printf("[SYNC] periodic: %v", err)
				a.emit("sync:state", map[string]any{"ok": false})
				continue
			}
			a.emit("sync:state", map[string]any{
				"ok": true, "titles": titles,
				"total": res.Total, "added": res.Added, "updated": res.Updated,
				"deleted": res.Deleted, "meta": res.Meta,
			})
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
	HasKey bool   `json:"hasKey"`
}

func (a *App) GetSettings() (ConnectionSettings, error) {
	if err := a.ready(); err != nil {
		return ConnectionSettings{}, err
	}
	s, err := a.getDB().GetSettings()
	if err != nil {
		return ConnectionSettings{}, err
	}
	return ConnectionSettings{APIURL: s.APIURL, HasKey: s.APIKey != ""}, nil
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
	u, err := url.Parse(apiURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("укажите корректный URL: ожидается http:// или https://")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		s, err := a.getDB().GetSettings()
		if err != nil {
			return err
		}
		apiKey = crypto.DecryptIfPossible(s.APIKey)
		if apiKey == "" {
			return fmt.Errorf("укажите API-ключ")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := outline.NewClient(apiURL, apiKey)
	if err := client.CheckAuth(ctx); err != nil {
		return fmt.Errorf("проверка подключения: %w", err)
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if a.getDB() == nil {
		return fmt.Errorf("хранилище заблокировано")
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
	Meta    int `json:"meta"`
}

func (a *App) SyncDocuments() (SyncResult, error) {
	res, _, err := a.syncGuarded(false, true)
	return res, err
}

func (a *App) SyncDocumentsFull() (SyncResult, error) {
	res, _, err := a.syncGuarded(true, true)
	return res, err
}

func (a *App) TrySyncDocuments() (SyncResult, error) {
	res, _, err := a.syncGuarded(false, false)
	return res, err
}

func (a *App) syncGuarded(full, wait bool) (SyncResult, []string, error) {
	if wait {
		a.syncMu.Lock()
	} else if !a.syncMu.TryLock() {
		return SyncResult{}, nil, errSyncInProgress
	}
	defer a.syncMu.Unlock()
	if err := a.ready(); err != nil {
		return SyncResult{}, nil, err
	}
	client := a.getClient()
	if client == nil {
		return SyncResult{}, nil, fmt.Errorf("подключение не настроено")
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.syncCancel = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		a.syncCancel = nil
		a.mu.Unlock()
	}()
	return a.syncCore(ctx, client, full)
}

type docPlacement struct {
	colID  string
	parent string
	pos    int
}

func (a *App) syncCore(ctx context.Context, client *outline.Client, full bool) (SyncResult, []string, error) {
	db := a.getDB()
	cols, err := client.Collections(ctx)
	if err != nil {
		return SyncResult{}, nil, err
	}
	live := make(map[string]docPlacement)
	failedCols := make(map[string]bool)
	pos := 0
	var walk func(nodes []outline.NavNode, colID, parent string)
	walk = func(nodes []outline.NavNode, colID, parent string) {
		for _, n := range nodes {
			pos++
			if _, ok := live[n.ID]; !ok {
				live[n.ID] = docPlacement{colID: colID, parent: parent, pos: pos}
			}
			walk(n.Children, colID, n.ID)
		}
	}
	for _, col := range cols {
		nodes, err := client.CollectionTree(ctx, col.ID)
		if err != nil {
			log.Printf("[SYNC] tree %s: %v", col.Name, err)
			failedCols[col.ID] = true
			continue
		}
		walk(nodes, col.ID, "")
	}
	drafts, err := client.Drafts(ctx)
	if err != nil {
		return SyncResult{}, nil, err
	}
	last := db.GetLastSyncTime()
	fullMode := full || last.IsZero()
	var changed []outline.Document
	if fullMode {
		changed, err = client.AllDocuments(ctx)
	} else {
		changed, err = client.UpdatedSince(ctx, last.Add(-syncOverlap))
	}
	if err != nil {
		return SyncResult{}, nil, err
	}
	byID := make(map[string]outline.Document, len(changed)+len(drafts))
	for _, d := range changed {
		byID[d.ID] = d
	}
	for _, d := range drafts {
		if _, ok := byID[d.ID]; !ok {
			byID[d.ID] = d
		}
	}
	old, err := db.GetDocumentsMeta()
	if err != nil {
		return SyncResult{}, nil, err
	}
	oldMap := make(map[string]storage.Document, len(old))
	for _, d := range old {
		oldMap[d.OutlineID] = d
	}
	needed := make(map[string]bool, len(live)+len(drafts))
	for id := range live {
		needed[id] = true
	}
	for _, d := range drafts {
		needed[d.ID] = true
	}
	for id := range needed {
		if ctx.Err() != nil {
			return SyncResult{}, nil, ctx.Err()
		}
		if _, ok := oldMap[id]; ok {
			continue
		}
		if _, ok := byID[id]; ok {
			continue
		}
		d, err := client.DocumentInfo(ctx, id)
		if err != nil {
			log.Printf("[SYNC] info %s: %v", id, err)
			continue
		}
		byID[d.ID] = *d
	}
	res := SyncResult{}
	var titles []string
	var maxUpdated time.Time
	upserts := make([]storage.Document, 0, len(byID))
	for id, d := range byID {
		if d.UpdatedAt.After(maxUpdated) {
			maxUpdated = d.UpdatedAt
		}
		colID, parent, position := d.CollectionID, d.ParentID, 0
		if p, ok := live[id]; ok {
			colID, parent, position = p.colID, p.parent, p.pos
		} else if prev, ok := oldMap[id]; ok {
			position = prev.Position
		}
		prev, existed := oldMap[id]
		if !existed {
			res.Added++
		} else if !d.UpdatedAt.Equal(prev.UpdatedAt) {
			res.Updated++
			titles = append(titles, d.Title)
		}
		upserts = append(upserts, storage.Document{
			OutlineID: id, CollectionID: colID, ParentID: parent,
			UrlID: d.UrlID, Url: d.URL, Title: d.Title, Text: d.Text,
			Position: position, UpdatedAt: d.UpdatedAt,
		})
	}
	metaUpdates := make([]storage.Document, 0)
	for id, p := range live {
		if _, ok := byID[id]; ok {
			continue
		}
		prev, ok := oldMap[id]
		if !ok {
			continue
		}
		if prev.CollectionID != p.colID || prev.ParentID != p.parent || prev.Position != p.pos {
			metaUpdates = append(metaUpdates, storage.Document{
				OutlineID: id, CollectionID: p.colID, ParentID: p.parent, Position: p.pos,
			})
		}
	}
	deleteIDs := make([]string, 0)
	for id, prev := range oldMap {
		if needed[id] {
			continue
		}
		if _, ok := byID[id]; ok {
			continue
		}
		if failedCols[prev.CollectionID] {
			continue
		}
		deleteIDs = append(deleteIDs, id)
	}
	res.Deleted = len(deleteIDs)
	res.Meta = len(metaUpdates)
	if ctx.Err() != nil {
		return SyncResult{}, nil, ctx.Err()
	}
	scols := make([]storage.Collection, 0, len(cols))
	for i, c := range cols {
		scols = append(scols, storage.Collection{OutlineID: c.ID, Name: c.Name, Position: i})
	}
	if err := db.ApplySync(scols, upserts, metaUpdates, deleteIDs); err != nil {
		return SyncResult{}, nil, err
	}
	res.Total = int(db.CountDocuments())
	watermark := maxUpdated
	if watermark.Before(last) {
		watermark = last
	}
	if watermark.IsZero() {
		watermark = time.Now()
	}
	if err := db.SetSyncState(watermark, res.Added, res.Updated, res.Deleted); err != nil {
		log.Printf("[SYNC] state: %v", err)
	}
	if fullMode {
		if err := db.SetLastFullSync(time.Now()); err != nil {
			log.Printf("[SYNC] full state: %v", err)
		}
	}
	log.Printf("[SYNC] collections=%d upserts=%d meta=%d +%d ~%d -%d", len(scols), len(upserts), res.Meta, res.Added, res.Updated, res.Deleted)
	go a.prefetchBlobs(upserts, fullMode)
	return res, titles, nil
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

func (a *App) prefetchBlobs(docs []storage.Document, full bool) {
	if !a.blobMu.TryLock() {
		return
	}
	defer a.blobMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.blobCancel = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		a.blobCancel = nil
		a.mu.Unlock()
	}()
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
	haveSet := make(map[string]bool, len(have))
	for _, id := range have {
		haveSet[id] = true
	}
	if full {
		ids := make([]string, 0, len(want))
		for id := range want {
			ids = append(ids, id)
		}
		_ = db.DeleteBlobsNotIn(ids)
	}
	ok, fail := 0, 0
	for id := range want {
		if ctx.Err() != nil || a.getDB() == nil || a.getClient() == nil {
			return
		}
		if haveSet[id] {
			continue
		}
		resp, err := client.Attachment(ctx, id)
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

func writeBlobHeaders(w http.ResponseWriter, ct string) {
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !strings.HasPrefix(ct, "image/") || strings.HasPrefix(ct, "image/svg") {
		w.Header().Set("Content-Disposition", "attachment")
	}
}

func (a *App) serveImage(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	if db := a.getDB(); db != nil {
		if b, err := db.GetBlob(id); err == nil {
			writeBlobHeaders(w, b.Mime)
			_, _ = w.Write(b.Data)
			return
		}
	}
	client := a.getClient()
	if client == nil {
		http.Error(w, "offline: attachment not cached", http.StatusNotFound)
		return
	}
	resp, err := client.Attachment(r.Context(), id)
	if err != nil {
		log.Printf("[IMG] %v", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
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
	writeBlobHeaders(w, ct)
	_, _ = w.Write(data)
}

func (a *App) ResetApp() error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if err := a.ensure(); err != nil {
		return err
	}
	a.cancelWork()
	a.setClient(nil)
	if old := a.getDB(); old != nil {
		_ = old.Close()
		a.setDB(nil)
	}
	for _, suf := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(a.dbPath + suf)
	}
	_ = os.Remove(filepath.Join(a.dataDir, "db.salt"))
	_ = os.Remove(filepath.Join(a.dataDir, "db.key"))
	_ = os.Remove(a.guardPath())
	log.Printf("[APP] storage reset, мастер-пароль будет задан заново")
	return nil
}
