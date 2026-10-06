package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/transport"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sampleProfile(t *testing.T) config.Profile {
	t.Helper()
	return config.Profile{Name: "Test", LocalRoot: t.TempDir(), RemoteRoot: "/srv/app", Connection: transport.Connection{Mode: "manual", Host: "localhost", User: "test", Port: 22}}
}

func TestProjectSaveSelectAndDefensiveCopies(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "app.exe")
	svc, err := New(exe)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, name := range []string{"src", "src2", "docs"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	manual := transport.Connection{Mode: "manual", Host: "one.test", User: "me", Port: 22}
	project := config.Project{Name: "Multi", LocalRoot: root, Connections: []config.ProjectConnection{
		{Name: "Primary", Connection: manual, Mappings: []config.Mapping{{LocalPath: "src", RemotePath: "/srv/app/src"}, {LocalPath: "src2", RemotePath: "/srv/app/src2"}}},
		{Name: "Other server", Connection: transport.Connection{Mode: "manual", Host: "two.test", User: "me", Port: 22}, Mappings: []config.Mapping{{LocalPath: "docs", RemotePath: "/data/docs"}}},
	}}
	status, err := svc.SaveProject(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Settings.Projects) != 1 {
		t.Fatalf("projects: %+v", status.Settings.Projects)
	}
	saved := status.Settings.Projects[0]
	if saved.ID == "" || saved.Connections[0].ID == "" || saved.Connections[0].Mappings[0].ID == "" {
		t.Fatal("IDs were not generated")
	}
	project.Connections[0].Mappings[0].LocalPath = "changed"
	status.Settings.Projects[0].Connections[0].Mappings[0].LocalPath = "also changed"
	if svc.Status().Settings.Projects[0].Connections[0].Mappings[0].LocalPath != "src" {
		t.Fatal("project state was mutated by caller")
	}
	preview, err := svc.PreviewTransfer("upload", "file.txt")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := svc.SelectProject(saved.ID, saved.Connections[1].ID, saved.Connections[1].Mappings[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	profile := selected.Settings.Profile
	if profile == nil || profile.LocalRoot != filepath.Join(root, "docs") || profile.RemoteRoot != "/data/docs" || profile.Connection.Host != "two.test" {
		t.Fatalf("selected profile: %+v", profile)
	}
	restarted, err := New(exe)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Status().Settings.ActiveConnectionID != saved.Connections[1].ID || restarted.Status().Settings.Profile.Connection.Host != "two.test" {
		t.Fatal("selection was not persisted")
	}
	second := config.Project{Name: "Same root", LocalRoot: root, Connections: []config.ProjectConnection{{Name: "Other", Connection: manual, Mappings: []config.Mapping{{LocalPath: "src", RemotePath: "/other"}}}}}
	if _, err := svc.SaveProject(second); err != nil {
		t.Fatalf("projects sharing a local root should be allowed: %v", err)
	}
	restarted, err = New(exe)
	if err != nil {
		t.Fatal(err)
	}
	if len(restarted.Status().Settings.Projects) != 2 {
		t.Fatalf("saved projects were lost: %+v", restarted.Status().Settings.Projects)
	}
	if svc.pending.preview != preview || svc.pending.profile.Connection.Host != "one.test" {
		t.Fatal("selection changed pending transfer snapshot")
	}
	before := svc.Status().Settings
	if _, err := svc.SelectProject(saved.ID, "missing", saved.Connections[0].Mappings[0].ID); err == nil {
		t.Fatal("unknown ID selected")
	}
	if svc.Status().Settings.ActiveConnectionID != before.ActiveConnectionID {
		t.Fatal("failed selection mutated settings")
	}
}

func TestProjectRejectsOverlappingMappings(t *testing.T) {
	svc, err := New(filepath.Join(t.TempDir(), "app.exe"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	p := config.Project{Name: "Overlap", LocalRoot: root, Connections: []config.ProjectConnection{{Name: "Primary", Connection: transport.Connection{Mode: "manual", Host: "server", User: "me", Port: 22}, Mappings: []config.Mapping{{LocalPath: ".", RemotePath: "/srv/app"}, {LocalPath: "child", RemotePath: "/srv/app-child"}}}}}
	if _, err := svc.SaveProject(p); err == nil || !strings.Contains(err.Error(), "пересекаются") {
		t.Fatalf("expected overlap error, got %v", err)
	}
}

func TestProfilePersistenceAndSnapshot(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "app.exe")
	service, err := New(exe)
	if err != nil {
		t.Fatal(err)
	}
	profile := sampleProfile(t)
	if _, err := service.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	loaded, err := New(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*loaded.Status().Settings.Profile, profile) {
		t.Fatal("profile not restored")
	}
	status := service.Status()
	status.Settings.Profile.Connection.Host = "changed"
	if service.Status().Settings.Profile.Connection.Host != "localhost" {
		t.Fatal("status mutates service")
	}
	preview, err := service.PreviewTransfer("upload", "file.txt")
	if err != nil {
		t.Fatal(err)
	}
	profile.Connection.Host = "another-server"
	if _, err := service.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	if service.pending.profile.Connection.Host != "localhost" || service.pending.preview != preview {
		t.Fatal("preview destination changed")
	}
	if _, err := service.PreviewTransfer("upload", "../escape"); err == nil {
		t.Fatal("traversal preview accepted")
	}
}

func TestLocalListingAndUnsafeProfile(t *testing.T) {
	service, err := New(filepath.Join(t.TempDir(), "app.exe"))
	if err != nil {
		t.Fatal(err)
	}
	profile := sampleProfile(t)
	if err := os.WriteFile(filepath.Join(profile.LocalRoot, "данные.txt"), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	entries, err := service.ListLocal(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Size != 3 || entries[0].RelativePath != "данные.txt" {
		t.Fatalf("entries: %+v", entries)
	}
	if _, err := service.ListLocal("../outside"); err == nil {
		t.Fatal("traversal listing accepted")
	}
	profile.Connection.Host = "-malicious"
	if _, err := service.SaveProfile(profile); err == nil {
		t.Fatal("unsafe profile saved")
	}
	if service.Status().Settings.Profile.Connection.Host != "localhost" {
		t.Fatal("failed save modified settings")
	}
}
