package main

import (
	"awesomeProject/internal/application"
	"awesomeProject/internal/transport"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	wails "github.com/wailsapp/wails/v3/pkg/application"
	"io/fs"
	"os"
	"sync"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if handled, err := transport.RunAskpass(); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		showError(err.Error())
		os.Exit(1)
	}
}

func run() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	service, err := application.New(executable)
	if err != nil {
		return err
	}
	var shutdownOnce sync.Once
	shutdown := func() { shutdownOnce.Do(service.Shutdown) }
	defer shutdown()
	if len(os.Args) == 2 && os.Args[1] == "--diagnostics" {
		return json.NewEncoder(os.Stdout).Encode((&App{service: service}).GetStatus())
	}
	if err := requireWebview(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge := &App{service: service, ctx: ctx}
	frontend, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return err
	}
	app := wails.New(wails.Options{
		Name:       "SFTP Project Manager",
		Assets:     wails.AssetOptions{Handler: wails.BundledAssetFileServer(frontend)},
		Services:   []wails.Service{wails.NewService(bridge)},
		Windows:    wails.WindowsOptions{WebviewUserDataPath: service.Status().WebviewDataDir},
		OnShutdown: func() { cancel(); shutdown() },
	})
	window := app.Window.NewWithOptions(wails.WebviewWindowOptions{Title: "SFTP Project Manager", URL: "/", Width: 1280, Height: 800, MinWidth: 960, MinHeight: 640})
	bridge.desktop = &wailsDesktop{app: app, window: window}
	return app.Run()
}

func printError(message string) { fmt.Fprintln(os.Stderr, message) }
