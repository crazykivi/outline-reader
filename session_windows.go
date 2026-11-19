//go:build windows

package main

import (
	"context"
	"log"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                             = windows.NewLazySystemDLL("kernel32.dll")
	wtsapi32                             = windows.NewLazySystemDLL("wtsapi32.dll")
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
	procWTSRegisterSessionNotification   = wtsapi32.NewProc("WTSRegisterSessionNotification")
	procWTSUnRegisterSessionNotification = wtsapi32.NewProc("WTSUnRegisterSessionNotification")
)

const (
	wmWtsSessionChange = 0x02B1
	wtsSessionLock     = 0x7
	wmClose            = 0x0010
	wmDestroy          = 0x0002
	hwndMessage        = ^uintptr(2)
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

func (a *App) sessionWndProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case wmWtsSessionChange:
		if wp == wtsSessionLock {
			log.Printf("[SESSION] workstation locked")
			_ = a.LockApp()
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wp, lp)
	return r
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
		WndProc:   windows.NewCallback(a.sessionWndProc),
		Instance:  windows.Handle(hInst),
		ClassName: className,
	}
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
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
	if r, _, err := procWTSRegisterSessionNotification.Call(hwnd, 0); r == 0 {
		log.Printf("[SESSION] WTSRegisterSessionNotification: %v", err)
		procDestroyWindow.Call(hwnd)
		return
	}
	log.Printf("[SESSION] session lock watcher started")
	go func() {
		<-ctx.Done()
		procPostMessageW.Call(hwnd, wmClose, 0, 0)
	}()
	var m sessionMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	procWTSUnRegisterSessionNotification.Call(hwnd)
}
