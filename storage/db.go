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
	LastFull time.Time
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
	_ = db.Exec("PRAGMA busy_timeout = 5000").Error
	var n int64
	if err := db.Raw("SELECT count(*) FROM sqlite_master").Scan(&n).Error; err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("неверный ключ или база повреждена: %w", err)
	}
	if err := db.AutoMigrate(&Document{}, &Collection{}, &Settings{}, &SyncState{},
		&Blob{}, &Recent{}, &Favorite{}, &Pref{}); err != nil {
		_ = sqlDB.Close()
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
	d.dropFTSTriggers()
	for _, q := range []string{
		`CREATE TRIGGER docs_ai AFTER INSERT ON documents BEGIN
			INSERT INTO docs_fts(rowid, title, text) VALUES (new.id, new.title, new.text);
		END`,
		`CREATE TRIGGER docs_ad AFTER DELETE ON documents BEGIN
			INSERT INTO docs_fts(docs_fts, rowid, title, text) VALUES('delete', old.id, old.title, old.text);
		END`,
		`CREATE TRIGGER docs_au AFTER UPDATE OF title, text ON documents BEGIN
			INSERT INTO docs_fts(docs_fts, rowid, title, text) VALUES('delete', old.id, old.title, old.text);
			INSERT INTO docs_fts(rowid, title, text) VALUES(new.id, new.title, new.text);
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
	defer func() { _ = f.Close() }()
	hdr := make([]byte, 16)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return false
	}
	return string(hdr) == "SQLite format 3\x00"
}

func MigratePlaintext(dbPath, keyHex string) error {
	if !IsPlaintextDB(dbPath) {
		return fmt.Errorf("исходная база %s не является plaintext SQLite", dbPath)
	}

	old, err := gorm.Open(gormlite.Open(dsn(dbPath, "")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return err
	}
	oldSQL, err := old.DB()
	if err != nil {
		return fmt.Errorf("получение соединения исходной базы: %w", err)
	}
	oldSQL.SetMaxOpenConns(1)

	closeOld := func(cause error) error {
		if closeErr := oldSQL.Close(); closeErr != nil {
			return errors.Join(cause, fmt.Errorf("закрытие исходной базы: %w", closeErr))
		}
		return cause
	}
	if err := old.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
		return closeOld(fmt.Errorf("checkpoint исходной базы: %w", err))
	}

	var docs []Document
	var cols []Collection
	var s Settings
	var ss SyncState
	if err := old.Find(&docs).Error; err != nil {
		return closeOld(fmt.Errorf("чтение документов исходной базы: %w", err))
	}
	if err := old.Find(&cols).Error; err != nil {
		return closeOld(fmt.Errorf("чтение коллекций исходной базы: %w", err))
	}

	hasSettings := false
	if err := old.First(&s, 1).Error; err == nil {
		hasSettings = true
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return closeOld(fmt.Errorf("чтение настроек исходной базы: %w", err))
	}
	hasSync := false
	if err := old.First(&ss, 1).Error; err == nil {
		hasSync = true
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return closeOld(fmt.Errorf("чтение состояния синхронизации исходной базы: %w", err))
	}
	if err := oldSQL.Close(); err != nil {
		return fmt.Errorf("закрытие исходной базы: %w", err)
	}

	suffixes := []string{"", "-wal", "-shm", "-journal"}
	moved := make([]string, 0, len(suffixes))
	for _, suf := range suffixes {
		_, statErr := os.Stat(dbPath + suf)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return restorePlaintextFiles(dbPath, moved, false, fmt.Errorf("проверка файла %s: %w", dbPath+suf, statErr))
		}
		if err := os.Rename(dbPath+suf, dbPath+".plain"+suf); err != nil {
			return restorePlaintextFiles(dbPath, moved, fmt.Errorf("сохранение исходного файла %s: %w", dbPath+suf, err))
		}
		moved = append(moved, suf)
	}

	rollback := func(cause error, nd *DB) error {
		if nd != nil {
			if closeErr := nd.Close(); closeErr != nil {
				cause = errors.Join(cause, fmt.Errorf("закрытие новой базы: %w", closeErr))
			}
		}
		return restorePlaintextFiles(dbPath, moved, true, cause)
	}

	nd, err := NewDB(dbPath, keyHex)
	if err != nil {
		return rollback(fmt.Errorf("создание зашифрованной базы: %w", err), nil)
	}
	if len(cols) > 0 {
		if err := nd.db.CreateInBatches(&cols, 200).Error; err != nil {
			return rollback(fmt.Errorf("перенос коллекций: %w", err), nd)
		}
	}
	if len(docs) > 0 {
		if err := nd.db.CreateInBatches(&docs, 200).Error; err != nil {
			return rollback(fmt.Errorf("перенос документов: %w", err), nd)
		}
	}
	if hasSettings {
		if err := nd.SaveSettings(&s); err != nil {
			return rollback(fmt.Errorf("перенос настроек: %w", err), nd)
		}
	}
	if hasSync {
		if err := nd.db.Save(&ss).Error; err != nil {
			return rollback(fmt.Errorf("перенос состояния синхронизации: %w", err), nd)
		}
	}
	if err := nd.Close(); err != nil {
		return rollback(fmt.Errorf("закрытие зашифрованной базы: %w", err), nil)
	}

	for _, suf := range moved {
		if err := os.Remove(dbPath + ".plain" + suf); err != nil && !os.IsNotExist(err) {
			log.Printf("[DB] не удалось удалить резервный файл %s: %v", dbPath+".plain"+suf, err)
		}
	}
	log.Printf("[DB] существующий кэш зашифрован, docs=%d", len(docs))
	return nil
}

func restorePlaintextFiles(dbPath string, moved []string, removeUnmoved bool, cause error) error {
	remove := moved
	if removeUnmoved {
		remove = []string{"", "-wal", "-shm", "-journal"}
	}
	for _, suf := range remove {
		if err := os.Remove(dbPath + suf); err != nil && !os.IsNotExist(err) {
			cause = errors.Join(cause, fmt.Errorf("удаление незавершённого файла %s: %w", dbPath+suf, err))
		}
	}
	for _, suf := range moved {
		if err := os.Rename(dbPath+".plain"+suf, dbPath+suf); err != nil {
			cause = errors.Join(cause, fmt.Errorf("восстановление исходного файла %s: %w", dbPath+suf, err))
		}
	}
	return cause
}

func (d *DB) GetCollections() ([]Collection, error) {
	var out []Collection
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
	if !d.fts {
		return d.searchLike(query)
	}
	var out []Document
	err := d.db.Table("documents").
		Select("documents.*").
		Joins("JOIN docs_fts ON docs_fts.rowid = documents.id").
		Where("docs_fts MATCH ?", ftsQuery(query)).
		Order("docs_fts.rank").
		Limit(50).
		Find(&out).Error
	if err != nil {
		log.Printf("[DB] fts search: %v", err)
		return d.searchLike(query)
	}
	return out, nil
}

func likePattern(q string) string {
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + esc.Replace(strings.ToLower(q)) + "%"
}

func (d *DB) searchLike(query string) ([]Document, error) {
	p := likePattern(query)
	var out []Document
	err := d.db.Where(`lower(title) LIKE ? ESCAPE '\' OR lower(text) LIKE ? ESCAPE '\'`, p, p).
		Order("position").Limit(50).Find(&out).Error
	return out, err
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

func (d *DB) GetSyncState() SyncState {
	var s SyncState
	_ = d.db.First(&s, 1).Error
	return s
}

func (d *DB) SetSyncState(t time.Time, added, updated, deleted int) error {
	return d.updateSyncState(map[string]any{
		"last_sync": t, "added": added, "updated": updated, "deleted": deleted,
	}, SyncState{LastSync: t, Added: added, Updated: updated, Deleted: deleted})
}

func (d *DB) updateSyncState(cols map[string]any, seed SyncState) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&SyncState{}).Where("id = ?", 1).Updates(cols)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			return nil
		}
		seed.ID = 1
		return tx.Create(&seed).Error
	})
}

func (d *DB) Close() error {
	sqlDB, err := d.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
