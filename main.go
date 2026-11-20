package main

import (
	"context"
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/logger"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed frontend/*
var assets embed.FS

var version = "dev"

var webviewJunk = []string{
	"Cache", "Code Cache", "GPUCache", "DawnCache", "DawnGraphiteCache", "DawnWebGPUCache",
	"ShaderCache", "GrShaderCache", "Service Worker", "Session Storage", "Sessions",
	"IndexedDB", "databases", "History", "History-journal", "Top Sites", "Top Sites-journal",
	"Favicons", "Favicons-journal", "Visited Links", "Network Action Predictor",
	"Web Data", "Web Data-journal", "Network", "shared_proto_db", "VideoDecodeStats",
	"Reporting and NEL", "Trust Tokens", "Trust Tokens-journal",
}

func wipeWebviewProfile(root string) {
	base := filepath.Join(root, "EBWebView", "Default")
	for _, name := range webviewJunk {
		if err := os.RemoveAll(filepath.Join(base, name)); err != nil && !os.IsNotExist(err) {
			log.Printf("[WEBVIEW] cleanup %s: %v", name, err)
		}
	}
}

func main() {
	_ = os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--disk-cache-size=1 --media-cache-size=1")

	app := NewApp()
	app.version = version
	app.uiEvents = true
	if err := app.ensure(); err != nil {
		log.Printf("[APP] init: %v", err)
	}
	info, err := app.StorageInfo()
	if err != nil {
		log.Printf("[APP] storage info: %v", err)
	}

	uniqueID := "outline-reader-crazykivi-9f3a2b1c-d4e5f6"
	profileDir := filepath.Join(info.DataDir, "webview")
	if info.Portable {
		profileDir = filepath.Join(os.TempDir(), instanceID(info.DataDir))
		uniqueID = instanceID(info.DataDir)
		log.Printf("[APP] portable mode, data=%s", info.DataDir)
	}
	wipeWebviewProfile(profileDir)

	err = wails.Run(&options.App{
		Title:            "Outline Reader",
		Frameless:        true,
		Width:            1200,
		Height:           800,
		MinWidth:         900,
		MinHeight:        600,
		BackgroundColour: &options.RGBA{R: 15, G: 18, B: 22, A: 255},
		AssetServer: &assetserver.Options{
			Assets:     assets,
			Middleware: app.imageMiddleware,
		},
		OnStartup: app.startup,
		OnShutdown: func(ctx context.Context) {
			app.shutdown()
			wipeWebviewProfile(profileDir)
		},
		Bind:   []interface{}{app},
		Logger: logger.NewDefaultLogger(),
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			WebviewUserDataPath:  profileDir,
			OnSuspend: func() {
				_ = app.LockApp()
				log.Printf("[APP] система уходит в сон, база заблокирована")
			},
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: uniqueID,
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
				log.Println("[APP] second instance detected, focusing existing window")
				if ctx := app.Context(); ctx != nil {
					wailsruntime.WindowUnminimise(ctx)
					wailsruntime.WindowShow(ctx)
				}
			},
		},
	})
	if err != nil {
		log.Fatal("Error:", err.Error())
	}
}
