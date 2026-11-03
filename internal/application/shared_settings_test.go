package application

import (
	"awesomeProject/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestApplySharedSettingsPreservesEachSessionSelectionAndDoesNotWrite(t *testing.T) {
	first, firstExe := workspaceService(t)
	second, _ := workspaceService(t)
	projectA := config.Project{ID: "a", Name: "A", LocalRoot: t.TempDir(), Connections: []config.ProjectConnection{{ID: "a-c1", Mappings: []config.Mapping{{ID: "a-m1"}, {ID: "a-m2"}}}, {ID: "a-c2", Mappings: []config.Mapping{{ID: "a-m3"}}}}}
	projectB := config.Project{ID: "b", Name: "B", LocalRoot: t.TempDir(), Connections: []config.ProjectConnection{{ID: "b-c", Mappings: []config.Mapping{{ID: "b-m"}}}}}
	base := config.Settings{SchemaVersion: 7, Projects: []config.Project{projectA, projectB}, ActiveProjectID: "a", ActiveConnectionID: "a-c1", ActiveMappingID: "a-m1"}
	first.status.Settings = cloneSettings(base)
	second.status.Settings = cloneSettings(base)
	first.status.Settings.ActiveMappingID = "a-m2"
	configFlatten(&first.status.Settings)
	second.status.Settings.ActiveProjectID = "b"
	second.status.Settings.ActiveConnectionID = "b-c"
	second.status.Settings.ActiveMappingID = "b-m"
	configFlatten(&second.status.Settings)
	// The incoming shared project edits B. Each session keeps its own selection.
	incoming := cloneSettings(base)
	incoming.Projects[1].Name = "B renamed"
	incoming.Projects[1].Color = "#123456"
	first.store.Save(base)
	second.store.Save(base)
	firstBytes, _ := os.ReadFile(filepath.Join(filepath.Dir(firstExe), "data", "settings.json"))
	first.queuePaused, second.authClosed = true, true
	first.ApplySharedSettings(incoming)
	second.ApplySharedSettings(incoming)
	for _, tc := range []struct {
		service                      *Service
		project, connection, mapping string
	}{{first, "a", "a-c1", "a-m2"}, {second, "b", "b-c", "b-m"}} {
		got := tc.service.Status().Settings
		if got.ActiveProjectID != tc.project || got.ActiveConnectionID != tc.connection || got.ActiveMappingID != tc.mapping {
			t.Fatalf("selection changed: got (%q, %q, %q)", got.ActiveProjectID, got.ActiveConnectionID, got.ActiveMappingID)
		}
		if got.Projects[1].Name != "B renamed" || got.ProjectName != got.Projects[0].Name && tc.project == "a" {
			t.Fatalf("shared project or selected name not reconciled: %#v", got)
		}
	}
	if first.queuePaused != true || second.authClosed != true {
		t.Fatal("session-local state changed")
	}
	after, _ := os.ReadFile(filepath.Join(filepath.Dir(firstExe), "data", "settings.json"))
	if string(firstBytes) != string(after) {
		t.Fatal("ApplySharedSettings wrote settings to disk")
	}
	// Caller-owned arrays must not alias service state.
	incoming.Projects[1].SearchExcludedDirectories = []string{"cache"}
	first.ApplySharedSettings(incoming)
	incoming.Projects[1].SearchExcludedDirectories[0] = "mutated"
	if first.Status().Settings.Projects[1].SearchExcludedDirectories[0] != "cache" {
		t.Fatal("project slices were not deep-copied")
	}
}

