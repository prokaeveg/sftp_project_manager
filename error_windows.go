//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

func showError(message string) {
	printError(message)
	if len(os.Args) == 2 && os.Args[1] == "--diagnostics" {
		return
	}
	text, _ := syscall.UTF16PtrFromString(message)
	title, _ := syscall.UTF16PtrFromString("SFTP Project Manager")
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}
