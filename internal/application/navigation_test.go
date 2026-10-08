package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/transport"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

func navigationTestService(t *testing.T) (*Service, config.Profile) {
	t.Helper()
	service, err := New(filepath.Join(t.TempDir(), "app.exe"))
	if err != nil {
		t.Fatal(err)
	}
	profile := config.Profile{
		Name: "Navigation", LocalRoot: t.TempDir(), RemoteRoot: "/remote",
		Connection: transport.Connection{Mode: "manual", Host: "navigation.test", User: "tester", Port: 22},
	}
	if _, err := service.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.DisconnectRemote)
	return service, profile
}

func navigationClient(t *testing.T, handlers sftp.Handlers) *sftp.Client {
	t.Helper()
	left, right := net.Pipe()
	server := sftp.NewRequestServer(right, handlers)
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve() }()
	client, err := sftp.NewClientPipe(left, left)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.MkdirAll("/remote"); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"/remote/one", "/remote/two", "/remote/folder/nested"} {
		if err := client.MkdirAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		<-done
	})
	return client
}

func installNavigationFixture(t *testing.T, service *Service, handlers sftp.Handlers) *atomic.Int32 {
	t.Helper()
	var opens atomic.Int32
	service.navigationOpen = func(context.Context, string, transport.Connection) (*transport.Session, error) {
		opens.Add(1)
		return &transport.Session{Client: navigationClient(t, handlers)}, nil
	}
	return &opens
}

func TestNavigationReusesOneSessionForNestedListingsAndRefresh(t *testing.T) {
	service, _ := navigationTestService(t)
	opens := installNavigationFixture(t, service, sftp.InMemHandler())
	for _, relative := range []string{".", "folder", "folder/nested", "folder"} {
		if _, err := service.ListRemote(context.Background(), relative); err != nil {
			t.Fatalf("ListRemote(%q) error = %v", relative, err)
		}
	}
	if got := opens.Load(); got != 1 {
		t.Fatalf("navigation open count = %d, want 1", got)
	}
	if status := service.GetNavigationStatus(); status.State != "connected" {
		t.Fatalf("navigation status = %+v, want connected", status)
	}
}

func TestNavigationSuccessfulReadSurvivesLaterRequestCancellation(t *testing.T) {
	service, _ := navigationTestService(t)
	opens := installNavigationFixture(t, service, sftp.InMemHandler())
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := service.ListRemote(ctx, "."); err != nil {
		t.Fatal(err)
	}
	cancel()
	if status := service.GetNavigationStatus(); status.State != "connected" {
		t.Fatalf("cancelling a completed request disconnected navigation: %+v", status)
	}
	if _, err := service.ListRemote(context.Background(), "folder"); err != nil {
		t.Fatalf("follow-up listing after request cancellation: %v", err)
	}
	if got := opens.Load(); got != 1 {
		t.Fatalf("navigation open count = %d, want 1", got)
	}
}

type navigationDenyList struct {
	sftp.FileLister
	errorsByPath map[string]error
}

func (handler navigationDenyList) Filelist(request *sftp.Request) (sftp.ListerAt, error) {
	if request.Method == "List" {
		if err := handler.errorsByPath[request.Filepath]; err != nil {
			return nil, err
		}
	}
	return handler.FileLister.Filelist(request)
}

func TestNavigationNotFoundAndPermissionErrorsKeepSession(t *testing.T) {
	service, _ := navigationTestService(t)
	handlers := sftp.InMemHandler()
	handlers.FileList = navigationDenyList{FileLister: handlers.FileList, errorsByPath: map[string]error{
		"/remote/one": sftp.ErrSSHFxPermissionDenied,
		"/remote/two": sftp.ErrSSHFxFailure,
	}}
	opens := installNavigationFixture(t, service, handlers)
	if _, err := service.ListRemote(context.Background(), "."); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListRemote(context.Background(), "missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing directory error = %v, want os.ErrNotExist", err)
	}
	if status := service.GetNavigationStatus(); status.State != "connected" {
		t.Fatalf("not-found response dropped navigation: %+v", status)
	}
	for _, relative := range []string{"one", "two"} {
		_, err := service.ListRemote(context.Background(), relative)
		if relative == "one" {
			if !errors.Is(err, os.ErrPermission) {
				t.Fatalf("permission error = %v, want os.ErrPermission", err)
			}
		} else {
			var protocolErr *sftp.StatusError
			if !errors.As(err, &protocolErr) {
				t.Fatalf("generic SFTP error = %v, want protocol StatusError", err)
			}
		}
		if status := service.GetNavigationStatus(); status.State != "connected" {
			t.Fatalf("protocol error for %q dropped navigation: %+v", relative, status)
		}
	}
	if got := opens.Load(); got != 1 {
		t.Fatalf("navigation open count = %d, want 1", got)
	}
}

