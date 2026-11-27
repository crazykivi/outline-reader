package storage

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Blob struct {
	ID        string `gorm:"primaryKey"`
	Mime      string
	Data      []byte
	Size      int64
	FetchedAt time.Time
}

func (d *DB) GetBlob(id string) (*Blob, error) {
	var b Blob
	if err := d.db.First(&b, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &b, nil
}

func (d *DB) SaveBlob(b *Blob) error {
	return d.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(b).Error
}

func (d *DB) BlobIDs() ([]string, error) {
	var ids []string
	err := d.db.Model(&Blob{}).Pluck("id", &ids).Error
	return ids, err
}

func (d *DB) DeleteBlobsNotIn(ids []string) error {
	if len(ids) == 0 {
		return d.db.Where("1 = 1").Delete(&Blob{}).Error
	}
	return d.db.Transaction(func(tx *gorm.DB) error {
		return withTempIDs(tx, "keep_blobs", ids, func(tbl string) error {
			return tx.Exec("DELETE FROM blobs WHERE id NOT IN (SELECT id FROM " + tbl + ")").Error
		})
	})
}
