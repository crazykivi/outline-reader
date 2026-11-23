//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const updateDetachFlags = 0x00000008 | 0x00000200 // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP

func (a *App) restartAfterUpdate(exePath, oldPath string) error {
	pid := os.Getpid()
	script := fmt.Sprintf(`@echo off
set /a tries=0
:wait
tasklist /FI "PID eq %d" 2>nul | find " %d " >nul
if errorlevel 1 goto done
set /a tries+=1
if %%tries%% GEQ 60 goto done
ping -n 2 127.0.0.1 >nul
goto wait
:done
del /F /Q "%s" >nul 2>nul
start "" "%s"
del /F /Q "%%~f0" >nul 2>nul
`, pid, pid, oldPath, exePath)

	bat := filepath.Join(os.TempDir(), fmt.Sprintf("outline-reader-update-%d.bat", pid))
	if err := os.WriteFile(bat, []byte(strings.ReplaceAll(script, "\n", "\r\n")), 0o600); err != nil {
		return err
	}
	cmd := exec.Command("cmd", "/C", bat)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: updateDetachFlags}
	if err := cmd.Start(); err != nil {
		return err
	}
	log.Printf("[UPDATE] helper запущен, ожидание выхода pid=%d", pid)
	return nil
}
