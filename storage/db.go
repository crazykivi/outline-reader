package storage

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/ext/fts5"
	"github.com/ncruces/go-sqlite3/gormlite"
	_ "github.com/ncruces/go-sqlite3/vfs/adiantum"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func init() {
	sqlite3.AutoExtension(fts5.Register)
}

type Document struct {
	ID           uint   `gorm:"primaryKey"`
	OutlineID    string `gorm:"uniqueIndex"`
	CollectionID string `gorm:"index"`
	ParentID     string `gorm:"index"`
	UrlID        string `gorm:"index"`
	Url          string
	Title        string
	Text         string
	Position     int
	UpdatedAt    time.Time
}

type Collection struct {
	ID        uint   `gorm:"primaryKey"`
	OutlineID string `gorm:"uniqueIndex"`
	Name      string
	Position  int
}

type Settings struct {
	ID     uint `gorm:"primaryKey"`
	APIURL string
	APIKey string
}

type SyncState struct {
	ID       uint `gorm:"primaryKey"`
	LastSync time.Time
	Added    int
	Updated  int
	Deleted  int
}

type DB struct {
	db  *gorm.DB
	fts bool
}

func dsn(dbPath, keyHex string) string {
	p := filepath.ToSlash(dbPath)
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	out := "file:" + strings.Join(segs, "/")
	if keyHex != "" {
		out += "?vfs=adiantum&hexkey=" + keyHex
	}
	return out
}

func NewDB(dbPath, keyHex string) (*DB, error) {
	db, err := gorm.Open(gormlite.Open(dsn(dbPath, keyHex)), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	_ = db.Exec("PRAGMA temp_store = memory").Error
	var n int64
	if err := db.Raw("SELECT count(*) FROM sqlite_master").Scan(&n).Error; err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("неверный ключ или база повреждена: %w", err)
	}
	if err := db.AutoMigrate(&Document{}, &Collection{}, &Settings{}, &SyncState{},
		&Blob{}, &Recent{}, &Favorite{}); err != nil {
		return nil, err
	}
	d := &DB{db: db}
	d.fts = d.initFTS()
	return d, nil
}

