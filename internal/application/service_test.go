package application

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsSurviveRestart(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "manager.exe")
	service, err := New(exe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveProjectName("  Мой проект  "); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(exe)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Status().Settings.ProjectName != "Мой проект" {
		t.Fatal("name not restored")
	}
	if _, err := service.SaveProjectName(strings.Repeat("я", 121)); err == nil {
		t.Fatal("long name accepted")
	}
	if service.Status().Settings.ProjectName != "Мой проект" {
		t.Fatal("failed save changed state")
	}
}

func TestMissingSSHDoesNotBlockStartup(t *testing.T) {
	t.Setenv("PATH", "")
	service, err := New(filepath.Join(t.TempDir(), "manager.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if service.Status().SSHPath != "" || !strings.Contains(service.Status().SSHMessage, "не найден") {
		t.Fatalf("status: %+v", service.Status())
	}
}

func TestCorruptConfigBlocksStartup(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "manager.exe")
	if _, err := New(exe); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "data", "settings.json")
	if err := os.WriteFile(file, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(exe); err == nil {
		t.Fatal("corrupt settings accepted")
	}
	got, _ := os.ReadFile(file)
	if string(got) != "broken" {
		t.Fatal("corrupt settings modified")
	}
}
