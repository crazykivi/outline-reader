//go:build !windows

package main

import "errors"

func (a *App) restartAfterUpdate(exePath, oldPath string) error {
	return errors.New("автообновление доступно только на Windows")
}
