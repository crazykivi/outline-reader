package main

import (
	"fmt"
	"os"
)

const updateReadyEnv = "OUTLINE_READER_UPDATE_READY_FILE"

// markUpdateReady is called only after Wails has loaded the application DOM.
// The updater helper uses this marker to decide whether the new binary is usable.
func (a *App) markUpdateReady() error {
	if err := a.ensure(); err != nil {
		return fmt.Errorf("инициализация приложения: %w", err)
	}
	path := os.Getenv(updateReadyEnv)
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("создание маркера готовности: %w", err)
	}
	_, writeErr := fmt.Fprintf(f, "%d", os.Getpid())
	closeErr := f.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return closeErr
	}
	if err := os.Unsetenv(updateReadyEnv); err != nil {
		return err
	}
	return nil
}
