package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"outline-reader/outline"
	"outline-reader/storage"
)

type App struct {
	mu      sync.RWMutex
	once    sync.Once
	initErr error
	db      *storage.DB
	client  *outline.Client
	dbPath  string
	version string
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
		dir, err := os.UserConfigDir()
		if err != nil {
			dir = "."
		}
		dir = filepath.Join(dir, "OutlineReader")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("[APP] config dir: %v", err)
			dir = "."
		}
		a.dbPath = filepath.Join(dir, "cache.db")
	}
	db, err := storage.NewDB(a.dbPath)
	if err != nil {
		return fmt.Errorf("db init: %w", err)
	}
	a.db = db
	s, err := db.GetSettings()
	if err == nil && s.APIURL != "" && s.APIKey != "" {
		a.setClient(outline.NewClient(s.APIURL, s.APIKey))
	}
	log.Printf("[APP] started, db=%s", a.dbPath)
	return nil
}

func (a *App) startup(_ context.Context) { _ = a.ensure() }

func (a *App) setClient(c *outline.Client) { a.mu.Lock(); a.client = c; a.mu.Unlock() }
func (a *App) getClient() *outline.Client  { a.mu.RLock(); defer a.mu.RUnlock(); return a.client }

func (a *App) ready() error {
	if err := a.ensure(); err != nil {
		return err
	}
	if a.db == nil {
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
	s, err := a.db.GetSettings()
	if err != nil {
		return ConnectionSettings{}, err
	}
	return ConnectionSettings{APIURL: s.APIURL, APIKey: s.APIKey}, nil
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
	if err := a.db.SaveSettings(&storage.Settings{APIURL: apiURL, APIKey: apiKey}); err != nil {
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
	cols, err := a.db.GetCollections()
	if err != nil {
		return nil, err
	}
	docs, err := a.db.GetDocuments()
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
	return a.db.GetDocumentByOutlineID(outlineID)
}

func (a *App) GetDocumentByURLID(urlID string) (*storage.Document, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a.db.GetDocumentByURLID(urlID)
}

func (a *App) SearchDocuments(query string) ([]storage.Document, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return []storage.Document{}, nil
	}
	return a.db.SearchDocuments(query)
}

func (a *App) SyncDocuments() (int, error) {
	if err := a.ready(); err != nil {
		return 0, err
	}
	client := a.getClient()
	if client == nil {
		return 0, fmt.Errorf("подключение не настроено")
	}

	cols, err := client.Collections()
	if err != nil {
		return 0, err
	}
	content, err := client.AllDocuments()
	if err != nil {
		return 0, err
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
	if err := a.db.ReplaceAll(scols, docs); err != nil {
		return 0, err
	}

	if maxUpdated.IsZero() {
		maxUpdated = time.Now()
	}
	_ = a.db.SetLastSyncTime(maxUpdated)
	log.Printf("[SYNC] collections=%d documents=%d", len(scols), len(docs))
	return len(docs), nil
}

func (a *App) CheckForUpdates() ([]string, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	client := a.getClient()
	if client == nil {
		return nil, nil
	}
	last := a.db.GetLastSyncTime()
	if last.IsZero() {
		_, err := a.SyncDocuments()
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
	if _, err := a.SyncDocuments(); err != nil {
		return nil, err
	}
	return titles, nil
}

func (a *App) imageMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/img" {
			a.serveImage(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) serveImage(w http.ResponseWriter, r *http.Request) {
	client := a.getClient()
	if client == nil {
		http.Error(w, "not connected", http.StatusServiceUnavailable)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	resp, err := client.Attachment(id)
	if err != nil {
		log.Printf("[IMG] %v", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = io.Copy(w, resp.Body)
}

func (a *App) ResetApp() error {
	if err := a.ready(); err != nil {
		return err
	}
	a.setClient(nil)
	old := a.db
	a.db = nil
	if err := old.Close(); err != nil {
		return fmt.Errorf("close db: %w", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(a.dbPath + suffix); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", a.dbPath+suffix, err)
		}
	}
	var err error
	a.db, err = storage.NewDB(a.dbPath)
	if err != nil {
		return fmt.Errorf("reinit db: %w", err)
	}
	log.Printf("[APP] local storage reset")
	return nil
}
