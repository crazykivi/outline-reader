package main

import "fmt"

const maxPrefValueLen = 64 << 10

var prefKeys = map[string]bool{
	"theme":          true,
	"collapsedCollections": true,
	"favoritesCollapsed": true,
	"openTabs": true,
	"activeTabOID": true,
	"fullWidth":      true,
	"fontScale":      true,
	"sideWidth":      true,
	"expanded":       true,
	"tocOpen":        true,
	"captureProtect": true,
}

func (a *App) GetPrefs() (map[string]string, error) {
	sess, err := a.session()
	if err != nil {
		return nil, err
	}
	defer sess.done()
	return sess.db.GetPrefs()
}

func (a *App) SetPrefs(prefs map[string]string) error {
	sess, err := a.session()
	if err != nil {
		return err
	}
	defer sess.done()
	clean := make(map[string]string, len(prefs))
	for k, v := range prefs {
		if !prefKeys[k] {
			continue
		}
		if len(v) > maxPrefValueLen {
			return fmt.Errorf("настройка %s слишком большая", k)
		}
		clean[k] = v
	}
	if len(clean) == 0 {
		return nil
	}
	return sess.db.SetPrefs(clean)
}
