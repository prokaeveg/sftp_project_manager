package main

import (
	"awesomeProject/internal/application"
	"awesomeProject/internal/config"
	"awesomeProject/internal/transport"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	wails "github.com/wailsapp/wails/v3/pkg/application"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	if len(os.Args) == 2 && os.Args[1] == "--diagnostics" {
		service, err := application.New(executable)
		if err != nil {
			return err
		}
		defer service.Shutdown()
		return json.NewEncoder(os.Stdout).Encode((&session{service: service}).GetStatus())
	}
	if err := requireWebview(); err != nil {
		return err
	}
	dataDir, err := config.DataDir(executable)
	if err != nil {
		return err
	}
	manager := &windowManager{sessions: map[uint]*session{}, windows: map[uint]*wails.WebviewWindow{}, executable: executable}
	bridge := &App{manager: manager}
	frontend, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return err
	}
	app := wails.New(wails.Options{
		Name:           "SFTP Project Manager",
		Assets:         wails.AssetOptions{Handler: wails.BundledAssetFileServer(frontend)},
		Services:       []wails.Service{wails.NewService(bridge)},
		Windows:        wails.WindowsOptions{WebviewUserDataPath: filepath.Join(dataDir, "webview2")},
		OnShutdown:     manager.shutdown,
		SingleInstance: &wails.SingleInstanceOptions{UniqueID: singleInstanceID(executable), OnSecondInstanceLaunch: func(wails.SecondInstanceData) { manager.focusAny() }},
	})
	service, err := application.New(executable)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	initial := &session{service: service, ctx: ctx, cancel: cancel}
	manager.app = app
	manager.registerWindowHook()
	manager.add(initial)
	defer manager.shutdown()
	return app.Run()
}

func printError(message string) { fmt.Fprintln(os.Stderr, message) }

func singleInstanceID(executable string) string {
	dir := strings.ToLower(filepath.Clean(filepath.Dir(executable)))
	digest := sha256.Sum256([]byte(dir))
	return fmt.Sprintf("sftp.project.manager.p%x", digest)
}