func TestApplySharedSettingsKeepsSelectionEmptyWhenActiveProjectRemoved(t *testing.T) {
	service, _ := workspaceService(t)
	removedConnection := workspaceConnection("Old")
	removedConnection.ID = "old-c"
	removedConnection.Mappings[0].ID = "old-m"
	removedConnection.RootPath = "/"
	removedConnection.Mappings[0].DeploymentPath = "."
	keepConnection := workspaceConnection("New")
	keepConnection.ID = "new-c"
	keepConnection.Mappings[0].ID = "new-m"
	keepConnection.RootPath = "/"
	keepConnection.Mappings[0].DeploymentPath = "."
	removed := config.Project{ID: "gone", Name: "Gone", LocalRoot: t.TempDir(), Connections: []config.ProjectConnection{removedConnection}}
	keep := config.Project{ID: "keep", Name: "Keep", LocalRoot: t.TempDir(), Connections: []config.ProjectConnection{keepConnection}}
	service.status.Settings = config.Settings{SchemaVersion: 1, Projects: []config.Project{removed, keep}, ActiveProjectID: "gone", ActiveConnectionID: "old-c", ActiveMappingID: "old-m"}
	service.ApplySharedSettings(config.Settings{SchemaVersion: 8, Projects: []config.Project{keep}})
	got := service.Status().Settings
	if got.SchemaVersion != 8 || got.ActiveProjectID != "" || got.ActiveConnectionID != "" || got.ActiveMappingID != "" || got.ProjectName != "" || got.Profile != nil || len(got.Projects) != 1 || got.Projects[0].ID != "keep" {
		t.Fatalf("unexpected fallback settings: %#v", got)
	}
	service.status.Settings.ActiveProjectID = ""
	service.ApplySharedSettings(config.Settings{SchemaVersion: 9})
	got = service.Status().Settings
	if got.ActiveProjectID != "" || got.ActiveConnectionID != "" || got.ActiveMappingID != "" || got.Profile != nil {
		t.Fatalf("expected empty selection without projects: %#v", got)
	}
}

func TestApplySharedSettingsKeepsBlankSelectionWithProjectsVisible(t *testing.T) {
	service, _ := workspaceService(t)
	project := config.Project{ID: "shared", Name: "Shared", LocalRoot: t.TempDir()}
	service.status.Settings = config.Settings{ActiveProjectID: ""}
	service.ApplySharedSettings(config.Settings{SchemaVersion: 4, Projects: []config.Project{project}})
	got := service.Status().Settings
	if got.ActiveProjectID != "" || got.ActiveConnectionID != "" || got.ActiveMappingID != "" || got.ProjectName != "" || got.Profile != nil || len(got.Projects) != 1 || got.Projects[0].ID != "shared" {
		t.Fatalf("expected visible catalog with blank local selection, got %#v", got)
	}
}

func TestSelectWindowProjectChangesOnlySessionSelection(t *testing.T) {
	service, executable := workspaceService(t)
	first, err := service.SaveProject(config.Project{Name: "First", LocalRoot: t.TempDir(), Connections: []config.ProjectConnection{workspaceConnection("first")}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.SaveProject(config.Project{Name: "Second", LocalRoot: t.TempDir(), Connections: []config.ProjectConnection{workspaceConnection("second")}})
	if err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(filepath.Dir(executable), "data", "settings.json")
	before, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}

	selected, err := service.SelectWindowProject(second.Settings.ActiveProjectID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Settings.ActiveProjectID != second.Settings.ActiveProjectID || selected.Settings.Profile == nil || selected.Settings.Profile.Name != "Second" {
		t.Fatalf("window selection/profile not updated: %#v", selected.Settings)
	}
	after, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("window selection was persisted to the shared settings file")
	}

	invalid, err := service.SelectWindowProject("missing", "missing", "missing")
	if err == nil {
		t.Fatal("expected invalid window selection to fail")
	}
	if invalid.Settings.ActiveProjectID != "" {
		t.Fatal("invalid selection should return an empty status")
	}
	got := service.Status().Settings
	if got.ActiveProjectID != second.Settings.ActiveProjectID || got.Profile == nil || got.Profile.Name != "Second" {
		t.Fatalf("invalid selection changed session state: %#v", got)
	}
	afterInvalid, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(afterInvalid) {
		t.Fatal("invalid window selection changed the shared settings file")
	}
	if first.Settings.ActiveProjectID == second.Settings.ActiveProjectID {
		t.Fatal("fixture projects unexpectedly share an ID")
	}
}
