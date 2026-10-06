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
