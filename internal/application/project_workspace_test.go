package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/transport"
	"os"
	"path/filepath"
	"testing"
)

func workspaceService(t *testing.T) (*Service, string) {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "app.exe")
	service, err := New(executable)
	if err != nil {
		t.Fatal(err)
	}
	service.status.SSHPath = "missing-ssh-executable.exe"
	return service, executable
}

func workspaceConnection(name string) config.ProjectConnection {
	return config.ProjectConnection{Name: name, Connection: transport.Connection{Mode: "manual", Host: name + ".test", User: "me", Port: 22}, Mappings: []config.Mapping{{LocalPath: ".", RemotePath: "/" + name}}}
}

func TestLocalOnlyProjectSaveRestartSelectAndList(t *testing.T) {
	service, executable := workspaceService(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	status, err := service.SaveProject(config.Project{Name: "Local project", LocalRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	project := status.Settings.Projects[0]
	if project.ID == "" || project.DefaultConnectionID != "" || status.Settings.ActiveProjectID != project.ID || status.Settings.ActiveConnectionID != "" || status.Settings.ActiveMappingID != "" || status.Settings.Profile != nil {
		t.Fatalf("local-only state: %+v", status.Settings)
	}
	entries, err := service.ListProjectLocal(".")
	if err != nil || len(entries) != 2 || entries[0].Name != "folder" || !entries[0].IsDir || entries[1].Size != 3 || entries[1].Excluded {
		t.Fatalf("local-only entries: %+v, %v", entries, err)
	}
	if _, err := service.ListProjectLocal("../escape"); err == nil {
		t.Fatal("project listing accepted traversal")
	}
	if _, err := service.ListLocal("."); err == nil {
		t.Fatal("mapping listing succeeded without server")
	}
	restarted, err := New(executable)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Status().Settings.ActiveProjectID != project.ID || restarted.Status().Settings.Profile != nil {
		t.Fatal("local-only selection not restored")
	}
	if _, err := restarted.SelectProject(project.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.SelectProject(project.ID, "missing", ""); err == nil {
		t.Fatal("local-only project accepted a connection reference")
	}
	if _, err := restarted.ListProjectLocal("folder"); err != nil {
		t.Fatal(err)
	}
}

func TestProjectDefaultServerSelectionAndRemoval(t *testing.T) {
	service, executable := workspaceService(t)
	status, err := service.SaveProject(config.Project{Name: "Workspace", LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	project := status.Settings.Projects[0]
	project.Connections = []config.ProjectConnection{workspaceConnection("first")}
	status, err = service.SaveProject(project)
	if err != nil {
		t.Fatal(err)
	}
	project = status.Settings.Projects[0]
	first := project.Connections[0].ID
	if project.DefaultConnectionID != first || status.Settings.ActiveConnectionID != first {
		t.Fatal("first server was not selected as default")
	}
	project.Connections = append(project.Connections, workspaceConnection("second"))
	status, err = service.SaveProject(project)
	if err != nil {
		t.Fatal(err)
	}
	project = status.Settings.Projects[0]
	second := project.Connections[1].ID
	project.DefaultConnectionID = second
	status, err = service.SaveProject(project)
	if err != nil {
		t.Fatal(err)
	}
	project = status.Settings.Projects[0]
	if status.Settings.ActiveConnectionID != first || project.DefaultConnectionID != second {
		t.Fatal("saving default changed current selection")
	}
	restarted, err := New(executable)
	if err != nil || restarted.Status().Settings.ActiveConnectionID != first || restarted.Status().Settings.Projects[0].DefaultConnectionID != second {
		t.Fatalf("restart default/selection: %v", err)
	}
	status, err = service.SelectProject(project.ID, "", "")
	if err != nil || status.Settings.ActiveConnectionID != second || status.Settings.ActiveMappingID != project.Connections[1].Mappings[0].ID {
		t.Fatalf("select default: %+v, %v", status.Settings, err)
	}
	status, err = service.SelectProject(project.ID, first, "")
	if err != nil || status.Settings.ActiveConnectionID != first {
		t.Fatal("explicit selection failed", err)
	}
	bad := cloneProject(project)
	bad.DefaultConnectionID = "unknown"
	if _, err := service.SaveProject(bad); err == nil || service.Status().Settings.ActiveConnectionID != first {
		t.Fatal("unknown default saved or mutated state")
	}
	project.Connections = project.Connections[1:]
	status, err = service.SaveProject(project)
	if err != nil || status.Settings.ActiveConnectionID != second {
		t.Fatal("removed selected server did not choose default", err)
	}
	project = status.Settings.Projects[0]
	project.Connections = append(project.Connections, workspaceConnection("third"))
	status, err = service.SaveProject(project)
	if err != nil {
		t.Fatal(err)
	}
	project = status.Settings.Projects[0]
	third := project.Connections[1].ID
	project.Connections = project.Connections[1:]
	status, err = service.SaveProject(project)
	if err != nil || status.Settings.ActiveConnectionID != third || status.Settings.Projects[0].DefaultConnectionID != third {
		t.Fatal("removed default did not select remaining server", err)
	}
	project = status.Settings.Projects[0]
	project.Connections = nil
	status, err = service.SaveProject(project)
	if err != nil || status.Settings.ActiveConnectionID != "" || status.Settings.ActiveMappingID != "" || status.Settings.Profile != nil || status.Settings.Projects[0].DefaultConnectionID != "" {
		t.Fatalf("removing last server: %+v, %v", status.Settings, err)
	}
	restarted, err = New(executable)
	if err != nil || restarted.Status().Settings.Profile != nil || restarted.Status().Settings.ActiveProjectID != project.ID {
		t.Fatal("local-only removal state not persisted", err)
	}
}

func TestProjectLocalTreeUsesWholeProjectAndSelectedConnectionRules(t *testing.T) {
	service, _ := workspaceService(t)
	root := t.TempDir()
	for _, directory := range []string{"src", "docs", "private"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "src", "debug.log"), []byte("debug"), 0600); err != nil {
		t.Fatal(err)
	}
	connection := workspaceConnection("first")
	connection.Mappings = []config.Mapping{{LocalPath: "src", RemotePath: "/src"}, {LocalPath: "docs", RemotePath: "/docs"}}
	connection.Excludes = []string{"src/*.log", "private/**"}
	status, err := service.SaveProject(config.Project{Name: "Whole tree", LocalRoot: root, Connections: []config.ProjectConnection{connection}})
	if err != nil {
		t.Fatal(err)
	}
	project := status.Settings.Projects[0]
	entries, err := service.ListProjectLocal(".")
	if err != nil || len(entries) != 3 {
		t.Fatalf("whole project entries: %+v, %v", entries, err)
	}
	if entries[1].Name != "private" || !entries[1].Excluded {
		t.Fatalf("project exclusion: %+v", entries)
	}
	srcEntries, err := service.ListProjectLocal("src")
	if err != nil || len(srcEntries) != 1 || srcEntries[0].RelativePath != "src/debug.log" || !srcEntries[0].Excluded {
		t.Fatalf("project-relative listing: %+v, %v", srcEntries, err)
	}
	mappingEntries, err := service.ListLocal(".")
	if err != nil || len(mappingEntries) != 1 || mappingEntries[0].RelativePath != "debug.log" || !mappingEntries[0].Excluded {
		t.Fatalf("mapping-relative listing: %+v, %v", mappingEntries, err)
	}
	if _, err := service.ListProjectLocal("private"); err == nil {
		t.Fatal("excluded project directory opened")
	}
	preview, err := service.PreviewTransfer("upload", "keep.txt")
	if err != nil {
		t.Fatal(err)
	}
	localOnly, err := service.SaveProject(config.Project{Name: "Other local", LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SelectProject(project.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SelectProject(localOnly.Settings.ActiveProjectID, "", ""); err != nil {
		t.Fatal(err)
	}
	if service.pending == nil || service.pending.preview != preview || service.pending.profile.LocalRoot != filepath.Join(root, "src") || service.Status().Settings.Profile != nil {
		t.Fatal("local-only selection changed frozen transfer")
	}
	if entries, err := service.ListProjectLocal("."); err != nil || len(entries) != 0 {
		t.Fatal("local-only project retained old file tree", err)
	}
}

func TestSaveProjectPreservesMappingAndFallsBackAfterMappingRemoval(t *testing.T) {
	service, _ := workspaceService(t)
	root := t.TempDir()
	for _, directory := range []string{"src", "docs"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	connection := workspaceConnection("server")
	connection.Mappings = []config.Mapping{{LocalPath: "src", RemotePath: "/src"}, {LocalPath: "docs", RemotePath: "/docs"}}
	status, err := service.SaveProject(config.Project{Name: "Mappings", LocalRoot: root, Connections: []config.ProjectConnection{connection}})
	if err != nil {
		t.Fatal(err)
	}
	project := status.Settings.Projects[0]
	selectedMapping := project.Connections[0].Mappings[1].ID
	if _, err := service.SelectProject(project.ID, project.Connections[0].ID, selectedMapping); err != nil {
		t.Fatal(err)
	}
	project.Name = "Renamed"
	status, err = service.SaveProject(project)
	if err != nil || status.Settings.ActiveMappingID != selectedMapping {
		t.Fatal("saving reset selected mapping", err)
	}
	project.Connections[0].Mappings = project.Connections[0].Mappings[:1]
	status, err = service.SaveProject(project)
	if err != nil || status.Settings.ActiveMappingID != project.Connections[0].Mappings[0].ID {
		t.Fatal("removed mapping did not fall back", err)
	}
	if status.Settings.Profile.LocalRoot != filepath.Join(root, "src") {
		t.Fatal("fallback mapping profile incorrect")
	}
}

func TestServerWithoutMappingsCanBeSelectedAndLaterMapped(t *testing.T) {
	service, executable := workspaceService(t)
	root := t.TempDir()
	connection := workspaceConnection("unmapped")
	connection.RootPath = "/var/www/html"
	connection.Mappings = nil
	status, err := service.SaveProject(config.Project{Name: "Unmapped", LocalRoot: root, Connections: []config.ProjectConnection{connection}})
	if err != nil {
		t.Fatal(err)
	}
	project := status.Settings.Projects[0]
	serverID := project.Connections[0].ID
	if status.Settings.ActiveConnectionID != serverID || status.Settings.ActiveMappingID != "" || status.Settings.Profile != nil {
		t.Fatalf("unmapped server selection: %+v", status.Settings)
	}
	restarted, err := New(executable)
	if err != nil {
		t.Fatal(err)
	}
	status, err = restarted.SelectProject(project.ID, serverID, "")
	if err != nil || status.Settings.ActiveConnectionID != serverID || status.Settings.ActiveMappingID != "" || status.Settings.Profile != nil {
		t.Fatalf("select unmapped server after restart: %+v, %v", status.Settings, err)
	}
	project = status.Settings.Projects[0]
	project.Connections[0].Mappings = []config.Mapping{{LocalPath: ".", DeploymentPath: "."}}
	status, err = restarted.SaveProject(project)
	if err != nil || status.Settings.ActiveMappingID == "" || status.Settings.Profile == nil || status.Settings.Profile.RemoteRoot != "/var/www/html" {
		t.Fatalf("adding first mapping: %+v, %v", status.Settings, err)
	}
	project = status.Settings.Projects[0]
	project.Connections[0].Mappings = nil
	status, err = restarted.SaveProject(project)
	if err != nil || status.Settings.ActiveConnectionID != serverID || status.Settings.ActiveMappingID != "" || status.Settings.Profile != nil {
		t.Fatalf("removing last mapping: %+v, %v", status.Settings, err)
	}
	project = status.Settings.Projects[0]
	mapped := workspaceConnection("mapped")
	mapped.RootPath = "/srv/app"
	mapped.Mappings = []config.Mapping{{LocalPath: ".", DeploymentPath: "."}}
	unmapped := workspaceConnection("another-unmapped")
	unmapped.RootPath = "/srv/other"
	unmapped.Mappings = nil
	project.Connections = append(project.Connections, mapped, unmapped)
	status, err = restarted.SaveProject(project)
	if err != nil {
		t.Fatal(err)
	}
	project = status.Settings.Projects[0]
	mappedID, unmappedID := project.Connections[1].ID, project.Connections[2].ID
	status, err = restarted.SelectProject(project.ID, mappedID, "")
	if err != nil || status.Settings.Profile == nil || status.Settings.Profile.RemoteRoot != "/srv/app" {
		t.Fatalf("switch to mapped server: %+v, %v", status.Settings, err)
	}
	status, err = restarted.SelectProject(project.ID, unmappedID, "")
	if err != nil || status.Settings.ActiveConnectionID != unmappedID || status.Settings.ActiveMappingID != "" || status.Settings.Profile != nil {
		t.Fatalf("switch to unmapped server: %+v, %v", status.Settings, err)
	}
}

func TestLegacyProfileBridgeCanRestoreServerAfterLastConnectionRemoval(t *testing.T) {
	service, _ := workspaceService(t)
	profile := sampleProfile(t)
	if _, err := service.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	project := service.Status().Settings.Projects[0]
	project.Connections = nil
	if _, err := service.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	status, err := service.SaveProfile(profile)
	if err != nil || status.Settings.Profile == nil || status.Settings.Projects[0].DefaultConnectionID != "legacy-connection" {
		t.Fatalf("restoring legacy server after local-only edit: %+v, %v", status.Settings, err)
	}
}
