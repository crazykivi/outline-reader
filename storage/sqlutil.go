package storage

import (
	"slices"
	"strings"

	"gorm.io/gorm"
)

const sqlInChunk = 500

func withTempIDs(tx *gorm.DB, name string, ids []string, fn func(tbl string) error) error {
	tbl := "temp." + name
	if err := tx.Exec("CREATE TEMP TABLE IF NOT EXISTS " + tbl + " (id TEXT PRIMARY KEY)").Error; err != nil {
		return err
	}
	defer func() { _ = tx.Exec("DROP TABLE IF EXISTS " + tbl).Error }()
	if err := tx.Exec("DELETE FROM " + tbl).Error; err != nil {
		return err
	}
	for batch := range slices.Chunk(ids, sqlInChunk) {
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		ph := strings.TrimSuffix(strings.Repeat("(?),", len(batch)), ",")
		if err := tx.Exec("INSERT OR IGNORE INTO "+tbl+" (id) VALUES "+ph, args...).Error; err != nil {
			return err
		}
	}
	return fn(tbl)
}
