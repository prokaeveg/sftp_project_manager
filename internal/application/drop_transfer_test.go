package application

import (
	"awesomeProject/internal/config"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDropTransferUsesExplicitDirectoriesWithoutMappings(t *testing.T) {
	profile, client := batchFixture(t)
	project := config.Project{LocalRoot: profile.LocalRoot}
	connection := config.ProjectConnection{RootPath: "/remote", Connection: profile.Connection}
	if err := os.MkdirAll(filepath.Join(project.LocalRoot, "source", "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(project.LocalRoot, "downloads"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project.LocalRoot, "source", "folder", "a.txt"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := client.Mkdir("/remote/target"); err != nil {
		t.Fatal(err)
	}
	upload, name, err := dropTransferProfile(project, connection, "upload", "source/folder", "target")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildBatch(context.Background(), upload, "upload", []string{name}, client)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range plan.items {
		if err := executeBatchItem(context.Background(), upload, "upload", item, "overwrite", client); err != nil {
			t.Fatal(err)
		}
	}
	download, name, err := dropTransferProfile(project, connection, "download", "target/folder", "downloads")
	if err != nil {
		t.Fatal(err)
	}
	plan, err = buildBatch(context.Background(), download, "download", []string{name}, client)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range plan.items {
		if err := executeBatchItem(context.Background(), download, "download", item, "overwrite", client); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(project.LocalRoot, "downloads", "folder", "a.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("download: %q %v", data, err)
	}
	for _, direction := range []string{"upload", "download"} {
		for _, pair := range [][2]string{{"../escape", "."}, {"source/folder", "../outside"}, {".", "."}, {"source/folder", "/absolute"}} {
			if _, _, err := dropTransferProfile(project, connection, direction, pair[0], pair[1]); err == nil {
				t.Fatalf("accepted %s %v", direction, pair)
			}
		}
	}
}
