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
	sess         *session
	retireMu     sync.Mutex
	retiring     []chan struct{}
	keyLost      bool
	dbPath       string
	dataDir      string
	portable     bool
	version      string
	ctx          context.Context
	uiEvents     bool
	lastActivity time.Time
	idleTimeout  time.Duration
	updMu        sync.Mutex
	lastUpdate   *UpdateInfo
	updateAPI         string
	updateInProgress  bool
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
	log.Printf("[APP] база заблокирована, нужен мастер-пароль (portable=%v)", a.portable)
	return nil
}

func (a *App) openDB(keyHex string) error {
	a.waitRetired()
	if storage.IsPlaintextDB(a.dbPath) {
		if err := storage.MigratePlaintext(a.dbPath, keyHex); err != nil {
			return fmt.Errorf("шифрование существующего кэша: %w", err)
		}
	}
	db, err := storage.NewDB(a.dbPath, keyHex)
	if err != nil {
		return err
	}
	a.cleanupPlaintextBackups()
	a.attachSession(db)
	s, err := db.GetSettings()
	if err != nil {
		log.Printf("[APP] настройки: %v", err)
		return nil
	}
	if s.APIURL != "" && s.APIKey != "" {
		key, err := crypto.UnwrapLegacy(s.APIKey)
		if err != nil {
			log.Printf("[APP] сохранённый API-ключ недоступен: %v", err)
			s.APIKey = ""
			if err := db.SaveSettings(s); err != nil {
				log.Printf("[APP] сброс ключа: %v", err)
			}
			a.setKeyLost(true)
		} else {
			if crypto.IsWrapped(s.APIKey) {
				s.APIKey = key
				if err := db.SaveSettings(s); err != nil {
					log.Printf("[APP] перенос ключа в шифрованную базу: %v", err)
				}
			}
			a.setClient(outline.NewClient(s.APIURL, key))
		}
	}
	log.Printf("[APP] база открыта, portable=%v", a.portable)
	return nil
}

func (a *App) cleanupPlaintextBackups() {
	plain := a.dbPath + ".plain"
	found := false
	for _, suf := range []string{"", "-wal", "-shm", "-journal"} {
		if _, err := os.Stat(plain + suf); err == nil {
			found = true
			break
		}
	}
	if !found {
		return
	}
	for _, suf := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(plain + suf); err != nil && !os.IsNotExist(err) {
			log.Printf("[DB] не удалось удалить резервный plaintext-файл %s: %v", plain+suf, err)
		}
	}
	log.Printf("[DB] удалена резервная plaintext-копия после успешного открытия зашифрованной базы")
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

func (a *App) appContext() context.Context { return a.ctx }

func (a *App) shutdown() {
	a.retireSession(a.detachSession())
	a.waitRetired()
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
		Locked:   !a.unlocked(),
		HasDB:    hasDB,
		Legacy:   hasDB && !crypto.HasMasterSalt(a.dataDir),
		DataDir:  a.dataDir,
	}, nil
}

