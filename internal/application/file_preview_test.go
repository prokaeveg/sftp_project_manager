package application

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"
)

func TestFilePreviewLocalSearchAndLimits(t *testing.T) {
	s, p := navigationTestService(t)
	root := p.LocalRoot
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{
		"nested/error_windows.go": []byte("package main\n// Привет\n"),
		".env":                    []byte("SECRET"), "binary.bin": {0, 1, 2},
		"large.txt":         []byte(strings.Repeat("a", maxPreviewBytes+1)),
		"utf16.txt":         {0xff, 0xfe, 'h', 0, 'i', 0},
		"invalid-utf16.txt": {0xff, 0xfe, 0, 0xd8, 'a', 0},
	} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	p.Excludes = []string{".env"}
	if _, err := s.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	id := s.Status().Settings.ActiveProjectID
	file, err := s.ReadLocalFile(id, "nested/error_windows.go")
	if err != nil || !strings.Contains(file.Text, "Привет") || file.Binary || file.Truncated {
		t.Fatalf("file=%+v err=%v", file, err)
	}
	for _, name := range []string{"../outside", ".env", "nested", filepath.Join(root, "binary.bin")} {
		if _, err := s.ReadLocalFile(id, name); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	binary, err := s.ReadLocalFile(id, "binary.bin")
	if err != nil || !binary.Binary || binary.Text != "" {
		t.Fatalf("binary=%+v err=%v", binary, err)
	}
	large, err := s.ReadLocalFile(id, "large.txt")
	if err != nil || !large.Truncated || len(large.Text) > maxPreviewBytes {
		t.Fatalf("large preview err=%v", err)
	}
	utf16, err := s.ReadLocalFile(id, "utf16.txt")
	if err != nil || utf16.Text != "hi" {
		t.Fatalf("utf16=%+v err=%v", utf16, err)
	}
	invalid, err := s.ReadLocalFile(id, "invalid-utf16.txt")
	if err != nil || !invalid.Binary {
		t.Fatalf("invalid utf16=%+v err=%v", invalid, err)
	}
	result, err := s.SearchProjectFiles(context.Background(), id, "erwin")
	if err != nil || len(result.Files) != 1 || result.Files[0].RelativePath != "nested/error_windows.go" {
		t.Fatalf("search=%+v err=%v", result, err)
	}
	result, err = s.SearchProjectFiles(context.Background(), id, "env")
	if err != nil || len(result.Files) != 0 {
		t.Fatalf("excluded search=%+v err=%v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.SearchProjectFiles(ctx, id, "go"); err == nil {
		t.Fatal("cancelled search succeeded")
	}
}

func TestFilePreviewRemoteCompareUsesMappingAndKeepsSelection(t *testing.T) {
	s, p := navigationTestService(t)
	if err := os.WriteFile(filepath.Join(p.LocalRoot, "hello.go"), []byte("hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	installNavigationFixture(t, s, sftp.InMemHandler())
	if _, err := s.ListRemote(context.Background(), "."); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	client := s.navigation.session.Client
	s.mu.Unlock()
	f, err := client.Create("/remote/hello.go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	before := s.Status().Settings
	remote, err := s.ReadRemoteFile(context.Background(), before.ActiveProjectID, before.ActiveConnectionID, "hello.go")
	if err != nil || remote.Text != "hello\n" || remote.Server != "Default" {
		t.Fatalf("remote=%+v err=%v", remote, err)
	}
	comparison, err := s.CompareFile(context.Background(), before.ActiveProjectID, before.ActiveConnectionID, "hello.go")
	if err != nil || !comparison.Equal || !comparison.Comparable {
		t.Fatalf("comparison=%+v err=%v", comparison, err)
	}
	after := s.Status().Settings
	if after.ActiveConnectionID != before.ActiveConnectionID || after.ActiveMappingID != before.ActiveMappingID {
		t.Fatal("comparison changed selection")
	}
	for _, name := range []string{"../hello.go", "/remote/hello.go", "folder"} {
		if _, err := s.ReadRemoteFile(context.Background(), before.ActiveProjectID, before.ActiveConnectionID, name); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	// A deployment subdirectory maps independently of the active mapping.
	if _, err := s.ListRemote(context.Background(), "."); err != nil {
		t.Fatal(err)
	}
	project := before.Projects[0]
	project.Connections[0].Mappings[0].DeploymentPath = "folder"
	if _, err := s.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	client = s.navigation.session.Client
	s.mu.Unlock()
	f, err = client.Create("/remote/folder/hello.go")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write([]byte("different\n"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	comparison, err = s.CompareFile(context.Background(), project.ID, project.Connections[0].ID, "hello.go")
	if err != nil || comparison.Equal || comparison.Remote.RelativePath != "folder/hello.go" {
		t.Fatalf("mapping comparison=%+v err=%v", comparison, err)
	}
}
