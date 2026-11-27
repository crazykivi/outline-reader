//go:build !windows

package main

import "errors"

func (a *App) restartAfterUpdate(exePath, tempPath, oldPath, readyPath string) error {
	return errors.New("автообновление доступно только на Windows")
}