func (a *App) LockApp() error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	s := a.detachSession()
	if s == nil {
		return nil
	}
	a.retireSession(s)
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
	if a.unlocked() {
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
		if len(passphrase) < 8 {
			return fmt.Errorf("мастер-пароль должен быть не короче 8 символов")
		}
		keyHex, err := crypto.CreateMasterKey(a.dataDir, passphrase)
		if err != nil {
			return err
		}
		if err := a.openDB(keyHex); err != nil {
			_ = os.Remove(filepath.Join(a.dataDir, "db.salt"))
			return err
		}
		a.clearGuard()
		a.NotifyActivity()
		return nil
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
	if !a.unlocked() {
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
			if !a.unlocked() || a.getClient() == nil {
				continue
			}
			full := a.needFullSync()
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

func (a *App) setClient(c *outline.Client) {
	a.mu.Lock()
	if a.sess != nil {
		a.sess.client = c
	}
	a.mu.Unlock()
}

func (a *App) getClient() *outline.Client {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.sess == nil {
		return nil
	}
	return a.sess.client
}

func (a *App) getDB() *storage.DB {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.sess == nil {
		return nil
	}
	return a.sess.db
}

func (a *App) unlocked() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sess != nil
}

func (a *App) setKeyLost(v bool) { a.mu.Lock(); a.keyLost = v; a.mu.Unlock() }

func (a *App) needFullSync() bool {
	sess := a.acquire()
	if sess == nil {
		return false
	}
	defer sess.done()
	return time.Since(sess.db.GetLastFullSync()) >= fullSyncInterval
}

func (a *App) session() (*session, error) {
	if err := a.ensure(); err != nil {
		return nil, err
	}
	sess := a.acquire()
	if sess == nil {
		return nil, errLocked
	}
	return sess, nil
}

type ConnectionSettings struct {
	APIURL  string `json:"apiURL"`
	HasKey  bool   `json:"hasKey"`
	KeyLost bool   `json:"keyLost"`
}

func (a *App) GetSettings() (ConnectionSettings, error) {
	sess, err := a.session()
	if err != nil {
		return ConnectionSettings{}, err
	}
	defer sess.done()
	s, err := sess.db.GetSettings()
	if err != nil {
		return ConnectionSettings{}, err
	}
	a.mu.RLock()
	lost := a.keyLost
	a.mu.RUnlock()
	return ConnectionSettings{APIURL: s.APIURL, HasKey: s.APIKey != "", KeyLost: lost}, nil
}

func (a *App) SetConnection(apiURL, apiKey string) error {
	sess, err := a.session()
	if err != nil {
		return err
	}
	defer sess.done()
	apiURL = strings.TrimRight(strings.TrimSpace(apiURL), "/")
	u, err := url.Parse(apiURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("укажите корректный URL: ожидается http:// или https://")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		s, err := sess.db.GetSettings()
		if err != nil {
			return err
		}
		stored, err := crypto.UnwrapLegacy(s.APIKey)
		if err != nil || stored == "" {
			return fmt.Errorf("укажите API-ключ")
		}
		apiKey = stored
	}
	ctx, cancel := context.WithTimeout(sess.ctx, 15*time.Second)
	defer cancel()
	client := outline.NewClient(apiURL, apiKey)
	if err := client.CheckAuth(ctx); err != nil {
		return fmt.Errorf("проверка подключения: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sess != sess {
		return fmt.Errorf("хранилище заблокировано")
	}
	if err := sess.db.SaveSettings(&storage.Settings{APIURL: apiURL, APIKey: apiKey}); err != nil {
		return err
	}
	sess.client = client
	a.keyLost = false
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
	sess, err := a.session()
	if err != nil {
		return nil, err
	}
	defer sess.done()
	db := sess.db
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
	sess, err := a.session()
	if err != nil {
		return nil, err
	}
	defer sess.done()
	doc, err := sess.db.GetDocumentByOutlineID(outlineID)
	if err == nil {
		_ = sess.db.TouchRecent(outlineID)
	}
	return doc, err
}

func (a *App) GetDocumentByURLID(urlID string) (*storage.Document, error) {
	sess, err := a.session()
	if err != nil {
		return nil, err
	}
	defer sess.done()
	doc, err := sess.db.GetDocumentByURLID(urlID)
	if err == nil {
		_ = sess.db.TouchRecent(doc.OutlineID)
	}
	return doc, err
}

func (a *App) SearchDocuments(query string) ([]storage.Document, error) {
	sess, err := a.session()
	if err != nil {
		return nil, err
	}
	defer sess.done()
	query = strings.TrimSpace(query)
	if query == "" {
		return []storage.Document{}, nil
	}
	return sess.db.SearchDocuments(query)
}

func (a *App) GetRecent() ([]storage.Document, error) {
	sess, err := a.session()
	if err != nil {
		return nil, err
	}
	defer sess.done()
	return sess.db.GetRecentDocs()
}

func (a *App) GetFavorites() ([]storage.Document, error) {
	sess, err := a.session()
	if err != nil {
		return nil, err
	}
	defer sess.done()
	return sess.db.GetFavoriteDocs()
}

func (a *App) ToggleFavorite(oid string) (bool, error) {
	sess, err := a.session()
	if err != nil {
		return false, err
	}
	defer sess.done()
	return sess.db.ToggleFavorite(oid)
}

func (a *App) IsFavorite(oid string) (bool, error) {
	sess, err := a.session()
	if err != nil {
		return false, err
	}
	defer sess.done()
	return sess.db.IsFavorite(oid), nil
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
	sess, err := a.session()
	if err != nil {
		return SyncResult{}, nil, err
	}
	defer sess.done()
	client := a.getClient()
	if client == nil {
		return SyncResult{}, nil, fmt.Errorf("подключение не настроено")
	}
	ctx, cancel := context.WithCancel(sess.ctx)
	defer cancel()
	return a.syncCore(ctx, sess, client, full)
}

type docPlacement struct {
	colID  string
	parent string
	pos    int
}

const maxCollectionTreeDepth = 256
const maxCollectionTreeNodes = 250000

type collectionTreeItem struct {
	node   outline.NavNode
	parent string
	depth  int
}

func flattenCollectionTree(nodes []outline.NavNode, colID string, startPos, maxNodes int) (map[string]docPlacement, int, error) {
	if maxNodes <= 0 {
		return nil, startPos, fmt.Errorf("collection tree node limit exceeded")
	}
	placements := make(map[string]docPlacement)
	stack := make([]collectionTreeItem, 0, len(nodes))
	for i := len(nodes) - 1; i >= 0; i-- {
		stack = append(stack, collectionTreeItem{node: nodes[i], depth: 1})
	}
	for len(stack) > 0 {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if item.depth > maxCollectionTreeDepth {
			return nil, startPos, fmt.Errorf("collection tree depth exceeds %d", maxCollectionTreeDepth)
		}
		if len(placements) >= maxNodes {
			return nil, startPos, fmt.Errorf("collection tree node limit exceeded")
		}
		if item.node.ID == "" {
			return nil, startPos, fmt.Errorf("collection tree contains an empty document ID")
		}
		if _, exists := placements[item.node.ID]; exists {
			return nil, startPos, fmt.Errorf("collection tree contains duplicate document ID")
		}
		position := startPos + len(placements) + 1
		placements[item.node.ID] = docPlacement{colID: colID, parent: item.parent, pos: position}
		for i := len(item.node.Children) - 1; i >= 0; i-- {
			stack = append(stack, collectionTreeItem{node: item.node.Children[i], parent: item.node.ID, depth: item.depth + 1})
		}
	}
	return placements, startPos + len(placements), nil
}

func syncSnapshotComplete(fullMode bool, live map[string]docPlacement, docs map[string]outline.Document, previousCount int) bool {
	if !fullMode || len(docs) < len(live) {
		return false
	}
	if previousCount > 0 && len(docs) == 0 {
		return false
	}
	for id := range live {
		if _, ok := docs[id]; !ok {
			return false
		}
	}
	return true
}

func (a *App) syncCore(ctx context.Context, sess *session, client *outline.Client, full bool) (SyncResult, []string, error) {
	db := sess.db
	cols, err := client.Collections(ctx)
	if err != nil {
		return SyncResult{}, nil, err
	}
	live := make(map[string]docPlacement)
	failedCols := make(map[string]bool)
	pos := 0
	for _, col := range cols {
		nodes, err := client.CollectionTree(ctx, col.ID)
		if err != nil {
			log.Printf("[SYNC] tree %s: %v", col.Name, err)
			failedCols[col.ID] = true
			continue
		}
		remaining := maxCollectionTreeNodes - pos
		placements, nextPos, err := flattenCollectionTree(nodes, col.ID, pos, remaining)
		if err != nil {
			log.Printf("[SYNC] tree %s rejected: %v", col.Name, err)
			failedCols[col.ID] = true
			continue
		}
		duplicate := false
		for id := range placements {
			if _, exists := live[id]; exists {
				duplicate = true
				break
			}
		}
		if duplicate {
			log.Printf("[SYNC] tree %s rejected: duplicate document ID", col.Name)
			failedCols[col.ID] = true
			continue
		}
		for id, placement := range placements {
			live[id] = placement
		}
		pos = nextPos
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
	if syncSnapshotComplete(fullMode, live, byID, len(oldMap)) {
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
	} else {
		log.Printf("[SYNC] deletion skipped: snapshot is incomplete or incremental")
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
	if ps := a.acquire(); ps != nil {
		if ps == sess {
			go func() {
				defer ps.done()
				a.prefetchBlobs(ps, upserts, fullMode)
			}()
		} else {
			ps.done()
		}
	}
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
	sess, err := a.session()
	if err != nil {
		return SyncStats{}, err
	}
	defer sess.done()
	db := sess.db
	s := db.GetSyncState()
	out := SyncStats{Total: int(db.CountDocuments()), Added: s.Added, Updated: s.Updated, Deleted: s.Deleted}
	if !s.LastSync.IsZero() {
		out.LastSync = s.LastSync.UTC().Format(time.RFC3339)
	}
	return out, nil
}

func (a *App) prefetchBlobs(sess *session, docs []storage.Document, full bool) {
	if !a.blobMu.TryLock() {
		return
	}
	defer a.blobMu.Unlock()
	ctx := sess.ctx
	client, db := a.getClient(), sess.db
	if client == nil || ctx.Err() != nil {
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
		if ctx.Err() != nil {
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
	sess := a.acquire()
	if sess != nil {
		defer sess.done()
		if b, err := sess.db.GetBlob(id); err == nil {
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
	if int64(len(data)) <= maxBlobSize && sess != nil && sess.ctx.Err() == nil {
		_ = sess.db.SaveBlob(&storage.Blob{ID: id, Mime: ct, Data: data, Size: int64(len(data)), FetchedAt: time.Now()})
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
	a.retireSession(a.detachSession())
	a.waitRetired()
	for _, suf := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(a.dbPath + suf)
	}
	_ = os.Remove(filepath.Join(a.dataDir, "db.salt"))
	_ = os.Remove(filepath.Join(a.dataDir, "db.key"))
	_ = os.Remove(a.guardPath())
	log.Printf("[APP] storage reset, мастер-пароль будет задан заново")
	return nil
}
