//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

const updatePowerShellScript = `param(
    [Parameter(Mandatory=$true)][string]$ExePath,
    [Parameter(Mandatory=$true)][string]$TempPath,
    [Parameter(Mandatory=$true)][string]$OldPath,
    [Parameter(Mandatory=$true)][string]$ReadyPath,
    [Parameter(Mandatory=$true)][int]$OldPID
)

$ErrorActionPreference = "Stop"
$newProcess = $null
$backupCreated = $false

try {
    Wait-Process -Id $OldPID -Timeout 60 -ErrorAction SilentlyContinue
    if (Get-Process -Id $OldPID -ErrorAction SilentlyContinue) {
        throw "The previous application process did not exit in time."
    }

    if (Test-Path -LiteralPath $OldPath) {
        throw "A previous backup already exists; refusing to overwrite it."
    }

    Move-Item -LiteralPath $ExePath -Destination $OldPath
    $backupCreated = $true

    try {
        Move-Item -LiteralPath $TempPath -Destination $ExePath
    }
    catch {
        Move-Item -LiteralPath $OldPath -Destination $ExePath
        $backupCreated = $false
        throw
    }

    $env:OUTLINE_READER_UPDATE_READY_FILE = $ReadyPath
    $newProcess = Start-Process -FilePath $ExePath -WorkingDirectory ([IO.Path]::GetDirectoryName($ExePath)) -PassThru
    $deadline = [DateTime]::UtcNow.AddSeconds(45)

    while ([DateTime]::UtcNow -lt $deadline) {
        if (Test-Path -LiteralPath $ReadyPath) {
            $newProcess.Refresh()
            if (-not $newProcess.HasExited) {
                Remove-Item -LiteralPath $OldPath -Force
                $backupCreated = $false
                Remove-Item -LiteralPath $ReadyPath -Force -ErrorAction SilentlyContinue
                exit 0
            }
            throw "The updated application exited immediately after reporting readiness."
        }

        $newProcess.Refresh()
        if ($newProcess.HasExited) {
            throw "The updated application exited before becoming ready."
        }
        Start-Sleep -Milliseconds 500
    }

    throw "The updated application did not become ready before the timeout."
}
catch {
    Write-Error $_

    if ($newProcess -ne $null) {
        try {
            $newProcess.Refresh()
            if (-not $newProcess.HasExited) {
                Stop-Process -Id $newProcess.Id -Force -ErrorAction SilentlyContinue
            }
        } catch {}
    }

    if ($backupCreated -and (Test-Path -LiteralPath $OldPath)) {
        Remove-Item -LiteralPath $ExePath -Force -ErrorAction SilentlyContinue
        Move-Item -LiteralPath $OldPath -Destination $ExePath -Force
        Start-Process -FilePath $ExePath -WorkingDirectory ([IO.Path]::GetDirectoryName($ExePath)) | Out-Null
    }
    Remove-Item -LiteralPath $ReadyPath -Force -ErrorAction SilentlyContinue
    exit 1
}
finally {
    Remove-Item -LiteralPath $PSCommandPath -Force -ErrorAction SilentlyContinue
}
`

const updateCreateNoWindow = 0x08000000 // CREATE_NO_WINDOW

func (a *App) restartAfterUpdate(exePath, tempPath, oldPath, readyPath string) error {
	pid := os.Getpid()
	script, err := os.CreateTemp(os.TempDir(), "outline-reader-update-*.ps1")
	if err != nil {
		return err
	}
	scriptPath := script.Name()
	if _, err := script.WriteString(updatePowerShellScript); err != nil {
		_ = script.Close()
		_ = os.Remove(scriptPath)
		return err
	}
	if err := script.Close(); err != nil {
		_ = os.Remove(scriptPath)
		return err
	}

	cmd := exec.Command(
		"powershell.exe",
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-WindowStyle", "Hidden",
		"-ExecutionPolicy", "Bypass",
		"-File", scriptPath,
		exePath,
		tempPath,
		oldPath,
		readyPath,
		strconv.Itoa(pid),
	)
	cmd.Dir = filepath.Dir(exePath)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: updateCreateNoWindow}
	if err := cmd.Start(); err != nil {
		_ = os.Remove(scriptPath)
		return err
	}
	if err := cmd.Process.Release(); err != nil {
		log.Printf("[UPDATE] не удалось освободить процесс помощника: %v", err)
	}
	log.Printf("[UPDATE] PowerShell helper запущен, pid=%d", pid)
	return nil
}
