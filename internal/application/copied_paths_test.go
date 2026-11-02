package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/transport"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func copiedPathsFixture(t *testing.T) (*Service, config.Project) {
	t.Helper()
	s, _ := workspaceService(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	connection := func(id, serverRoot, local, deploy string) config.ProjectConnection {
		return config.ProjectConnection{ID: id, Name: id, RootPath: serverRoot, Connection: transport.Connection{Mode: "manual", Host: "example.invalid", User: "tester", Port: 22}, Mappings: []config.Mapping{{ID: id + "-mapping", LocalPath: local, DeploymentPath: deploy}}}
	}
	status, err := s.SaveProject(config.Project{Name: "Copy fixture", LocalRoot: root, Connections: []config.ProjectConnection{connection("source", "/srv/source", "src", "public"), connection("target", "/srv/target", ".", "app")}, DefaultConnectionID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	return s, status.Settings.Projects[0]
}
func TestCopiedPathsLocalRemoteAndTargetMappings(t *testing.T) {
	s, p := copiedPathsFixture(t)
	before, _ := json.Marshal(s.Status().Settings)
	for _, tc := range []struct{ side, source, target, relative, kind, want string }{
		{"local", "", "", "src/hello world.txt", "local", filepath.Join(p.LocalRoot, "src", "hello world.txt")},
		{"local", "", "", "src/hello world.txt", "project", "src/hello world.txt"},
		{"local", "", "", "src/hello world.txt", "server", "/srv/target/app/src/hello world.txt"},
		{"local", "", "source", "src/hello world.txt", "server", "/srv/source/public/hello world.txt"},
		{"local", "", "target", ".", "server", "/srv/target/app"},
		{"remote", "source", "", "public/hello world.txt", "local", filepath.Join(p.LocalRoot, "src", "hello world.txt")},
		{"remote", "source", "", "public/hello world.txt", "project", "src/hello world.txt"},
		{"remote", "source", "", "outside/not-mapped.txt", "server", "/srv/source/outside/not-mapped.txt"},
		{"remote", "source", "target", "public/hello world.txt", "server", "/srv/target/app/src/hello world.txt"},
		{"remote", "source", "", ".", "server", "/srv/source"},
		{"remote", "source", "", "public", "local", filepath.Join(p.LocalRoot, "src")},
		{"local", "", "", ".", "project", "."},
	} {
		t.Run(tc.side+"_"+tc.kind+"_"+tc.relative, func(t *testing.T) {
			actual, err := s.CopiedPaths(p.ID, tc.side, tc.source, tc.target, []string{tc.relative}, tc.kind)
			if err != nil || actual != tc.want {
				t.Fatalf("got %q, %v; want %q", actual, err, tc.want)
			}
		})
	}
	after, _ := json.Marshal(s.Status().Settings)
	if string(before) != string(after) {
		t.Fatal("copying changed active project/server or settings")
	}
}
func TestCopiedPathsMultipleAndFailures(t *testing.T) {
	s, p := copiedPathsFixture(t)
	value, err := s.CopiedPaths(p.ID, "local", "", "", []string{"src/one.txt", "src/two.txt"}, "project")
	if err != nil || value != "src/one.txt\nsrc/two.txt" {
		t.Fatalf("multi: %q %v", value, err)
	}
	for _, tc := range []struct {
		side, source, target, kind string
		paths                      []string
	}{
		{"local", "", "", "project", []string{"src/ok", "../escape"}},
		{"local", "", "", "local", []string{"/absolute"}},
		{"local", "", "", "local", []string{"C:/absolute"}},
		{"local", "", "", "local", []string{"line\nbreak"}},
		{"remote", "source", "", "project", []string{"outside/file"}},
		{"remote", "missing", "", "server", []string{"file"}},
		{"local", "", "missing", "server", []string{"file"}},
		{"local", "", "source", "server", []string{"outside/file"}},
		{"other", "", "", "local", []string{"file"}},
		{"local", "", "", "other", []string{"file"}},
		{"local", "", "", "local", nil},
	} {
		value, err := s.CopiedPaths(p.ID, tc.side, tc.source, tc.target, tc.paths, tc.kind)
		if err == nil || value != "" {
			t.Fatalf("invalid request returned partial paths: %q, %v", value, err)
		}
	}
	if _, err := s.CopiedPaths("missing", "local", "", "", []string{"file"}, "local"); err == nil {
		t.Fatal("unknown project accepted")
	}
}
func TestCopiedPathsLocalOnlyProject(t *testing.T) {
	s, _ := workspaceService(t)
	root := t.TempDir()
	status, err := s.SaveProject(config.Project{Name: "Local", LocalRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	id := status.Settings.ActiveProjectID
	value, err := s.CopiedPaths(id, "local", "", "", []string{".env"}, "local")
	if err != nil || value != filepath.Join(root, ".env") {
		t.Fatal("local copy unnecessarily requires a server or existing file")
	}
	if _, err := s.CopiedPaths(id, "local", "", "", []string{"file"}, "server"); err == nil {
		t.Fatal("missing server accepted")
	}
}
