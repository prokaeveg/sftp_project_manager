package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/mapping"
	"awesomeProject/internal/transport"
	"context"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pkg/sftp"
)

// The request server has a case-sensitive POSIX filesystem on Windows too,
// so remote names and symlinks can be exercised without local admin rights.
func batchFixture(t *testing.T) (config.Profile, *sftp.Client) {
	return batchFixtureWithHandlers(t, sftp.InMemHandler())
}

func batchFixtureWithHandlers(t *testing.T, handlers sftp.Handlers) (config.Profile, *sftp.Client) {
	t.Helper()
	left, right := net.Pipe()
	server := sftp.NewRequestServer(right, handlers)
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve() }()
	client, err := sftp.NewClientPipe(left, left)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		<-done
	})
	if err := client.Mkdir("/remote"); err != nil {
		t.Fatal(err)
	}
	return config.Profile{Name: "Batch", LocalRoot: t.TempDir(), RemoteRoot: "/remote", Connection: transport.Connection{Mode: "manual", Host: "localhost", User: "test", Port: 22}}, client
}

type cancellingBatchWriter struct {
	sftp.FileWriter
	cancel context.CancelFunc
}

func (writer cancellingBatchWriter) Filewrite(request *sftp.Request) (io.WriterAt, error) {
	file, err := writer.FileWriter.Filewrite(request)
	if err == nil && strings.Contains(request.Filepath, ".transfer-") {
		writer.cancel()
	}
	return file, err
}

type denyBatchCleanup struct{ sftp.FileCmder }

func (handler denyBatchCleanup) Filecmd(request *sftp.Request) error {
	if request.Method == "Remove" && strings.Contains(request.Filepath, ".transfer-") {
		return os.ErrPermission
	}
	return handler.FileCmder.Filecmd(request)
}

type cancelAfterBatchCommit struct {
	sftp.FileCmder
	cancel context.CancelFunc
}

func (handler cancelAfterBatchCommit) Filecmd(request *sftp.Request) error {
	err := handler.FileCmder.Filecmd(request)
	if err == nil && request.Method == "Rename" && strings.Contains(request.Filepath, ".transfer-") {
		handler.cancel()
	}
	return err
}

func TestBatchCancellationAfterCommitKeepsCompletedStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handlers := sftp.InMemHandler()
	handlers.FileCmd = cancelAfterBatchCommit{FileCmder: handlers.FileCmd, cancel: cancel}
	profile, client := batchFixtureWithHandlers(t, handlers)
	batchLocalFile(t, profile, "a.txt", "committed payload")
	batchLocalFile(t, profile, "b.txt", "remaining payload")
	plan, err := buildBatch(context.Background(), profile, "upload", []string{"a.txt", "b.txt"}, client)
	if err != nil {
		t.Fatal(err)
	}
	result := executeBatch(ctx, profile, plan, "skip", client)
	if ctx.Err() == nil || len(result.Items) != 2 || result.Items[0].Status != "completed" || result.Items[1].Status != "cancelled" {
		t.Fatalf("cancellation after commit: %+v, context error: %v", result, ctx.Err())
	}
	file, err := client.Open("/remote/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil || string(content) != "committed payload" {
		t.Fatalf("committed destination content: %q, %v", content, err)
	}
	if _, err := client.Lstat("/remote/b.txt"); !os.IsNotExist(err) {
		t.Fatalf("remaining cancelled destination was installed: %v", err)
	}
}

func TestBatchMidTransferCancellationRetainsCleanupFailureAndRemainingItems(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handlers := sftp.InMemHandler()
	handlers.FilePut = cancellingBatchWriter{FileWriter: handlers.FilePut, cancel: cancel}
	handlers.FileCmd = denyBatchCleanup{FileCmder: handlers.FileCmd}
	profile, client := batchFixtureWithHandlers(t, handlers)
	batchLocalFile(t, profile, "a.txt", "first payload")
	batchLocalFile(t, profile, "b.txt", "second payload")
	plan, err := buildBatch(context.Background(), profile, "upload", []string{"a.txt", "b.txt"}, client)
	if err != nil {
		t.Fatal(err)
	}
	result := executeBatch(ctx, profile, plan, "skip", client)
	if len(result.Items) != 2 || result.Items[0].Status != "cancelled" || result.Items[1].Status != "cancelled" {
		t.Fatalf("mid-transfer cancellation: %+v", result)
	}
	if !strings.Contains(result.Items[0].Error, "remove remote temporary file") || !strings.Contains(result.Items[0].Error, ".transfer-") {
		t.Fatalf("cleanup error/path disappeared: %+v", result)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := client.Lstat("/remote/" + name); !os.IsNotExist(err) {
			t.Fatalf("cancelled destination installed: %s, %v", name, err)
		}
	}
}

