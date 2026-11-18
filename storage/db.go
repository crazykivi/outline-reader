package storage

import (
	"errors"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type DB struct{ db *gorm.DB }

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
}

func NewDB(dbPath string) (*DB, error) {
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&Document{}, &Collection{}, &Settings{}, &SyncState{}); err != nil {
		return nil, err
	}
	return &DB{db: db}, nil
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

func (d *DB) GetDocumentByOutlineID(id string) (*Document, error) {
	var doc Document
	if err := d.db.Where("outline_id = ?", id).First(&doc).Error; err != nil {
		return nil, err
	}
	return &doc, nil
}

func (d *DB) GetDocumentByURLID(urlID string) (*Document, error) {
	var doc Document
	if err := d.db.Where("url_id = ?", urlID).First(&doc).Error; err != nil {
		return nil, err
	}
	return &doc, nil
}

func (d *DB) SearchDocuments(query string) ([]Document, error) {
	var out []Document
	like := "%" + query + "%"
	err := d.db.Where("title LIKE ? OR text LIKE ?", like, like).Limit(50).Find(&out).Error
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

func (d *DB) SetLastSyncTime(t time.Time) error {
	return d.db.Save(&SyncState{ID: 1, LastSync: t}).Error
}

func (d *DB) Close() error {
	sqlDB, err := d.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
