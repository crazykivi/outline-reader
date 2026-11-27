package storage

import "gorm.io/gorm/clause"

type Pref struct {
	Key   string `gorm:"primaryKey"`
	Value string
}

func (d *DB) GetPrefs() (map[string]string, error) {
	var rows []Pref
	if err := d.db.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}

func (d *DB) SetPrefs(prefs map[string]string) error {
	if len(prefs) == 0 {
		return nil
	}
	rows := make([]Pref, 0, len(prefs))
	for k, v := range prefs {
		rows = append(rows, Pref{Key: k, Value: v})
	}
	return d.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).CreateInBatches(rows, 100).Error
}
