//go:build !windows

package main

import "context"

func (a *App) watchSession(ctx context.Context) {}

func (a *App) stopSessionWatcher() {}
