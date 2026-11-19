//go:build windows

package main

import (
	"log"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procFindWindowW              = user32.NewProc("FindWindowW")
	procSetWindowDisplayAffinity = user32.NewProc("SetWindowDisplayAffinity")
)

const wdaExcludeFromCapture = 0x11

func setCaptureProtection(title string, enable bool) bool {
	t, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return false
	}
	for i := 0; i < 50; i++ {
		h, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(t)))
		if h != 0 {
			aff := uintptr(0)
			if enable {
				aff = wdaExcludeFromCapture
			}
			r, _, _ := procSetWindowDisplayAffinity.Call(h, aff)
			log.Printf("[SCREEN] capture protection=%v ok=%v", enable, r != 0)
			return r != 0
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func (a *App) SetCaptureProtection(on bool) bool {
	return setCaptureProtection("Outline Reader", on)
}
