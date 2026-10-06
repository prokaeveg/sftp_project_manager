package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/transport"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExclusionsPersistenceMappingAndEnforcement(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "app.exe")
	s, err := New(exe)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src", "private"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"keep.txt", "debug.log", ".env"} {
		if err := os.WriteFile(filepath.Join(root, "src", name), []byte("sample"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := config.Project{Name: "Rules", LocalRoot: root, Connections: []config.ProjectConnection{
		{Name: "Protected", Connection: transport.Connection{Mode: "manual", Host: "example.invalid", User: "test", Port: 22}, Mappings: []config.Mapping{{LocalPath: "src", RemotePath: "/srv/app"}}, Excludes: []string{"src/*.log", "src/private/**", "**/.env"}},
		{Name: "Other", Connection: transport.Connection{Mode: "manual", Host: "example.invalid", User: "test", Port: 22}, Mappings: []config.Mapping{{LocalPath: "src", RemotePath: "/other"}}},
	}}
	status, err := s.SaveProject(p)
	if err != nil {
		t.Fatal(err)
	}
	saved := cloneProject(status.Settings.Projects[0])
	p.Connections[0].Excludes[0] = "**"
	status.Settings.Projects[0].Connections[0].Excludes[0] = "**"
	status.Settings.Profile.Excludes[0] = "**"
	if got := s.Status().Settings.Profile.Excludes[0]; got != "src/*.log" {
		t.Fatalf("caller mutated policy: %s", got)
	}
	profile, err := s.profile()
	if err != nil {
		t.Fatal(err)
	}
	profile.Excludes[0] = "**"
	entries, err := s.ListLocal(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("entries should remain visible: %+v", entries)
	}
	for _, entry := range entries {
		want := entry.Name != "keep.txt"
		if entry.Excluded != want {
			t.Fatalf("flag for %s = %v, want %v", entry.Name, entry.Excluded, want)
		}
	}
	// These requests must fail before any attempt to launch OpenSSH.
	s.status.SSHPath = ""
	for _, direction := range []string{"upload", "download"} {
		for _, name := range []string{"debug.log", ".env", "private/nested/file.txt"} {
			if _, err := s.PreviewTransfer(direction, name); err == nil || !strings.Contains(err.Error(), "исключён") {
				t.Fatalf("%s %s accepted: %v", direction, name, err)
			}
		}
	}
	if _, err := s.ListLocal("private"); err == nil || !strings.Contains(err.Error(), "исключён") {
		t.Fatalf("excluded local directory: %v", err)
	}
	if _, err := s.ListRemote(context.Background(), "private"); err == nil || !strings.Contains(err.Error(), "исключён") {
		t.Fatalf("excluded remote directory attempted SSH: %v", err)
	}
	preview, err := s.PreviewTransfer("upload", "keep.txt")
	if err != nil {
		t.Fatal(err)
	}
	// An invalid edit must preserve both saved rules and an existing preview.
	bad := cloneProject(saved)
	bad.Connections[0].Excludes = []string{"../outside"}
	if _, err := s.SaveProject(bad); err == nil {
		t.Fatal("invalid rules saved")
	}
	if s.pending == nil || s.pending.preview != preview {
		t.Fatal("invalid save altered preview")
	}
	saved.Connections[0].Excludes = append(saved.Connections[0].Excludes, "src/keep.txt")
	if _, err := s.SaveProject(saved); err != nil {
		t.Fatal(err)
	}
	if err := s.ExecuteTransfer(context.Background(), preview.Token, false); err == nil || !strings.Contains(err.Error(), "устарел") {
		t.Fatalf("old policy token usable: %v", err)
	}
	restarted, err := New(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restarted.Status().Settings.Profile.Excludes, saved.Connections[0].Excludes) {
		t.Fatal("rules lost after restart")
	}
	if restarted.Status().Settings.Profile.ExcludeBase != "src" {
		t.Fatal("mapping prefix lost")
	}
	if _, err := restarted.SelectProject(saved.ID, saved.Connections[1].ID, saved.Connections[1].Mappings[0].ID); err != nil {
		t.Fatal(err)
	}
	otherPreview, err := restarted.PreviewTransfer("download", ".env")
	if err != nil {
		t.Fatalf("rules leaked to another connection: %v", err)
	}
	// Replacing a connection must invalidate tokens from its removed predecessor.
	saved.Connections[1].ID = ""
	saved.Connections[1].Excludes = []string{"src/.env"}
	if _, err := restarted.SaveProject(saved); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ExecuteTransfer(context.Background(), otherPreview.Token, false); err == nil || !strings.Contains(err.Error(), "устарел") {
		t.Fatalf("removed connection token remained valid: %v", err)
	}
}

func TestExecuteTransferRechecksSnapshotExclusions(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "app.exe"))
	if err != nil {
		t.Fatal(err)
	}
	profile := sampleProfile(t)
	profile.Excludes = []string{"secret.txt"}
	if _, err := s.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	s.status.SSHPath = ""
	// Simulate an invalid prepared plan to exercise the backend guard itself.
	s.pending = &transferPlan{preview: Preview{Token: "test", Direction: "upload"}, profile: profile, relative: "secret.txt"}
	if err := s.ExecuteTransfer(context.Background(), "test", false); err == nil || !strings.Contains(err.Error(), "исключён") {
		t.Fatalf("execution attempted SSH for excluded file: %v", err)
	}
}
