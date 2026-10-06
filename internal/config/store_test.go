package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPortableDataDir(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "Папка с пробелами", "manager.exe")
	got, err := DataDir(exe)
	if err != nil || got != filepath.Join(filepath.Dir(exe), "data") {
		t.Fatalf("DataDir = %q, %v", got, err)
	}
	if _, err := DataDir("manager.exe"); err == nil {
		t.Fatal("relative path accepted")
	}
}

func TestSaveRestoreAndBackup(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := store.Load()
	if err != nil || !reflect.DeepEqual(settings, Default()) {
		t.Fatalf("initial settings: %+v, %v", settings, err)
	}
	settings.ProjectName = "Первый проект"
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	settings.ProjectName = "Второй проект"
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Load()
	if err != nil || !reflect.DeepEqual(restored, settings) {
		t.Fatalf("restored: %+v, %v", restored, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json.bak"))
	if err != nil {
		t.Fatal(err)
	}
	previous, err := decode(data)
	if err != nil || previous.ProjectName != "Первый проект" {
		t.Fatalf("backup: %+v, %v", previous, err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".settings-*"))
	if len(files) != 0 {
		t.Fatalf("temporary files: %v", files)
	}
}

func TestInvalidConfigIsPreserved(t *testing.T) {
	for _, data := range []string{`{broken`, `{"schemaVersion":99}`} {
		t.Run(data, func(t *testing.T) {
			dir := t.TempDir()
			store, err := New(dir)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(file, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Load(); err == nil {
				t.Fatal("invalid config loaded")
			}
			if err := store.Save(Default()); err == nil {
				t.Fatal("invalid config overwritten")
			}
			got, _ := os.ReadFile(file)
			if string(got) != data {
				t.Fatal("original config modified")
			}
		})
	}
}

func TestSchemaOneMigrationIsReadOnlyUntilSave(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"schemaVersion":1,"projectName":"legacy","profile":{"name":"Legacy","localRoot":"C:/offline/project","remoteRoot":"/srv/app","connection":{"mode":"manual","host":"example.test","user":"me","port":22}}}`)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), original, 0600); err != nil {
		t.Fatal(err)
	}
	settings, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if settings.SchemaVersion != 2 || len(settings.Projects) != 1 || settings.ActiveProjectID != "legacy-project" || settings.Projects[0].Connections[0].Mappings[0].ID != "legacy-mapping" {
		t.Fatalf("migration: %+v", settings)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(got) != string(original) {
		t.Fatal("load rewrote schema-1 settings")
	}
	settings.ProjectName = "after save"
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"settings.json.bak", "settings.json.v1.bak"} {
		backup, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(backup) != string(original) {
			t.Fatalf("%s did not preserve original", name)
		}
	}
}

func TestSchemaTwoRejectsDuplicateIDsAndBadSelection(t *testing.T) {
	base := `{"schemaVersion":2,"projects":[{"id":"p","name":"P","localRoot":"C:/offline","connections":[{"id":"c","name":"C","connection":{},"mappings":[{"id":"m","localPath":".","remotePath":"/srv"}]}]}],"activeProjectID":"p","activeConnectionID":"c","activeMappingID":"m"}`
	if _, err := decode([]byte(base)); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}
	badSelection := strings.Replace(base, `"activeConnectionID":"c"`, `"activeConnectionID":"missing"`, 1)
	if _, err := decode([]byte(badSelection)); err == nil {
		t.Fatal("unknown selection accepted")
	}
	duplicate := strings.Replace(base, `"remotePath":"/srv"`, `"remotePath":"/srv"},{"id":"m","localPath":"other","remotePath":"/other"`, 1)
	if _, err := decode([]byte(duplicate)); err == nil {
		t.Fatal("duplicate ID accepted")
	}
}

func TestUnwritableLocation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(filepath.Join(file, "data")); err == nil {
		t.Fatal("file accepted as directory")
	}
}

func TestInvalidExclusionsOnDiskArePreserved(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"schemaVersion":2,"projects":[{"id":"p","name":"P","localRoot":"C:/offline","connections":[{"id":"c","name":"C","connection":{},"excludes":["../outside"],"mappings":[{"id":"m","localPath":"src","remotePath":"/srv"}]}]}],"activeProjectID":"p","activeConnectionID":"c","activeMappingID":"m"}`)
	file := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("invalid exclusion loaded")
	}
	if err := store.Save(Default()); err == nil {
		t.Fatal("invalid settings overwritten")
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != string(data) {
		t.Fatal("invalid original was not preserved")
	}
}