func TestBatchBoundsSelectionAndRecursiveDepth(t *testing.T) {
	profile, client := batchFixture(t)
	mapper, err := mapping.New(profile.LocalRoot, profile.RemoteRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeBatchSelection(mapper, make([]string, maxBatchItems+1)); err == nil {
		t.Fatal("unbounded selection accepted")
	}
	deepPath := "folder/" + strings.Repeat("d/", maxBatchDepth)
	if err := client.MkdirAll("/remote/" + deepPath); err != nil {
		t.Fatal(err)
	}
	if _, err := buildBatch(context.Background(), profile, "download", []string{"folder"}, client); err == nil || !strings.Contains(err.Error(), "глубина") {
		t.Fatalf("recursive depth was unbounded: %v", err)
	}
	if entries, err := os.ReadDir(profile.LocalRoot); err != nil || len(entries) != 0 {
		t.Fatalf("failed preview wrote local paths: %v, %v", entries, err)
	}
}

func batchLocalFile(t *testing.T, profile config.Profile, relative, content string) {
	t.Helper()
	name := filepath.Join(profile.LocalRoot, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func batchRemoteFile(t *testing.T, client *sftp.Client, relative, content string) {
	t.Helper()
	name := path.Join("/remote", relative)
	if err := client.MkdirAll(path.Dir(name)); err != nil {
		t.Fatal(err)
	}
	file, err := client.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func batchStatuses(result BatchResult) map[string]string {
	out := map[string]string{}
	for _, item := range result.Items {
		out[item.RelativePath] = item.Status
	}
	return out
}

func TestBatchRecursiveUploadPreservesEmptyDirectoriesAndSkipsExclusions(t *testing.T) {
	profile, client := batchFixture(t)
	profile.Excludes = []string{"**/.env", "tree/ignored/**"}
	batchLocalFile(t, profile, "tree/a.txt", "abc")
	batchLocalFile(t, profile, "tree/nested/b.bin", "\x00\xffdata")
	batchLocalFile(t, profile, "tree/.env", "SECRET")
	batchLocalFile(t, profile, "tree/ignored/hidden.txt", "hidden")
	if err := os.Mkdir(filepath.Join(profile.LocalRoot, "tree", "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	mapper, err := mapping.New(profile.LocalRoot, profile.RemoteRoot)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := normalizeBatchSelection(mapper, []string{"tree/nested/b.bin", "tree", "tree", "tree/a.txt"})
	if err != nil || !reflect.DeepEqual(selected, []string{"tree"}) {
		t.Fatalf("selection = %v, %v", selected, err)
	}
	plan, err := buildBatch(context.Background(), profile, "upload", selected, client)
	if err != nil {
		t.Fatal(err)
	}
	if plan.preview.FileCount != 2 || plan.preview.TotalBytes != 9 || len(plan.items) != 7 {
		t.Fatalf("preview: %+v", plan.preview)
	}
	if _, err := client.Lstat("/remote/tree"); !os.IsNotExist(err) {
		t.Fatalf("preview wrote destination: %v", err)
	}
	result := executeBatch(context.Background(), profile, plan, "skip", client)
	statuses := batchStatuses(result)
	if statuses["tree/.env"] != "skipped" || statuses["tree/ignored"] != "skipped" || statuses["tree/ignored/hidden.txt"] != "" {
		t.Fatalf("exclusions: %+v", result)
	}
	for _, relative := range []string{"tree", "tree/a.txt", "tree/empty", "tree/nested", "tree/nested/b.bin"} {
		if statuses[relative] != "completed" {
			t.Fatalf("%s: %+v", relative, result)
		}
	}
	file, err := client.Open("/remote/tree/nested/b.bin")
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil || string(bytes) != "\x00\xffdata" {
		t.Fatalf("binary transfer: %q, %v", bytes, err)
	}
	info, err := client.Lstat("/remote/tree/empty")
	if err != nil || !info.IsDir() {
		t.Fatalf("empty directory: %v", err)
	}
}

func TestBatchNestedSelectionPlansParentsAndDownloadPolicies(t *testing.T) {
	profile, client := batchFixture(t)
	batchRemoteFile(t, client, "a/b/data.txt", "new data")
	plan, err := buildBatch(context.Background(), profile, "download", []string{"a/b/data.txt"}, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.items) != 3 || plan.items[0].item.RelativePath != "a" || plan.items[1].item.RelativePath != "a/b" {
		t.Fatalf("parents omitted: %+v", plan.preview)
	}
	result := executeBatch(context.Background(), profile, plan, "skip", client)
	for _, item := range result.Items {
		if item.Status != "completed" {
			t.Fatalf("download: %+v", result)
		}
	}
	batchLocalFile(t, profile, "a/b/data.txt", "old")
	plan, err = buildBatch(context.Background(), profile, "download", []string{"a/b/data.txt"}, client)
	if err != nil {
		t.Fatal(err)
	}
	result = executeBatch(context.Background(), profile, plan, "skip", client)
	if result.Items[0].Status != "skipped" {
		t.Fatalf("skip result: %+v", result)
	}
	contents, _ := os.ReadFile(filepath.Join(profile.LocalRoot, "a", "b", "data.txt"))
	if string(contents) != "old" {
		t.Fatal("skip replaced the destination")
	}
	result = executeBatch(context.Background(), profile, plan, "overwrite", client)
	if result.Items[0].Status != "completed" {
		t.Fatalf("overwrite: %+v", result)
	}
	contents, _ = os.ReadFile(filepath.Join(profile.LocalRoot, "a", "b", "data.txt"))
	if string(contents) != "new data" {
		t.Fatal("overwrite did not download source")
	}
}

func TestBatchRejectsChangedSourcesAndTargetsButContinuesOtherFiles(t *testing.T) {
	for _, change := range []string{"source", "appeared target", "changed target"} {
		t.Run(change, func(t *testing.T) {
			profile, client := batchFixture(t)
			batchRemoteFile(t, client, "a.txt", "original")
			batchRemoteFile(t, client, "b.txt", "safe")
			if change == "changed target" {
				batchLocalFile(t, profile, "a.txt", "existing")
			}
			plan, err := buildBatch(context.Background(), profile, "download", []string{"a.txt", "b.txt"}, client)
			if err != nil {
				t.Fatal(err)
			}
			if change == "source" {
				batchRemoteFile(t, client, "a.txt", "source changed in size")
			} else {
				batchLocalFile(t, profile, "a.txt", "must preserve")
			}
			result := executeBatch(context.Background(), profile, plan, "overwrite", client)
			statuses := batchStatuses(result)
			if statuses["a.txt"] != "failed" || statuses["b.txt"] != "completed" {
				t.Fatalf("partial result: %+v", result)
			}
			if change != "source" {
				contents, _ := os.ReadFile(filepath.Join(profile.LocalRoot, "a.txt"))
				if string(contents) != "must preserve" {
					t.Fatal("changed destination was overwritten")
				}
			}
		})
	}
}

func TestBatchRejectsInvalidWindowsNamesCaseCollisionsAndTypeConflicts(t *testing.T) {
	for _, names := range [][]string{{"new/foo", "new/Foo"}, {"σ/a", "ς/b"}, {"NUL.txt"}, {"trailing."}} {
		t.Run(strings.Join(names, "+"), func(t *testing.T) {
			profile, client := batchFixture(t)
			for _, name := range names {
				batchRemoteFile(t, client, name, "data")
			}
			if _, err := buildBatch(context.Background(), profile, "download", names, client); err == nil {
				t.Fatal("unsafe remote names accepted")
			}
		})
	}
	profile, client := batchFixture(t)
	batchRemoteFile(t, client, "folder/file.txt", "data")
	batchLocalFile(t, profile, "folder", "obstructs directory")
	if _, err := buildBatch(context.Background(), profile, "download", []string{"folder"}, client); err == nil {
		t.Fatal("file/directory conflict accepted")
	}
	mapper, _ := mapping.New(profile.LocalRoot, profile.RemoteRoot)
	for _, relative := range []string{"../escape", "a/../escape", "/absolute", "a\\b", "a\x00b", ""} {
		if _, err := normalizeBatchSelection(mapper, []string{relative}); err == nil {
			t.Fatalf("unsafe selection accepted: %q", relative)
		}
	}
}

func TestBatchSkipsLinksWithoutTraversalAndRejectsLinkAncestors(t *testing.T) {
	profile, client := batchFixture(t)
	batchRemoteFile(t, client, "folder/good.txt", "data")
	batchRemoteFile(t, client, "outside/secret", "secret")
	if err := client.Symlink("/remote/outside", "/remote/folder/link"); err != nil {
		t.Fatal(err)
	}
	plan, err := buildBatch(context.Background(), profile, "download", []string{"folder"}, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.items) != 3 || plan.items[2].item.SkippedReason == "" {
		t.Fatalf("link was followed: %+v", plan.preview)
	}
	result := executeBatch(context.Background(), profile, plan, "skip", client)
	if batchStatuses(result)["folder/link"] != "skipped" {
		t.Fatalf("link result: %+v", result)
	}
	if _, err := buildBatch(context.Background(), profile, "download", []string{"folder/link/secret"}, client); err == nil {
		t.Fatal("symbolic link ancestor accepted")
	}
}

func TestBatchCancellationAndFailedDirectoryBlockDescendants(t *testing.T) {
	profile, client := batchFixture(t)
	batchRemoteFile(t, client, "folder/child.txt", "data")
	batchRemoteFile(t, client, "other.txt", "other")
	plan, err := buildBatch(context.Background(), profile, "download", []string{"folder", "other.txt"}, client)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := executeBatch(ctx, profile, plan, "skip", client)
	for _, item := range result.Items {
		if item.Status != "cancelled" {
			t.Fatalf("cancelled result: %+v", result)
		}
	}
	batchLocalFile(t, profile, "folder", "new obstruction")
	result = executeBatch(context.Background(), profile, plan, "skip", client)
	statuses := batchStatuses(result)
	if statuses["folder"] != "failed" || statuses["folder/child.txt"] != "failed" || statuses["other.txt"] != "completed" {
		t.Fatalf("directory dependency result: %+v", result)
	}
}

func TestBatchTokenPolicyAndEnvironmentConsent(t *testing.T) {
	profile, client := batchFixture(t)
	batchLocalFile(t, profile, ".env.production", "secret")
	plan, err := buildBatch(context.Background(), profile, "upload", []string{".env.production"}, client)
	if err != nil {
		t.Fatal(err)
	}
	plan.preview.Token = "token"
	service, err := New(filepath.Join(t.TempDir(), "app.exe"))
	if err != nil {
		t.Fatal(err)
	}
	service.pending = &transferPlan{profile: profile, batch: plan}
	service.status.SSHPath = "missing-ssh-executable.exe"
	for _, test := range []struct {
		token, policy string
		includeEnv    bool
	}{{"wrong-token", "skip", true}, {"token", "invalid-policy", true}, {"token", "skip", false}} {
		if _, err := service.ExecuteBatch(context.Background(), test.token, test.policy, test.includeEnv); err == nil {
			t.Fatal("invalid batch execution accepted")
		}
		if service.pending == nil {
			t.Fatal("a rejected guard consumed the token")
		}
	}
	if err := service.ExecuteTransfer(context.Background(), "", true); err == nil || service.pending == nil {
		t.Fatal("single-file API accepted a batch token")
	}
	if _, err := service.ExecuteBatch(context.Background(), "token", "", true); err == nil {
		t.Fatal("missing OpenSSH executable was ignored")
	}
	if service.pending != nil {
		t.Fatal("execution did not consume token after a connect failure")
	}
	if _, err := service.ExecuteBatch(context.Background(), "token", "skip", true); err == nil {
		t.Fatal("batch token reused")
	}
}

func TestBatchPreviewEnforcesExclusionsBeforeConnecting(t *testing.T) {
	service, err := New(filepath.Join(t.TempDir(), "app.exe"))
	if err != nil {
		t.Fatal(err)
	}
	profile := sampleProfile(t)
	profile.Excludes = []string{"private/**"}
	if _, err := service.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	service.status.SSHPath = "missing-ssh-executable.exe"
	if _, err := service.PreviewBatch(context.Background(), "upload", []string{"private/secret"}); err == nil || !strings.Contains(err.Error(), "исключён") {
		t.Fatalf("excluded preview attempted connection: %v", err)
	}
}

func batchServerPreviewFixture(t *testing.T) (*Service, string, string, string, string, string) {
	t.Helper()
	service, err := New(filepath.Join(t.TempDir(), "app.exe"))
	if err != nil {
		t.Fatal(err)
	}
	localRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(localRoot, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	project := config.Project{Name: "Preview", LocalRoot: localRoot, Connections: []config.ProjectConnection{
		{Name: "First", Connection: transport.Connection{Mode: "manual", Host: "first.invalid", User: "test", Port: 22}, Mappings: []config.Mapping{{LocalPath: "src", RemotePath: "/first"}}},
		{Name: "Second", Connection: transport.Connection{Mode: "manual", Host: "second.invalid", User: "test", Port: 22}, Mappings: []config.Mapping{{LocalPath: "src", RemotePath: "/second"}}},
	}}
	status, err := service.SaveProject(project)
	if err != nil {
		t.Fatal(err)
	}
	project = status.Settings.Projects[0]
	return service, project.ID, project.Connections[0].ID, project.Connections[0].Mappings[0].ID, project.Connections[1].ID, project.Connections[1].Mappings[0].ID
}

func TestPreviewBatchForServerValidatesIDsAndDirection(t *testing.T) {
	service, projectID, connectionID, mappingID, _, _ := batchServerPreviewFixture(t)
	if _, err := service.PreviewBatchForServer(context.Background(), projectID, connectionID, mappingID, "sync", []string{"file.txt"}); err == nil {
		t.Fatal("unknown batch direction accepted")
	}
	if _, err := service.PreviewBatchForServer(context.Background(), "unknown", connectionID, mappingID, "upload", []string{"file.txt"}); err == nil {
		t.Fatal("unknown project ID accepted")
	}
	if _, err := service.PreviewBatchForServer(context.Background(), projectID, "unknown", mappingID, "upload", []string{"file.txt"}); err == nil {
		t.Fatal("unknown connection ID accepted")
	}
	if _, err := service.PreviewBatchForServer(context.Background(), projectID, connectionID, "unknown", "upload", []string{"file.txt"}); err == nil {
		t.Fatal("unknown mapping ID accepted")
	}
}

func TestPreviewBatchForServerLeavesActiveSelectionUnchanged(t *testing.T) {
	service, projectID, activeConnectionID, activeMappingID, otherConnectionID, otherMappingID := batchServerPreviewFixture(t)
	service.status.SSHPath = "missing-ssh-executable.exe"
	before := service.Status().Settings
	if _, err := service.PreviewBatchForServer(context.Background(), projectID, otherConnectionID, otherMappingID, "upload", []string{"file.txt"}); err == nil {
		t.Fatal("preview unexpectedly connected without an SSH executable")
	}
	after := service.Status().Settings
	if after.ActiveProjectID != before.ActiveProjectID || after.ActiveConnectionID != before.ActiveConnectionID || after.ActiveMappingID != before.ActiveMappingID {
		t.Fatalf("active selection changed: before (%q, %q, %q), after (%q, %q, %q)", before.ActiveProjectID, before.ActiveConnectionID, before.ActiveMappingID, after.ActiveProjectID, after.ActiveConnectionID, after.ActiveMappingID)
	}
	if before.ActiveConnectionID != activeConnectionID || before.ActiveMappingID != activeMappingID {
		t.Fatalf("fixture active selection changed unexpectedly: %+v", before)
	}
}

func TestPreviewBatchForServerRejectsMappingOutsideProject(t *testing.T) {
	service, projectID, connectionID, mappingID, _, _ := batchServerPreviewFixture(t)
	service.mu.Lock()
	for i := range service.status.Settings.Projects {
		project := &service.status.Settings.Projects[i]
		if project.ID == projectID {
			project.Connections[0].Mappings[0].LocalPath = "../outside"
			break
		}
	}
	service.mu.Unlock()
	service.status.SSHPath = "missing-ssh-executable.exe"
	if _, err := service.PreviewBatchForServer(context.Background(), projectID, connectionID, mappingID, "upload", []string{"file.txt"}); err == nil {
		t.Fatal("mapping outside project root accepted")
	}
}
