package main

import (
	"awesomeProject/internal/application"
	"awesomeProject/internal/config"
	"errors"
	"path/filepath"
	"testing"
)

type fakeDesktop struct {
	directory, selected, copied string
	err                         error
}

func (d *fakeDesktop) SelectDirectory(initial string) (string, error) {
	d.directory = initial
	return d.selected, d.err
}
func (d *fakeDesktop) SetClipboard(text string) error { d.copied = text; return d.err }
func TestSelectLocalDirectoryUsesValidInitialAndValidatesResult(t *testing.T) {
	dir := t.TempDir()
	desktop := &fakeDesktop{selected: dir}
	app := &App{desktop: desktop}
	got, err := app.SelectLocalDirectory(dir)
	if err != nil || got != filepath.Clean(dir) || desktop.directory != dir {
		t.Fatalf("got %q, initial %q, err %v", got, desktop.directory, err)
	}
	desktop.selected = ""
	got, err = app.SelectLocalDirectory(filepath.Join(dir, "missing"))
	if err != nil || got != "" || desktop.directory != "" {
		t.Fatalf("cancel: %q initial %q err %v", got, desktop.directory, err)
	}
	desktop.err = errors.New("dialog failed")
	if _, err = app.SelectLocalDirectory(dir); err == nil {
		t.Fatal("expected dialog error")
	}
	desktop.err = nil
	desktop.selected = filepath.Join(dir, "missing")
	if _, err = app.SelectLocalDirectory(dir); err == nil {
		t.Fatal("expected invalid directory error")
	}
}

func TestCopyPathsWritesClipboardAndReportsFailure(t *testing.T) {
	service, err := application.New(filepath.Join(t.TempDir(), "app.exe"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Shutdown)
	root := t.TempDir()
	status, err := service.SaveProject(config.Project{Name: "Local", LocalRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	desktop := &fakeDesktop{}
	app := &App{service: service, desktop: desktop}
	id := status.Settings.ActiveProjectID
	if err := app.CopyPaths(id, "local", "", "", []string{"file.txt"}, "local"); err != nil {
		t.Fatal(err)
	}
	if desktop.copied != filepath.Join(root, "file.txt") {
		t.Fatalf("clipboard got %q", desktop.copied)
	}
	failure := errors.New("clipboard unavailable")
	desktop.err = failure
	if err := app.CopyPaths(id, "local", "", "", []string{"other.txt"}, "local"); !errors.Is(err, failure) {
		t.Fatalf("expected clipboard error, got %v", err)
	}
	desktop.copied = "unchanged"
	if err := app.CopyPaths(id, "local", "", "", []string{"../escape"}, "local"); err == nil {
		t.Fatal("expected invalid path error")
	}
	if desktop.copied != "unchanged" {
		t.Fatalf("clipboard changed on invalid input: %q", desktop.copied)
	}
}