func TestNavigationMappingReuseAndAuthorizationChangesDisconnect(t *testing.T) {
	service, profile := navigationTestService(t)
	root := profile.LocalRoot
	for _, dir := range []string{"one", "two"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	project := config.Project{Name: "Mapped", LocalRoot: root, Connections: []config.ProjectConnection{{
		Name: "primary", Connection: profile.Connection,
		Mappings: []config.Mapping{{LocalPath: "one", RemotePath: "/remote/one"}, {LocalPath: "two", RemotePath: "/remote/two"}},
	}, {
		Name: "backup", Connection: transport.Connection{Mode: "manual", Host: "backup.test", User: "tester", Port: 22},
		Mappings: []config.Mapping{{LocalPath: "one", RemotePath: "/remote/one"}},
	}}}
	status, err := service.SaveProject(project)
	if err != nil {
		t.Fatal(err)
	}
	saved := status.Settings.Projects[1] // SaveProfile retains its legacy project.
	opens := installNavigationFixture(t, service, sftp.InMemHandler())
	if _, err := service.ListRemote(context.Background(), "."); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SelectProject(saved.ID, saved.Connections[0].ID, saved.Connections[0].Mappings[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListRemote(context.Background(), "."); err != nil {
		t.Fatalf("listing after mapping-only switch: %v", err)
	}
	if got := opens.Load(); got != 1 {
		t.Fatalf("mapping-only switch opened %d sessions, want 1", got)
	}
	if _, err := service.SelectProject(saved.ID, saved.Connections[1].ID, saved.Connections[1].Mappings[0].ID); err != nil {
		t.Fatal(err)
	}
	if status := service.GetNavigationStatus(); status.State != "disconnected" {
		t.Fatalf("server switch left prior navigation connected: %+v", status)
	}
	if _, err := service.ListRemote(context.Background(), "."); err != nil {
		t.Fatal(err)
	}
	if got := opens.Load(); got != 2 {
		t.Fatalf("server switch open count = %d, want 2", got)
	}

	// Updating authentication for the same selected project and connection ID
	// must detach the prior SSH session because the connection key changed.
	saved.Connections[1].Connection.User = "other-user"
	if _, err := service.SaveProject(saved); err != nil {
		t.Fatal(err)
	}
	if status := service.GetNavigationStatus(); status.State != "disconnected" {
		t.Fatalf("auth update left old navigation connected: %+v", status)
	}
	if _, err := service.ListRemote(context.Background(), "."); err != nil {
		t.Fatal(err)
	}
	if got := opens.Load(); got != 3 {
		t.Fatalf("auth update open count = %d, want 3", got)
	}

	// Switching projects also invalidates the session even if the connection
	// details happen to be equal.
	other := config.Project{Name: "Other project", LocalRoot: t.TempDir(), Connections: []config.ProjectConnection{{Name: "primary", Connection: saved.Connections[1].Connection, Mappings: []config.Mapping{{LocalPath: ".", RemotePath: "/remote"}}}}}
	otherStatus, err := service.SaveProject(other)
	if err != nil {
		t.Fatal(err)
	}
	if status := service.GetNavigationStatus(); status.State != "disconnected" || status.ProjectID != otherStatus.Settings.ActiveProjectID {
		t.Fatalf("project switch navigation status = %+v", status)
	}
	if _, err := service.ListRemote(context.Background(), "."); err != nil {
		t.Fatal(err)
	}
	if got := opens.Load(); got != 4 {
		t.Fatalf("project switch open count = %d, want 4", got)
	}
}

func TestNavigationSelectionDuringOpenCannotResurrectOldSession(t *testing.T) {
	service, profile := navigationTestService(t)
	opened := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	var clientsMu sync.Mutex
	var clients []*sftp.Client
	service.navigationOpen = func(ctx context.Context, _ string, _ transport.Connection) (*transport.Session, error) {
		client := navigationClient(t, sftp.InMemHandler())
		clientsMu.Lock()
		clients = append(clients, client)
		clientsMu.Unlock()
		close(opened)
		<-release // Deliberately ignore ctx to exercise late completion.
		return &transport.Session{Client: client}, nil
	}
	listDone := make(chan error, 1)
	go func() { _, err := service.ListRemote(context.Background(), "."); listDone <- err }()
	select {
	case <-opened:
	case <-time.After(3 * time.Second):
		t.Fatal("navigation open was not entered")
	}
	changed := profile
	changed.Connection.User = "changed-user"
	if _, err := service.SaveProfile(changed); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-listDone:
		if err == nil || !errors.Is(err, context.Canceled) && !strings.Contains(strings.ToLower(err.Error()), "подключение изменилось") {
			t.Fatalf("late listing error = %v, want invalidated connection", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listing did not finish after stale open returned")
	}
	if status := service.GetNavigationStatus(); status.State != "disconnected" {
		t.Fatalf("stale open resurrected navigation: %+v", status)
	}
	clientsMu.Lock()
	defer clientsMu.Unlock()
	if len(clients) != 1 {
		t.Fatalf("opened clients = %d, want 1", len(clients))
	}
	if _, err := clients[0].Getwd(); err == nil {
		t.Fatal("late session client remained open after stale completion")
	}
}

func TestNavigationCancelledHandshakeInvalidatesPendingSession(t *testing.T) {
	service, _ := navigationTestService(t)
	started := make(chan struct{})
	service.navigationOpen = func(ctx context.Context, _ string, _ transport.Connection) (*transport.Session, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	listDone := make(chan error, 1)
	go func() { _, err := service.ListRemote(ctx, "."); listDone <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("navigation handshake did not start")
	}
	cancel()
	select {
	case err := <-listDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled handshake error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled handshake did not return")
	}
	if status := service.GetNavigationStatus(); status.State != "disconnected" {
		t.Fatalf("cancelled handshake retained navigation: %+v", status)
	}
}

func TestNavigationStalledListingCancellationClosesSession(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	base := sftp.InMemHandler()
	base.FileList = blockingNavigationLister{FileLister: base.FileList, entered: entered, release: release}
	service, _ := navigationTestService(t)
	opens := installNavigationFixture(t, service, base)
	defer releaseOnce.Do(func() { close(release) })
	ctx, cancel := context.WithCancel(context.Background())
	listDone := make(chan error, 1)
	go func() { _, err := service.ListRemote(ctx, "."); listDone <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("remote listing did not stall in the handler")
	}
	if status := service.GetNavigationStatus(); status.State != "connected" {
		t.Fatalf("stalled listing status = %+v, want connected", status)
	}
	cancel()
	select {
	case err := <-listDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled listing error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not interrupt stalled SFTP listing")
	}
	if status := service.GetNavigationStatus(); status.State != "disconnected" {
		t.Fatalf("cancelled stalled listing retained session: %+v", status)
	}
	if opens.Load() != 1 {
		t.Fatalf("navigation open count = %d, want 1", opens.Load())
	}
}

type blockingNavigationLister struct {
	sftp.FileLister
	entered chan<- struct{}
	release <-chan struct{}
}

func (handler blockingNavigationLister) Filelist(request *sftp.Request) (sftp.ListerAt, error) {
	if request.Method == "List" && request.Filepath == "/remote" {
		select {
		case handler.entered <- struct{}{}:
		default:
		}
		<-handler.release
	}
	return handler.FileLister.Filelist(request)
}

func TestNavigationIdleTimerResetsAndDoesNotExpireInFlightRead(t *testing.T) {
	service, _ := navigationTestService(t)
	service.navigationIdle = 80 * time.Millisecond
	opens := installNavigationFixture(t, service, sftp.InMemHandler())
	if _, err := service.ListRemote(context.Background(), "."); err != nil {
		t.Fatal(err)
	}
	time.Sleep(45 * time.Millisecond)
	if _, err := service.ListRemote(context.Background(), "folder"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(45 * time.Millisecond) // Past the original timer, before the reset timer.
	if status := service.GetNavigationStatus(); status.State != "connected" {
		t.Fatalf("navigation expired despite a recent read: %+v", status)
	}
	waitNavigationState(t, service, "disconnected", 2*time.Second)
	if opens.Load() != 1 {
		t.Fatalf("idle timer reset caused %d opens, want 1", opens.Load())
	}

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	base := sftp.InMemHandler()
	base.FileList = blockingNavigationLister{FileLister: base.FileList, entered: entered, release: release}
	service.navigationOpen = func(context.Context, string, transport.Connection) (*transport.Session, error) {
		return &transport.Session{Client: navigationClient(t, base)}, nil
	}
	listDone := make(chan error, 1)
	go func() { _, err := service.ListRemote(context.Background(), "."); listDone <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight listing did not start")
	}
	time.Sleep(2 * service.navigationIdle)
	if status := service.GetNavigationStatus(); status.State != "connected" {
		t.Fatalf("idle timer expired an in-flight listing: %+v", status)
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-listDone; err != nil {
		t.Fatalf("released listing error = %v", err)
	}
	waitNavigationState(t, service, "disconnected", 2*time.Second)
}

func waitNavigationState(t *testing.T, service *Service, state string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := service.GetNavigationStatus().State; got == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("navigation did not reach %q: %+v", state, service.GetNavigationStatus())
}

func TestNavigationConcurrentListingsSerializeAndStatusIsRaceSafe(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	base := sftp.InMemHandler()
	base.FileList = blockingNavigationLister{FileLister: base.FileList, entered: entered, release: release}
	service, _ := navigationTestService(t)
	installNavigationFixture(t, service, base)
	defer releaseOnce.Do(func() { close(release) })
	firstDone := make(chan error, 1)
	go func() { _, err := service.ListRemote(context.Background(), "."); firstDone <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first listing did not enter handler")
	}
	if _, err := service.ListRemote(context.Background(), "folder"); err == nil {
		t.Fatalf("simultaneous listing error = %v, want serialized-operation error", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				_ = service.GetNavigationStatus()
				_ = service.GetQueue()
			}
		}()
	}
	wg.Wait()
	releaseOnce.Do(func() { close(release) })
	if err := <-firstDone; err != nil {
		t.Fatalf("first listing error = %v", err)
	}
}

func TestNavigationQueueCancellationLeavesIdleSessionAlive(t *testing.T) {
	service, _ := navigationTestService(t)
	installNavigationFixture(t, service, sftp.InMemHandler())
	if _, err := service.ListRemote(context.Background(), "."); err != nil {
		t.Fatal(err)
	}
	service.SetQueuePaused(false)
	service.operationMu.Lock()
	service.mu.Lock()
	service.pending = queueTestPlan(config.Profile{Name: "queued", LocalRoot: t.TempDir(), RemoteRoot: "/remote", Connection: transport.Connection{Mode: "manual", Host: "queue.test", User: "tester", Port: 22}}, BatchItem{RelativePath: "file", Kind: "file", Size: 1})
	service.mu.Unlock()
	state, err := service.EnqueueBatch(context.Background(), "token", "skip", true)
	if err != nil {
		service.operationMu.Unlock()
		t.Fatal(err)
	}
	waitForQueueStatus(t, service, state.Jobs[0].ID, "running")
	if err := service.CancelQueueJob(state.Jobs[0].ID); err != nil {
		service.operationMu.Unlock()
		t.Fatal(err)
	}
	service.operationMu.Unlock()
	waitQueueDone(t, service)
	if got := service.GetQueue().Jobs[0].Status; got != "cancelled" {
		t.Fatalf("queue job status = %q, want cancelled", got)
	}
	if status := service.GetNavigationStatus(); status.State != "connected" {
		t.Fatalf("cancelling queued operation disconnected idle navigation: %+v", status)
	}
}
