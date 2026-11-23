//go:build windows

package main

import (
	"context"
	"log"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var (
	kernel32                             = windows.NewLazySystemDLL("kernel32.dll")
	wtsapi32                             = windows.NewLazySystemDLL("wtsapi32.dll")
	shell32                              = windows.NewLazySystemDLL("shell32.dll")
	procGetModuleHandleW                 = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW                 = user32.NewProc("RegisterClassExW")
	procCreateWindowExW                  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                   = user32.NewProc("DefWindowProcW")
	procGetMessageW                      = user32.NewProc("GetMessageW")
	procTranslateMessage                 = user32.NewProc("TranslateMessage")
	procDispatchMessageW                 = user32.NewProc("DispatchMessageW")
	procPostMessageW                     = user32.NewProc("PostMessageW")
	procDestroyWindow                    = user32.NewProc("DestroyWindow")
	procPostQuitMessage                  = user32.NewProc("PostQuitMessage")
	procLoadImageW                        = user32.NewProc("LoadImageW")
	procLoadIconW                         = user32.NewProc("LoadIconW")
	procRegisterWindowMessageW            = user32.NewProc("RegisterWindowMessageW")
	procCreatePopupMenu                   = user32.NewProc("CreatePopupMenu")
	procAppendMenuW                       = user32.NewProc("AppendMenuW")
	procGetCursorPos                      = user32.NewProc("GetCursorPos")
	procSetForegroundWindow               = user32.NewProc("SetForegroundWindow")
	procTrackPopupMenu                    = user32.NewProc("TrackPopupMenu")
	procDestroyMenu                       = user32.NewProc("DestroyMenu")
	procShellNotifyIconW                  = shell32.NewProc("Shell_NotifyIconW")
	procWTSRegisterSessionNotification   = wtsapi32.NewProc("WTSRegisterSessionNotification")
	procWTSUnRegisterSessionNotification = wtsapi32.NewProc("WTSUnRegisterSessionNotification")
)

const (
	wmWtsSessionChange = 0x02B1
	wtsSessionLock     = 0x7
	wmClose            = 0x0010
	wmDestroy          = 0x0002
	wmApp              = 0x8000
	wmTrayCallback     = wmApp + 1
	wmRButtonUp        = 0x0205
	wmLButtonDblClick  = 0x0203
	hwndMessage        = ^uintptr(2)

	nimAdd    = 0x00000000
	nimDelete = 0x00000002
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	imageIcon     = 1
	lrDefaultSize = 0x00000040
	lrShared      = 0x00008000
	idiApplication = 32512

	mfString     = 0x00000000
	mfSeparator  = 0x00000800
	tpmRightButton = 0x0002
	tpmRetCommand  = 0x0100

	trayMenuOpen = 1001
	trayMenuExit = 1002
)

type sessionWndClass struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type sessionMsg struct {
	Hwnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

type notifyIconData struct {
	Size             uint32
	Hwnd             windows.HWND
	ID               uint32
	Flags            uint32
	CallbackMessage  uint32
	Icon             windows.Handle
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	TimeoutOrVersion uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GUID             windows.GUID
	BalloonIcon      windows.Handle
}

type trayPoint struct {
	X int32
	Y int32
}

var (
	trayWatcherMu   sync.Mutex
	trayWatcherStop func()
	trayWatcherDone chan struct{}
	trayTaskbarMsg  uint32
	trayIcon        *notifyIconData
)

func addTrayIcon(hwnd uintptr, instance uintptr) bool {
	// Load the application's embedded icon resource. Loading IDI_APPLICATION
	// here would silently show the generic Windows icon instead of our branding.
	icon, _, err := procLoadIconW.Call(instance, 1)
	if icon == 0 {
		log.Printf("[TRAY] LoadIcon(app resource ID 1): %v", err)
		icon, _, err = procLoadImageW.Call(instance, 1, imageIcon, 0, 0, lrDefaultSize|lrShared)
	}
	if icon == 0 {
		log.Printf("[TRAY] application icon resource is unavailable: %v", err)
		return false
	}

	tip, _ := windows.UTF16FromString("Outline Reader")
	data := &notifyIconData{
		Hwnd:            windows.HWND(hwnd),
		ID:              1,
		Flags:           nifMessage | nifIcon | nifTip,
		CallbackMessage: wmTrayCallback,
		Icon:            windows.Handle(icon),
	}
	copy(data.Tip[:], tip)
	data.Size = uint32(unsafe.Sizeof(*data))

	result, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(data)))
	if result == 0 {
		log.Printf("[TRAY] Shell_NotifyIcon(NIM_ADD): %v", err)
		return false
	}
	trayIcon = data
	log.Printf("[TRAY] system tray icon registered")
	return true
}

func removeTrayIcon() {
	if trayIcon == nil {
		return
	}
	result, _, err := procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(trayIcon)))
	if result == 0 {
		log.Printf("[TRAY] Shell_NotifyIcon(NIM_DELETE): %v", err)
	}
	trayIcon = nil
}

func showMainWindow(a *App) {
	ctx := a.appContext()
	if ctx == nil {
		log.Printf("[TRAY] cannot show window: application context is not ready")
		return
	}
	wailsruntime.WindowUnminimise(ctx)
	wailsruntime.WindowShow(ctx)
}

