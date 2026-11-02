package application

import (
	"awesomeProject/internal/config"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ideaFixture(t *testing.T, root string, name string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".idea"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".idea", name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func makeIdeaProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	ideaFixture(t, root, "deployment.xml", `<project><component name="PublishConfigData" serverName="ssh1"><serverData><paths name="ssh1"><serverdata><mappings><mapping local="$PROJECT_DIR$" deploy="/site" /></mappings></serverdata></paths></serverData></component></project>`)
	ideaFixture(t, root, "webServers.xml", `<project><component name="WebServers"><option name="servers"><webServer name="ssh1"><fileTransfer accessType="SFTP" rootFolder="/srv" sshConfigId="config1" password="SECRET-MUST-NOT-LEAK" /></webServer><webServer name="ftp"><fileTransfer accessType="FTP" /></webServer></option></component></project>`)
	ideaFixture(t, root, "sshConfigs.xml", `<project><component name="SshConfigs"><configs><sshConfig id="config1" host="example.invalid" port="2222" username="tester" authType="PASSWORD" password="SECRET-MUST-NOT-LEAK" /></configs></component></project>`)
	return root
}
func TestPhpStormSingleProjectPreviewAndCreate(t *testing.T) {
	service, executable := workspaceService(t)
	root := makeIdeaProject(t)
	before, _ := os.ReadFile(filepath.Join(root, ".idea", "webServers.xml"))
	initial := service.Status()
	preview, err := service.PreviewPhpStormProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Project.Connections) != 1 {
		t.Fatalf("connections: %d", len(preview.Project.Connections))
	}
	c := preview.Project.Connections[0]
	if c.Name != "ssh1" || c.RootPath != "/srv" || c.Connection.Host != "example.invalid" || c.Connection.Port != 2222 || c.Connection.User != "tester" || c.Connection.AuthMode != "password" {
		t.Fatal("SSH parameters lost")
	}
	if len(c.Mappings) != 1 || c.Mappings[0].LocalPath != "." || c.Mappings[0].DeploymentPath != "site" {
		t.Fatal("mapping changed")
	}
	if preview.Project.DefaultConnectionID != c.ID {
		t.Fatal("default server lost")
	}
	encoded, _ := json.Marshal(preview)
	if strings.Contains(string(encoded), "SECRET-MUST-NOT-LEAK") {
		t.Fatal("secret leaked")
	}
	old, _ := json.Marshal(initial.Settings)
	current, _ := json.Marshal(service.Status().Settings)
	if string(old) != string(current) {
		t.Fatal("preview mutated settings")
	}
	status, err := service.CreateImportedProject(preview.Project)
	if err != nil || len(status.Settings.Projects) != 1 {
		t.Fatalf("create: %v", err)
	}
	if _, err = service.CreateImportedProject(preview.Project); err == nil {
		t.Fatal("duplicate root accepted")
	}
	if _, err = service.PreviewPhpStormProject(filepath.Join(root, ".idea")); err == nil {
		t.Fatal("duplicate preview accepted")
	}
	restarted, err := New(executable)
	if err != nil || len(restarted.Status().Settings.Projects) != 1 {
		t.Fatalf("persistence: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(root, ".idea", "webServers.xml"))
	if string(before) != string(after) {
		t.Fatal("source project changed")
	}
}
func TestPhpStormOpenSSHRequiresExplicitSelection(t *testing.T) {
	root := makeIdeaProject(t)
	ideaFixture(t, root, "sshConfigs.xml", `<project><component name="SshConfigs"><configs><sshConfig id="config1" host="ssh1" useOpenSSHConfig="true" authType="PASSWORD" /></configs></component></project>`)
	preview, err := previewPhpStormProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Project.Connections) != 1 || len(preview.AliasConnectionIDs) != 1 {
		t.Fatal("alias confirmation absent")
	}
	c := preview.Project.Connections[0]
	if c.Connection.Mode != "ssh_alias" || c.Connection.AuthMode != "password" || c.Connection.Alias != "ssh1" || c.ID != preview.AliasConnectionIDs[0] {
		t.Fatal("alias conversion incorrect")
	}
}
func TestPhpStormInvalidInputsDoNotWriteSettings(t *testing.T) {
	for _, tc := range []struct{ name, file, xml string }{
		{"malformed", "deployment.xml", `<project><component password="SECRET-MUST-NOT-LEAK"`},
		{"multiple XML roots", "deployment.xml", `<project/><project/>`},
		{"trailing garbage", "deployment.xml", `<project/>SECRET-MUST-NOT-LEAK`},
		{"malformed optional", "sshConfigs.xml", `<broken`},
		{"oversize", "webServers.xml", strings.Repeat("x", 2*1024*1024+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, _ := workspaceService(t)
			root := makeIdeaProject(t)
			ideaFixture(t, root, tc.file, tc.xml)
			if _, err := service.PreviewPhpStormProject(root); err == nil || strings.Contains(err.Error(), "SECRET-MUST-NOT-LEAK") {
				t.Fatalf("expected safe error: %v", err)
			}
			if len(service.Status().Settings.Projects) != 0 {
				t.Fatal("settings mutated")
			}
		})
	}
}
func TestPhpStormUnsafeOrUnavailableServersAreSkipped(t *testing.T) {
	for _, tc := range []struct{ name, file, xml string }{
		{"outside root", "deployment.xml", `<project><component name="PublishConfigData"><serverData><paths name="ssh1"><serverdata><mappings><mapping local="$PROJECT_DIR$/.." deploy="/" /></mappings></serverdata></paths></serverData></component></project>`},
		{"remote traversal", "deployment.xml", `<project><component name="PublishConfigData"><serverData><paths name="ssh1"><serverdata><mappings><mapping local="$PROJECT_DIR$" deploy="/../escape" /></mappings></serverdata></paths></serverData></component></project>`},
		{"missing IDE config", "sshConfigs.xml", `<project/>`},
		{"unknown macro", "deployment.xml", `<project><component name="PublishConfigData"><serverData><paths name="ssh1"><serverdata><mappings><mapping local="$OTHER_DIR$" deploy="/" /></mappings></serverdata></paths></serverData></component></project>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := makeIdeaProject(t)
			ideaFixture(t, root, tc.file, tc.xml)
			preview, err := previewPhpStormProject(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(preview.Project.Connections) != 0 || len(preview.Warnings) == 0 {
				t.Fatal("unsafe server accepted or skipped silently")
			}
		})
	}
}
func TestPhpStormOnlyExplicitProjectAndNoExistingReplacement(t *testing.T) {
	service, _ := workspaceService(t)
	root := makeIdeaProject(t)
	saved, err := service.SaveProject(config.Project{Name: "Existing", LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	existing := saved.Settings.Projects[0]
	preview, err := service.PreviewPhpStormProject(root)
	if err != nil {
		t.Fatal(err)
	}
	preview.Project.Name = "Existing"
	if _, err = service.CreateImportedProject(preview.Project); err == nil {
		t.Fatal("name conflict accepted")
	}
	preview.Project.Name = "Imported"
	preview.Project.ID = existing.ID
	if _, err = service.CreateImportedProject(preview.Project); err == nil {
		t.Fatal("existing project replacement accepted")
	}
	preview.Project.ID = ""
	if _, err = service.CreateImportedProject(preview.Project); err != nil {
		t.Fatal(err)
	}
	if len(service.Status().Settings.Projects) != 2 || service.Status().Settings.Projects[0].Name != "Existing" {
		t.Fatal("existing project changed")
	}
	if _, err = previewPhpStormProject(filepath.Dir(root)); err == nil {
		t.Fatal("parent scanned recursively")
	}
}

// Opt-in read-only compatibility check; never saves a real project or opens SSH.
func TestPhpStormReadOnlySample(t *testing.T) {
	root := os.Getenv("SFTP_PHPSTORM_SAMPLE")
	if root == "" {
		t.Skip("no explicit sample selected")
	}
	before := map[string][]byte{}
	for _, name := range []string{"deployment.xml", "webServers.xml", "sshConfigs.xml"} {
		data, err := os.ReadFile(filepath.Join(root, ".idea", name))
		if err != nil {
			t.Fatal("sample unavailable")
		}
		before[name] = data
	}
	service, _ := workspaceService(t)
	preview, err := service.PreviewPhpStormProject(root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range preview.Project.Connections {
		if c.Name == "ssh1" {
			found = true
			if len(c.Mappings) == 0 {
				t.Fatal("sample mapping missing")
			}
			if err := c.Connection.Validate(); err != nil {
				t.Fatal("invalid sample connection")
			}
		}
	}
	if !found {
		t.Fatal("ssh1 sample was not recognized")
	}
	if len(service.Status().Settings.Projects) != 0 {
		t.Fatal("read-only preview changed settings")
	}
	for name, data := range before {
		after, err := os.ReadFile(filepath.Join(root, ".idea", name))
		if err != nil || string(after) != string(data) {
			t.Fatal("sample changed")
		}
	}
}

func TestPhpStormDirectKeyConnectionAndNestedMappings(t *testing.T) {
	root := makeIdeaProject(t)
	if err := os.Mkdir(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	ideaFixture(t, root, "webServers.xml", `<project><component name="WebServers"><option name="servers"><webServer name="ssh1"><fileTransfer accessType="SFTP" rootFolder="/var/www" host="direct.invalid" username="tester" authType="KEY_PAIR" privateKey="$PROJECT_DIR$/key" /></webServer></option></component></project>`)
	ideaFixture(t, root, "deployment.xml", `<project><component name="PublishConfigData"><serverData><paths name="ssh1"><serverdata><mappings><mapping local="$PROJECT_DIR$/src" deploy="/public" /></mappings><excludedPaths><excludedPath path="/secret" /></excludedPaths></serverdata></paths></serverData></component></project>`)
	preview, err := previewPhpStormProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Project.Connections) != 1 {
		t.Fatal("direct SFTP config not supported")
	}
	c := preview.Project.Connections[0]
	if c.Connection.IdentityFile != filepath.Join(root, "key") || c.Connection.Port != 22 || c.Mappings[0].LocalPath != "src" || c.Mappings[0].DeploymentPath != "public" || c.RootPath != "/var/www" {
		t.Fatal("direct connection or mapping changed")
	}
	if !strings.Contains(strings.Join(preview.Warnings, " "), "исключения") {
		t.Fatal("excluded paths dropped silently")
	}
}