func (d *DB) initFTS() bool {
	if err := d.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts
		USING fts5(title, text, content='documents', content_rowid='id')`).Error; err != nil {
		log.Printf("[DB] fts5 unavailable, LIKE fallback: %v", err)
		d.dropFTSTriggers()
		return false
	}
	for _, q := range []string{
		`CREATE TRIGGER IF NOT EXISTS docs_ai AFTER INSERT ON documents BEGIN
			INSERT INTO docs_fts(rowid, title, text) VALUES (new.id, new.title, new.text);
		END`,
		`CREATE TRIGGER IF NOT EXISTS docs_ad AFTER DELETE ON documents BEGIN
			INSERT INTO docs_fts(docs_fts, rowid, title, text) VALUES('delete', old.id, old.title, old.text);
		END`,
		`CREATE TRIGGER IF NOT EXISTS docs_au AFTER UPDATE ON documents BEGIN
			INSERT INTO docs_fts(docs_fts, rowid, title, text) VALUES('delete', old.id, old.title, old.text);
			INSERT INTO docs_fts(rowid, title, text) VALUES (new.id, new.title, new.text);
		END`,
	} {
		if err := d.db.Exec(q).Error; err != nil {
			log.Printf("[DB] fts trigger: %v", err)
			d.dropFTSTriggers()
			return false
		}
	}
	var docs, indexed int64
	d.db.Raw(`SELECT count(*) FROM documents`).Scan(&docs)
	d.db.Raw(`SELECT count(*) FROM docs_fts`).Scan(&indexed)
	if docs != indexed {
		if err := d.db.Exec(`INSERT INTO docs_fts(docs_fts) VALUES('rebuild')`).Error; err != nil {
			log.Printf("[DB] fts rebuild: %v", err)
			return false
		}
	}
	log.Printf("[DB] fts5 ready, docs=%d", docs)
	return true
}

func (d *DB) dropFTSTriggers() {
	for _, name := range []string{"docs_ai", "docs_ad", "docs_au"} {
		if err := d.db.Exec("DROP TRIGGER IF EXISTS " + name).Error; err != nil {
			log.Printf("[DB] drop trigger %s: %v", name, err)
		}
	}
}

func IsPlaintextDB(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	hdr := make([]byte, 16)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return false
	}
	return string(hdr) == "SQLite format 3\x00"
}

func MigratePlaintext(dbPath, keyHex string) error {
	old, err := gorm.Open(gormlite.Open(dsn(dbPath, "")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return err
	}
	oldSQL, _ := old.DB()
	oldSQL.SetMaxOpenConns(1)
	_ = old.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error
	var docs []Document
	var cols []Collection
	var s Settings
	var ss SyncState
	old.Find(&docs)
	old.Find(&cols)
	hasSettings := old.First(&s, 1).Error == nil
	hasSync := old.First(&ss, 1).Error == nil
	_ = oldSQL.Close()

	suffixes := []string{"", "-wal", "-shm", "-journal"}
	for _, suf := range suffixes {
		if _, err := os.Stat(dbPath + suf); err == nil {
			if err := os.Rename(dbPath+suf, dbPath+".plain"+suf); err != nil {
				return err
			}
		}
	}
	rollback := func() {
		for _, suf := range suffixes {
			_ = os.Rename(dbPath+".plain"+suf, dbPath+suf)
		}
	}
	nd, err := NewDB(dbPath, keyHex)
	if err != nil {
		rollback()
		return err
	}
	if len(cols) > 0 {
		if err := nd.db.CreateInBatches(&cols, 200).Error; err != nil {
			nd.Close()
			rollback()
			return err
		}
	}
	if len(docs) > 0 {
		if err := nd.db.CreateInBatches(&docs, 200).Error; err != nil {
			nd.Close()
			rollback()
			return err
		}
	}
	if hasSettings {
		_ = nd.SaveSettings(&s)
	}
	if hasSync {
		_ = nd.db.Save(&ss).Error
	}
	_ = nd.Close()
	for _, suf := range suffixes {
		_ = os.Remove(dbPath + ".plain" + suf)
	}
	log.Printf("[DB] существующий кэш зашифрован, docs=%d", len(docs))
	return nil
}

func (d *DB) ReplaceAll(cols []Collection, docs []Document) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("1 = 1").Delete(&Document{}).Error; err != nil {
			return err
		}
		if err := tx.Where("1 = 1").Delete(&Collection{}).Error; err != nil {
			return err
		}
		if len(cols) > 0 {
			if err := tx.Create(&cols).Error; err != nil {
				return err
			}
		}
		if len(docs) > 0 {
			if err := tx.CreateInBatches(&docs, 200).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (d *DB) GetCollections() ([]Collection, error) {
	var out []Collection
	err := d.db.Order("position").Find(&out).Error
	return out, err
}

func (d *DB) GetDocuments() ([]Document, error) {
	var out []Document
	err := d.db.Order("position").Find(&out).Error
	return out, err
}

func (d *DB) GetDocumentsMeta() ([]Document, error) {
	var out []Document
	err := d.db.Select("id, outline_id, collection_id, parent_id, url_id, position, title, updated_at").
		Order("position").Find(&out).Error
	return out, err
}

func (d *DB) CountDocuments() int64 {
	var n int64
	d.db.Model(&Document{}).Count(&n)
	return n
}

func (d *DB) GetDocumentByOutlineID(id string) (*Document, error) {
	var doc Document
	if err := d.db.Where("outline_id = ?", id).First(&doc).Error; err != nil {
		return nil, err
	}
	return &doc, nil
}

func (d *DB) GetDocumentByURLID(urlID string) (*Document, error) {
	var doc Document
	if err := d.db.Where("outline_id = ?", urlID).First(&doc).Error; err == nil {
		return &doc, nil
	}
	if err := d.db.Where("url_id = ?", urlID).First(&doc).Error; err == nil {
		return &doc, nil
	}
	if err := d.db.Where("url = ?", "/doc/"+urlID).First(&doc).Error; err == nil {
		return &doc, nil
	}
	if err := d.db.Where("url LIKE ?", "%/doc/"+urlID).First(&doc).Error; err == nil {
		return &doc, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func ftsQuery(q string) string {
	words := strings.Fields(q)
	parts := make([]string, 0, len(words))
	for _, w := range words {
		parts = append(parts, "\""+strings.ReplaceAll(w, "\"", "\"\"")+"\"*")
	}
	return strings.Join(parts, " ")
}

func (d *DB) SearchDocuments(query string) ([]Document, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []Document{}, nil
	}
	if d.fts {
		var out []Document
		err := d.db.Table("documents").
			Select("documents.*").
			Joins("JOIN docs_fts ON docs_fts.rowid = documents.id").
			Where("docs_fts MATCH ?", ftsQuery(query)).
			Order("docs_fts.rank").
			Limit(50).
			Find(&out).Error
		if err == nil && len(out) > 0 {
			return out, nil
		}
		if err != nil {
			log.Printf("[DB] fts search: %v", err)
		}
	}
	var all []Document
	if err := d.db.Order("position").Find(&all).Error; err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	var out []Document
	for _, doc := range all {
		if strings.Contains(strings.ToLower(doc.Title), q) || strings.Contains(strings.ToLower(doc.Text), q) {
			out = append(out, doc)
			if len(out) >= 50 {
				break
			}
		}
	}
	return out, nil
}

func (d *DB) SaveSettings(s *Settings) error {
	s.ID = 1
	return d.db.Save(s).Error
}

func (d *DB) GetSettings() (*Settings, error) {
	var s Settings
	err := d.db.First(&s, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &Settings{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (d *DB) GetLastSyncTime() time.Time {
	var s SyncState
	if err := d.db.First(&s, 1).Error; err != nil {
		return time.Time{}
	}
	return s.LastSync
}

func (d *DB) SetLastSyncTime(t time.Time) error {
	return d.db.Save(&SyncState{ID: 1, LastSync: t}).Error
}

func (d *DB) GetSyncState() SyncState {
	var s SyncState
	_ = d.db.First(&s, 1).Error
	return s
}

func (d *DB) SetSyncState(t time.Time, added, updated, deleted int) error {
	return d.db.Save(&SyncState{ID: 1, LastSync: t, Added: added, Updated: updated, Deleted: deleted}).Error
}

func (d *DB) Close() error {
	sqlDB, err := d.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
