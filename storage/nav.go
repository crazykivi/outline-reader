package storage

import (
	"time"

	"gorm.io/gorm/clause"
)

type Recent struct {
	OutlineID string `gorm:"primaryKey"`
	OpenedAt  time.Time
}

type Favorite struct {
	OutlineID string `gorm:"primaryKey"`
	AddedAt   time.Time
}

func (d *DB) TouchRecent(oid string) error {
	if err := d.db.Clauses(clause.OnConflict{
		DoUpdates: clause.AssignmentColumns([]string{"opened_at"}),
	}).Create(&Recent{OutlineID: oid, OpenedAt: time.Now()}).Error; err != nil {
		return err
	}
	return d.db.Exec(`DELETE FROM recents WHERE outline_id NOT IN
		(SELECT outline_id FROM recents ORDER BY opened_at DESC LIMIT 20)`).Error
}

func (d *DB) GetRecentDocs() ([]Document, error) {
	var out []Document
	err := d.db.Table("documents").Select("documents.*").
		Joins("JOIN recents ON recents.outline_id = documents.outline_id").
		Order("recents.opened_at DESC").Limit(20).Find(&out).Error
	return out, err
}

func (d *DB) ToggleFavorite(oid string) (bool, error) {
	var n int64
	d.db.Model(&Favorite{}).Where("outline_id = ?", oid).Count(&n)
	if n > 0 {
		return false, d.db.Where("outline_id = ?", oid).Delete(&Favorite{}).Error
	}
	return true, d.db.Create(&Favorite{OutlineID: oid, AddedAt: time.Now()}).Error
}

func (d *DB) IsFavorite(oid string) bool {
	var n int64
	d.db.Model(&Favorite{}).Where("outline_id = ?", oid).Count(&n)
	return n > 0
}

func (d *DB) GetFavoriteDocs() ([]Document, error) {
	var out []Document
	err := d.db.Table("documents").Select("documents.*").
		Joins("JOIN favorites ON favorites.outline_id = documents.outline_id").
		Order("favorites.added_at").Find(&out).Error
	return out, err
}
