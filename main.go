package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/logger"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed frontend/*
var assets embed.FS

func main() {
	app := NewApp()
	if err := app.ensure(); err != nil {
		log.Printf("[APP] init: %v", err)
	}

	err := wails.Run(&options.App{
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
		Bind:      []interface{}{app},
		Logger:    logger.NewDefaultLogger(),
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})

	if err != nil {
		log.Fatal("Error:", err.Error())
	}
}
