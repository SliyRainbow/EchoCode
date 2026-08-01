package main

import (
	"embed"
	"os"

	"github.com/SliyRainbow/EchoCode/internal/appupdate"
	"github.com/SliyRainbow/EchoCode/internal/plugins"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed all:skills
var bundledSkills embed.FS

func main() {
	if handled, exitCode := plugins.HandleCommandLine(os.Args[1:]); handled {
		os.Exit(exitCode)
	}
	if handled, exitCode := appupdate.HandleCommandLine(os.Args[1:]); handled {
		os.Exit(exitCode)
	}
	appupdate.ScheduleCleanup(os.Args[1:])
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "EchoCode",
		Width:     1280,
		Height:    820,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 247, G: 248, B: 245, A: 1},
		Windows: &windows.Options{
			WebviewUserDataPath:               webviewUserDataDir(),
			DisableFramelessWindowDecorations: false,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
