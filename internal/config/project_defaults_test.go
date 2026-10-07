package config

import (
	"awesomeProject/internal/transport"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func defaultProjectSettings(t *testing.T) Settings {
	t.Helper()
	project := Project{ID: "p", Name: "Project", LocalRoot: t.TempDir(), Connections: []ProjectConnection{
		{ID: "first", Name: "First", Connection: transport.Connection{Mode: "manual", Host: "first.test", User: "me", Port: 22}, Mappings: []Mapping{{ID: "m1", LocalPath: ".", RemotePath: "/first"}}},
		{ID: "second", Name: "Second", Connection: transport.Connection{Mode: "manual", Host: "second.test", User: "me", Port: 22}, Mappings: []Mapping{{ID: "m2", LocalPath: ".", RemotePath: "/second"}}},
	}}
	return Settings{SchemaVersion: SchemaVersion, Projects: []Project{project}, ActiveProjectID: "p", ActiveConnectionID: "second", ActiveMappingID: "m2"}
}

func TestMissingDefaultConnectionMigrationIsReadOnlyAndPreservesActiveServer(t *testing.T) {
	settings := defaultProjectSettings(t)
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path(), data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Projects[0].DefaultConnectionID != "first" || loaded.ActiveConnectionID != "second" || loaded.Profile == nil || loaded.Profile.Connection.Host != "second.test" {
		t.Fatalf("legacy default/active selection: %+v", loaded)
	}
	unchanged, err := os.ReadFile(store.path())
	if err != nil || !bytes.Equal(data, unchanged) {
		t.Fatal("loading migration changed settings.json", err)
	}
	if _, err := os.Stat(filepath.Join(store.dir, "settings.json.bak")); !os.IsNotExist(err) {
		t.Fatalf("read-only migration created a backup: %v", err)
	}
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	if settings.Projects[0].DefaultConnectionID != "" {
		t.Fatal("save mutated the caller's project")
	}
	saved, err := os.ReadFile(store.path())
	if err != nil || !bytes.Contains(saved, []byte("\"defaultConnectionID\": \"first\"")) {
		t.Fatalf("save did not persist default: %s, %v", saved, err)
	}
}

func TestLocalOnlySettingsSelectionAndDefaultValidation(t *testing.T) {
	local := Settings{SchemaVersion: SchemaVersion, Projects: []Project{{ID: "local", Name: "Local", LocalRoot: t.TempDir(), Connections: []ProjectConnection{}}}, ActiveProjectID: "local"}
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(local); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.Profile != nil || loaded.ActiveConnectionID != "" || loaded.ActiveMappingID != "" {
		t.Fatalf("local-only load: %+v, %v", loaded, err)
	}
	for _, selection := range []struct{ project, connection, mapping string }{{"", "", ""}, {"local", "unknown", ""}, {"local", "", "unknown"}, {"local", "unknown", "unknown"}, {"missing", "", ""}} {
		invalid := local
		invalid.ActiveProjectID, invalid.ActiveConnectionID, invalid.ActiveMappingID = selection.project, selection.connection, selection.mapping
		if err := store.Save(invalid); err == nil {
			t.Fatalf("invalid local-only refs accepted: %+v", selection)
		}
	}
	for _, settings := range []Settings{local, defaultProjectSettings(t)} {
		settings.Projects[0].DefaultConnectionID = "unknown"
		data, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decode(data); err == nil {
			t.Fatal("unknown stored default connection accepted")
		}
	}
	withServers := defaultProjectSettings(t)
	withServers.ActiveConnectionID, withServers.ActiveMappingID = "", ""
	if err := validateSettings(withServers); err == nil {
		t.Fatal("project with servers accepted incomplete active references")
	}
}

func TestSchemaTwoMappingMigrationInfersRootWithoutWriting(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	legacy := defaultProjectSettings(t)
	legacy.SchemaVersion = 2
	legacy.Projects[0].Connections = []ProjectConnection{{ID: "c", Name: "Server", Connection: transport.Connection{Mode: "manual", Host: "example.test", User: "me", Port: 22}, Mappings: []Mapping{{ID: "src", LocalPath: "src", RemotePath: "/srv/app/src"}, {ID: "assets", LocalPath: "assets", RemotePath: "/srv/app/public/assets"}}}}
	legacy.ActiveConnectionID, legacy.ActiveMappingID = "c", "src"
	original, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path(), original, 0600); err != nil {
		t.Fatal(err)
	}
	settings, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	connection := settings.Projects[0].Connections[0]
	if settings.SchemaVersion != SchemaVersion || connection.RootPath != "/srv/app" || connection.Mappings[0].DeploymentPath != "src" || connection.Mappings[1].DeploymentPath != "public/assets" || connection.Mappings[0].RemotePath != "" || settings.Profile.RemoteRoot != "/srv/app/src" {
		t.Fatalf("unexpected migrated mapping: %+v", settings)
	}
	got, err := os.ReadFile(store.path())
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("loading migration rewrote schema-2 settings", err)
	}
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(filepath.Join(store.dir, "settings.json.v2.bak"))
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatal("schema-2 backup did not preserve original settings", err)
	}
	saved, err := os.ReadFile(store.path())
	if err != nil || bytes.Contains(saved, []byte(`"remotePath"`)) || !bytes.Contains(saved, []byte(`"rootPath": "/srv/app"`)) {
		t.Fatalf("save did not write canonical root and deployment mapping: %s, %v", saved, err)
	}
}

func TestDeploymentPathsRejectEscapesAndAbsolutePaths(t *testing.T) {
	for _, candidate := range []string{"../outside", "a/../../outside", "/absolute", `a\\b`, "a\nfile"} {
		if _, err := ResolveDeploymentPath("/var/www/html", candidate); err == nil {
			t.Fatalf("accepted unsafe deployment path %q", candidate)
		}
	}
	if got, err := ResolveDeploymentPath("/var/www/html", "public"); err != nil || got != "/var/www/html/public" {
		t.Fatalf("resolve deployment: %q, %v", got, err)
	}
}
