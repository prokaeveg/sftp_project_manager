package application

import (
	"awesomeProject/internal/config"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectSearchExcludedDirectoriesPersistAndDefaultDotDirectories(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "app.exe")
	s, err := New(executable)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	root := t.TempDir()
	for _, name := range []string{".idea/hidden.txt", "src/.cache/nested.txt", "src/visible.txt", "vendor/lib.txt", "vendor-extra/keep.txt", ".env"} {
		filename := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte("text"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	status, err := s.SaveProject(config.Project{Name: "Search", LocalRoot: root, Connections: []config.ProjectConnection{}})
	if err != nil {
		t.Fatal(err)
	}
	id := status.Settings.ActiveProjectID
	if !status.Settings.Projects[0].ExcludeDotDirectories {
		t.Fatal("new project missing dot directory default")
	}
	check := func(name string, count int) {
		t.Helper()
		result, err := s.SearchProjectFiles(context.Background(), id, name)
		if err != nil || len(result.Files) != count {
			t.Fatalf("search %q = %+v err=%v want %d", name, result, err, count)
		}
	}
	check("hidden", 0)
	check("nested", 0)
	check("visible", 1)
	check(".env", 1)
	entries, err := s.ListProjectLocal(".")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Name == ".idea" {
			found = e.SearchExcluded
		}
	}
	if !found {
		t.Fatal("dot directory has no search mark")
	}
	if _, err := s.SetDirectorySearchExcluded(id, "vendor", true); err != nil {
		t.Fatal(err)
	}
	check("lib.txt", 0)
	check("keep.txt", 1)
	if _, err := s.SetDirectorySearchExcluded(id, ".idea", false); err != nil {
		t.Fatal(err)
	}
	check("hidden", 1)
	// The mark is search-only: manual file viewing and directory listing stay available.
	if _, err := s.ReadLocalFile(id, "vendor/lib.txt"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(executable)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reloaded.Shutdown)
	result, err := reloaded.SearchProjectFiles(context.Background(), id, "lib")
	if err != nil || len(result.Files) != 0 {
		t.Fatalf("persisted exclusion: %+v %v", result, err)
	}
	result, err = reloaded.SearchProjectFiles(context.Background(), id, "hidden")
	if err != nil || len(result.Files) != 1 {
		t.Fatalf("persisted dot override: %+v %v", result, err)
	}
	if _, err := s.SetDirectorySearchExcluded(id, "vendor", false); err != nil {
		t.Fatal(err)
	}
	check("lib", 1)
	for _, invalid := range []string{".", "../outside", "src/visible.txt", root} {
		if _, err := s.SetDirectorySearchExcluded(id, invalid, true); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	// Status snapshots must not allow callers to mutate the stored exclusion list.
	status, err = s.SetDirectorySearchExcluded(id, "vendor", true)
	if err != nil {
		t.Fatal(err)
	}
	status.Settings.Projects[0].SearchExcludedDirectories[0] = "src"
	check("lib", 0)
	check("visible", 1)
}