func showTrayMenu(a *App, hwnd uintptr) {
	menu, _, err := procCreatePopupMenu.Call()
	if menu == 0 {
		log.Printf("[TRAY] CreatePopupMenu: %v", err)
		return
	}
	defer func() {
		if result, _, err := procDestroyMenu.Call(menu); result == 0 {
			log.Printf("[TRAY] DestroyMenu: %v", err)
		}
	}()

	openText, _ := windows.UTF16PtrFromString("Открыть")
	exitText, _ := windows.UTF16PtrFromString("Выйти")
	if result, _, err := procAppendMenuW.Call(menu, mfString, trayMenuOpen, uintptr(unsafe.Pointer(openText))); result == 0 {
		log.Printf("[TRAY] append Open menu item: %v", err)
		return
	}
	if result, _, err := procAppendMenuW.Call(menu, mfSeparator, 0, 0); result == 0 {
		log.Printf("[TRAY] append menu separator: %v", err)
		return
	}
	if result, _, err := procAppendMenuW.Call(menu, mfString, trayMenuExit, uintptr(unsafe.Pointer(exitText))); result == 0 {
		log.Printf("[TRAY] append Exit menu item: %v", err)
		return
	}

	var point trayPoint
	if result, _, err := procGetCursorPos.Call(uintptr(unsafe.Pointer(&point))); result == 0 {
		log.Printf("[TRAY] GetCursorPos: %v", err)
		return
	}
	_, _, _ = procSetForegroundWindow.Call(hwnd)
	command, _, _ := procTrackPopupMenu.Call(
		menu,
		tpmRightButton|tpmRetCommand,
		uintptr(point.X),
		uintptr(point.Y),
		0,
		hwnd,
		0,
	)

	switch command {
	case trayMenuOpen:
		showMainWindow(a)
	case trayMenuExit:
		if ctx := a.appContext(); ctx != nil {
			wailsruntime.Quit(ctx)
		}
	}
}

func (a *App) sessionWndProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case wmWtsSessionChange:
		if wp == wtsSessionLock {
			log.Printf("[SESSION] workstation locked")
			_ = a.LockApp()
		}
		return 0
	case wmTrayCallback:
		switch uint32(lp) {
		case wmRButtonUp:
			showTrayMenu(a, hwnd)
		case wmLButtonDblClick:
			showMainWindow(a)
		}
		return 0
	case wmClose:
		_, _, _ = procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		removeTrayIcon()
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	}
	if msg == trayTaskbarMsg && trayTaskbarMsg != 0 {
		// Explorer can restart independently of the application. Re-register
		// the notification icon when the taskbar is recreated.
		if trayIcon != nil {
			if result, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(trayIcon))); result == 0 {
				log.Printf("[TRAY] restore icon after Explorer restart: %v", err)
			}
		}
		return 0
	}
	result, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wp, lp)
	return result
}

func (a *App) stopSessionWatcher() {
	trayWatcherMu.Lock()
	stop, done := trayWatcherStop, trayWatcherDone
	trayWatcherMu.Unlock()

	if stop != nil {
		stop()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			log.Printf("[TRAY] timed out waiting for tray watcher shutdown")
		}
	}
}

func (a *App) watchSession(ctx context.Context) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	className, err := windows.UTF16PtrFromString("OutlineReaderSessionWatcher")
	if err != nil {
		return
	}
	hInst, _, _ := procGetModuleHandleW.Call(0)
	wc := sessionWndClass{
		Size:      uint32(unsafe.Sizeof(sessionWndClass{})),
		Style:     0x0008, // CS_DBLCLKS: receive WM_LBUTTONDBLCLK for the tray icon.
		WndProc:   windows.NewCallback(a.sessionWndProc),
		Instance:  windows.Handle(hInst),
		ClassName: className,
	}
	if result, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); result == 0 {
		log.Printf("[SESSION] RegisterClassEx: %v", err)
		return
	}
	hwnd, _, err := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), 0, 0,
		0, 0, 0, 0, hwndMessage, 0, hInst, 0,
	)
	if hwnd == 0 {
		log.Printf("[SESSION] CreateWindowEx: %v", err)
		return
	}

	wtsRegistered := false
	if result, _, err := procWTSRegisterSessionNotification.Call(hwnd, 0); result == 0 {
		log.Printf("[SESSION] WTSRegisterSessionNotification: %v", err)
	} else {
		wtsRegistered = true
	}

	taskbarCreated, _ := windows.UTF16PtrFromString("TaskbarCreated")
	taskbarMessage, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(taskbarCreated)))
	trayTaskbarMsg = uint32(taskbarMessage)
	if !addTrayIcon(hwnd, hInst) {
		log.Printf("[TRAY] tray icon could not be registered; the window can still be restored by launching the EXE again")
	}

	done := make(chan struct{})
	var closeOnce sync.Once
	stop := func() {
		closeOnce.Do(func() {
			_, _, _ = procPostMessageW.Call(hwnd, wmClose, 0, 0)
		})
	}
	trayWatcherMu.Lock()
	trayWatcherStop = stop
	trayWatcherDone = done
	trayWatcherMu.Unlock()

	defer func() {
		if wtsRegistered {
			_, _, _ = procWTSUnRegisterSessionNotification.Call(hwnd)
		}
		close(done)
		trayWatcherMu.Lock()
		if trayWatcherDone == done {
			trayWatcherStop = nil
			trayWatcherDone = nil
		}
		trayWatcherMu.Unlock()
	}()

	log.Printf("[SESSION] session lock watcher started")
	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-done:
		}
	}()

	var message sessionMsg
	for {
		result, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) <= 0 {
			break
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
}

