package storage

import (
	"slices"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (d *DB) ApplySync(cols []Collection, docs, meta []Document, deleteIDs []string) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		if len(cols) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "outline_id"}},
				DoUpdates: clause.AssignmentColumns([]string{"name", "position"}),
			}).CreateInBatches(cols, 200).Error; err != nil {
				return err
			}
		}
		colIDs := make([]string, 0, len(cols))
		for _, c := range cols {
			colIDs = append(colIDs, c.OutlineID)
		}
		if len(colIDs) == 0 {
			if err := tx.Where("1 = 1").Delete(&Collection{}).Error; err != nil {
				return err
			}
		} else if err := withTempIDs(tx, "keep_collections", colIDs, func(tbl string) error {
			return tx.Exec("DELETE FROM collections WHERE outline_id NOT IN (SELECT id FROM " + tbl + ")").Error
		}); err != nil {
			return err
		}
		if len(docs) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "outline_id"}},
				DoUpdates: clause.AssignmentColumns([]string{"collection_id", "parent_id", "url_id", "url", "title", "text", "position", "updated_at"}),
			}).CreateInBatches(docs, 200).Error; err != nil {
				return err
			}
		}
		if len(meta) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "outline_id"}},
				DoUpdates: clause.AssignmentColumns([]string{"collection_id", "parent_id", "position"}),
			}).CreateInBatches(meta, 200).Error; err != nil {
				return err
			}
		}
		for batch := range slices.Chunk(deleteIDs, sqlInChunk) {
			if err := tx.Where("outline_id IN ?", batch).Delete(&Document{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (d *DB) GetLastFullSync() time.Time {
	var s SyncState
	if err := d.db.First(&s, 1).Error; err != nil {
		return time.Time{}
	}
	return s.LastFull
}

func (d *DB) SetLastFullSync(t time.Time) error {
	return d.updateSyncState(map[string]any{"last_full": t}, SyncState{LastFull: t})
}
