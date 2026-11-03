package main

import (
	"fmt"
	wails "github.com/wailsapp/wails/v3/pkg/application"
)

type desktopAdapter interface {
	SelectDirectory(initial string) (string, error)
	SetClipboard(text string) error
}

type wailsDesktop struct {
	app    *wails.App
	window *wails.WebviewWindow
}

func (d *wailsDesktop) SelectDirectory(initial string) (string, error) {
	return d.app.Dialog.OpenFileWithOptions(&wails.OpenFileDialogOptions{Title: "Выберите локальную папку проекта", Directory: initial, CanChooseDirectories: true, Window: d.window}).PromptForSingleSelection()
}
func (d *wailsDesktop) SetClipboard(text string) error {
	if !d.app.Clipboard.SetText(text) {
		return fmt.Errorf("ошибка записи в буфер обмена")
	}
	return nil
}
