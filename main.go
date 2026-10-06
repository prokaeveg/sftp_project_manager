package main

import (
	"awesomeProject/internal/application"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"os"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
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
	if len(os.Args) == 2 && os.Args[1] == "--diagnostics" {
		return json.NewEncoder(os.Stdout).Encode((&App{service: service}).GetStatus())
	}
	messages := windows.DefaultMessages()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := &App{service: service, ctx: ctx}
	messages.Error = "Ошибка запуска"
	messages.MissingRequirements = "Отсутствует WebView2"
	messages.ContactAdmin = "Не найден подходящий WebView2 Runtime. Эта сборка использует установленный runtime. Автоматическая установка не выполняется."
	messages.Webview2NotInstalled = messages.ContactAdmin
	return wails.Run(&options.App{
		Title: "SFTP Project Manager", Width: 1280, Height: 800, MinWidth: 960, MinHeight: 640,
		AssetServer: &assetserver.Options{Assets: assets},
		Bind:        []interface{}{app},
		OnStartup:   app.startup,
		OnShutdown:  func(context.Context) { cancel(); service.Shutdown() },
		Windows:     &windows.Options{WebviewUserDataPath: service.Status().WebviewDataDir, Messages: messages},
	})
}

func printError(message string) { fmt.Fprintln(os.Stderr, message) }
