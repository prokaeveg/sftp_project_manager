package application

import (
	"awesomeProject/internal/transport"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSSHDiscoveryDoesNotChangeProjectsOrRunCommands(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "app.exe"))
	if err != nil {
		t.Fatal(err)
	}
	before := s.Status().Settings
	file := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(file, []byte("Host work\n HostName example.test\nMatch exec \"invalid-command-must-not-run\"\n HostName other.test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := s.DiscoverSSH(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Aliases) != 1 || result.Aliases[0].Name != "work" {
		t.Fatalf("aliases: %+v", result.Aliases)
	}
	if !reflect.DeepEqual(before, s.Status().Settings) {
		t.Fatal("reading aliases changed projects")
	}
	if _, err := s.EvaluateSSH(context.Background(), transport.Connection{Mode: "ssh_alias", Alias: "work", ConfigPath: file}, false); err == nil || !strings.Contains(err.Error(), "доверие") {
		t.Fatalf("untrusted evaluation allowed: %v", err)
	}
	if !reflect.DeepEqual(before, s.Status().Settings) {
		t.Fatal("evaluation changed projects")
	}
}
