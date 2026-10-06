//go:build !windows

package main

func webviewInfo() (string, string) {
	return "", "Первая поставка предназначена для Windows x64."
}
