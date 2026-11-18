package main

import (
	"embed"
	"log"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/logger"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed frontend/*
var assets embed.FS

var version = "dev"

func main() {
	app := NewApp()
	app.version = version
	if err := app.ensure(); err != nil {
		log.Printf("[APP] init: %v", err)
	}

	info, err := app.StorageInfo()
	if err != nil {
		log.Printf("[APP] storage info: %v", err)
	}

	winOpts := &windows.Options{
		WebviewIsTransparent: false,
		WindowIsTranslucent:  false,
	}
	uniqueID := "outline-reader-crazykivi-9f3a2b1c-d4e5f6"
	if info.Portable {
		winOpts.WebviewUserDataPath = filepath.Join(info.DataDir, "webview")
		uniqueID = instanceID(info.DataDir)
		log.Printf("[APP] portable mode, data=%s", info.DataDir)
	}

	err = wails.Run(&options.App{
		Title:            "Outline Reader",
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
		Bind: []interface{}{
			app,
		},
		Logger:  logger.NewDefaultLogger(),
		Windows: winOpts,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: uniqueID,
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
				log.Println("[APP] second instance detected, focusing existing window")
			},
		},
	})

	if err != nil {
		log.Fatal("Error:", err.Error())
	}
}
