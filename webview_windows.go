//go:build windows

package main

import (
	"fmt"
	"github.com/wailsapp/go-webview2/webviewloader"
)

func webviewInfo() (string, string) {
	version, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
	if err != nil {
		return "", "Ошибка определения WebView2: " + err.Error()
	}
	if version == "" {
		return "", "WebView2 Runtime не найден. Эта сборка требует установленный runtime."
	}
	return version, "Обнаружен WebView2 Runtime."
}

func requireWebview() error {
	version, message := webviewInfo()
	if version == "" {
		return fmt.Errorf("%s", message)
	}
	return nil
}
