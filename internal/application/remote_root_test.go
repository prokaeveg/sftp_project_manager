package application

import (
	"awesomeProject/internal/config"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"
)

func TestRemoteRootBrowsingIsIndependentOfDeploymentMapping(t *testing.T) {
	service, profile := navigationTestService(t)
	if err := os.Mkdir(filepath.Join(profile.LocalRoot, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	project := service.Status().Settings.Projects[0]
	connection := &project.Connections[0]
	connection.RootPath = "/remote"
	connection.Mappings = []config.Mapping{{ID: "legacy-mapping", LocalPath: "src", DeploymentPath: "folder"}}
	connection.Excludes = []string{"src/nested/**"}
	if _, err := service.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	opens := installNavigationFixture(t, service, sftp.InMemHandler())
	entries, err := service.ListRemote(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].RelativePath != "folder" {
		t.Fatalf("listing must start at server root, got %+v", entries)
	}
	if _, err := service.ListRemote(context.Background(), "one"); err != nil {
		t.Fatalf("unmapped remote folder cannot be browsed: %v", err)
	}
	entries, err = service.ListRemote(context.Background(), "folder")
	if err != nil || len(entries) != 1 || !entries[0].Excluded || entries[0].RelativePath != "folder/nested" {
		t.Fatalf("root-relative listing must translate exclusions to project paths: %+v, %v", entries, err)
	}
	if _, err := service.ListRemote(context.Background(), "folder/nested"); err == nil {
		t.Fatal("excluded mapped folder was listed")
	}
	if _, err := service.ListRemote(context.Background(), "../outside"); err == nil {
		t.Fatal("listing escaped server root")
	}
	connection.RootPath = "/remote/one"
	if _, err := service.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	entries, err = service.ListRemote(context.Background(), ".")
	if err != nil || len(entries) != 0 {
		t.Fatalf("changed root should show /remote/one: %+v, %v", entries, err)
	}
	if opens.Load() != 1 {
		t.Fatal("root or mapping changes reopened unchanged SSH connection")
	}
}

func TestRemoteRootWithoutMappingsRetainsNavigationAndTracksSSHChanges(t *testing.T) {
	service, _ := navigationTestService(t)
	project := service.Status().Settings.Projects[0]
	project.Connections[0].RootPath = "/"
	project.Connections[0].Mappings = []config.Mapping{}
	if _, err := service.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	if service.Status().Settings.Profile != nil {
		t.Fatal("browse-only server received a transfer profile")
	}
	opens := installNavigationFixture(t, service, sftp.InMemHandler())
	for _, relative := range []string{".", "remote", "remote/folder"} {
		if _, err := service.ListRemote(context.Background(), relative); err != nil {
			t.Fatal(err)
		}
	}
	if opens.Load() != 1 {
		t.Fatal("browse-only navigation did not reuse session")
	}
	if _, err := service.PreviewBatch(context.Background(), "upload", []string{"file.txt"}); err == nil {
		t.Fatal("transfer allowed without a mapping")
	}
	project.Connections[0].Mappings = []config.Mapping{{ID: "manual", LocalPath: ".", DeploymentPath: "remote"}}
	if _, err := service.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListRemote(context.Background(), "."); err != nil {
		t.Fatal(err)
	}
	if opens.Load() != 1 {
		t.Fatal("adding mapping reopened unchanged SSH")
	}
	project.Connections[0].Mappings = nil
	project.Connections[0].Connection.Host = "changed.test"
	if _, err := service.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	if service.GetNavigationStatus().State != "disconnected" {
		t.Fatal("SSH change with no transfer profile retained stale session")
	}
	if _, err := service.ListRemote(context.Background(), "."); err != nil {
		t.Fatal(err)
	}
	if opens.Load() != 2 {
		t.Fatal("SSH change did not reopen connection")
	}
}

func TestRemoteRootExclusionsUseEachMappingsLocalPrefix(t *testing.T) {
	connection := config.ProjectConnection{
		RootPath: "/var/www/html",
		Mappings: []config.Mapping{{LocalPath: "src", DeploymentPath: "app"}, {LocalPath: "docs", DeploymentPath: "manual"}},
		Excludes: []string{"src/private/**", "docs/*.log", "**/.env"},
	}
	for relative, want := range map[string]bool{
		"app/private": true, "app/private/key": true, "app/.env": true,
		"manual/build.log": true, "manual/index.html": false, "app/index.php": false,
		"other/private/key": false, "application/private/key": false, ".": false,
	} {
		if got := remoteRootEntryExcluded(connection, relative); got != want {
			t.Errorf("excluded(%q) = %v, want %v", relative, got, want)
		}
	}
	// A manually edited legacy file may contain overlapping mappings. Match the
	// most specific deployment, like the UI, regardless of configuration order.
	connection.Mappings = []config.Mapping{{LocalPath: ".", DeploymentPath: "."}, {LocalPath: "src", DeploymentPath: "app"}}
	if !remoteRootEntryExcluded(connection, "app/private/key") {
		t.Fatal("broad mapping shadowed the most specific deployment exclusion")
	}
}
