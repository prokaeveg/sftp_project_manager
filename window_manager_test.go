package main

import (
	"awesomeProject/internal/application"
	"awesomeProject/internal/config"
	"context"
	wails "github.com/wailsapp/wails/v3/pkg/application"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRouterRejectsUnknownCaller(t *testing.T) {
	a := &App{manager: &windowManager{sessions: map[uint]*session{}}}
	if _, err := a.GetStatus(context.Background()); err == nil {
		t.Fatal("unknown caller accepted")
	}
}

func TestBroadcastPreservesOtherWindowSelection(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "app.exe")
	first, err := application.New(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Shutdown()
	one, err := first.SaveProject(config.Project{Name: "one", LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	second, err := application.New(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Shutdown()
	two, err := first.SaveProject(config.Project{Name: "two", LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s1, s2 := &session{service: first}, &session{service: second}
	manager := &windowManager{sessions: map[uint]*session{1: s1, 2: s2}, windows: map[uint]*wails.WebviewWindow{}}
	manager.broadcast(s1)
	got := second.Status().Settings
	if got.ActiveProjectID != one.Settings.ActiveProjectID || len(got.Projects) != 2 {
		t.Fatalf("selection or catalog lost: %+v", got)
	}
	if !manager.projectOpenElsewhere(s1, one.Settings.ActiveProjectID) || manager.projectOpenElsewhere(s1, two.Settings.ActiveProjectID) {
		t.Fatal("open project detection failed")
	}
}

func TestRemoveCancelsOnlyClosedSession(t *testing.T) {
	firstCtx, firstCancel := context.WithCancel(context.Background())
	secondCtx, secondCancel := context.WithCancel(context.Background())
	defer secondCancel()
	first, second := &session{ctx: firstCtx, cancel: firstCancel}, &session{ctx: secondCtx, cancel: secondCancel}
	m := &windowManager{sessions: map[uint]*session{1: first, 2: second}, windows: map[uint]*wails.WebviewWindow{}}
	m.remove(1)
	m.remove(1)
	if firstCtx.Err() == nil || secondCtx.Err() != nil || m.sessions[2] != second {
		t.Fatal("closing one window affected another")
	}
}

type callerWindow struct {
	wails.Window
	id uint
}

func (w callerWindow) ID() uint { return w.id }
func caller(id uint) context.Context {
	return context.WithValue(context.Background(), wails.WindowKey, callerWindow{id: id})
}

func TestConcurrentWindowSavesKeepBothProjects(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "app.exe")
	first, err := application.New(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Shutdown()
	one, err := first.SaveProject(config.Project{Name: "one", LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	two, err := first.SaveProject(config.Project{Name: "two", LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	second, err := application.New(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Shutdown()
	if _, err = first.SelectProject(one.Settings.ActiveProjectID, "", ""); err != nil {
		t.Fatal(err)
	}
	s1, s2 := &session{service: first}, &session{service: second}
	m := &windowManager{sessions: map[uint]*session{1: s1, 2: s2}, windows: map[uint]*wails.WebviewWindow{}}
	app := &App{manager: m}
	p1, p2 := one.Settings.Projects[0], two.Settings.Projects[1]
	p1.Name = "one changed"
	p2.Name = "two changed"
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, item := range []struct {
		id uint
		p  config.Project
	}{{1, p1}, {2, p2}} {
		wg.Add(1)
		go func(id uint, p config.Project) {
			defer wg.Done()
			<-start
			_, err := app.SaveProject(caller(id), p)
			errs <- err
		}(item.id, item.p)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	disk, err := application.New(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Shutdown()
	projects := disk.Status().Settings.Projects
	if len(projects) != 2 || projects[0].Name != p1.Name || projects[1].Name != p2.Name {
		t.Fatalf("lost concurrent save: %+v", projects)
	}
	if first.Status().Settings.ActiveProjectID != p1.ID || second.Status().Settings.ActiveProjectID != p2.ID {
		t.Fatal("window selection changed")
	}
}

func TestQueueStateIsRoutedPerWindow(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "app.exe")
	s1, err := application.New(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Shutdown()
	s2, err := application.New(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Shutdown()
	m := &windowManager{sessions: map[uint]*session{1: {service: s1}, 2: {service: s2}}}
	app := &App{manager: m}
	if err := app.SetQueuePaused(caller(1), true); err != nil {
		t.Fatal(err)
	}
	q1, err := app.GetQueue(caller(1))
	if err != nil {
		t.Fatal(err)
	}
	q2, err := app.GetQueue(caller(2))
	if err != nil {
		t.Fatal(err)
	}
	if !q1.Paused || q2.Paused {
		t.Fatal("queue state leaked between windows")
	}
}
func TestSingleInstanceIDUsesPortableDirectory(t *testing.T) {
	dir := t.TempDir()
	a := singleInstanceID(filepath.Join(dir, "app.exe"))
	b := singleInstanceID(filepath.Join(strings.ToUpper(dir), "other.exe"))
	c := singleInstanceID(filepath.Join(dir, "other", "app.exe"))
	if a != b || a == c {
		t.Fatal("portable directory identity mismatch")
	}
}

func TestClosedCallerWaitingForSaveCannotOverwriteCatalog(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "app.exe")
	first, err := application.New(exe)
	if err != nil {
		t.Fatal(err)
	}
	one, err := first.SaveProject(config.Project{Name: "one", LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	two, err := first.SaveProject(config.Project{Name: "two", LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	second, err := application.New(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Shutdown()
	if _, err = first.SelectProject(one.Settings.ActiveProjectID, "", ""); err != nil {
		t.Fatal(err)
	}
	m := &windowManager{sessions: map[uint]*session{1: {service: first}, 2: {service: second}}, windows: map[uint]*wails.WebviewWindow{}}
	resolved := make(chan struct{})
	app := &App{manager: m, resolved: func() { close(resolved) }}
	p1, p2 := one.Settings.Projects[0], two.Settings.Projects[1]
	p1.Name = "stale"
	p2.Name = "saved"
	m.configMu.Lock()
	result := make(chan error, 1)
	go func() { _, err := app.SaveProject(caller(1), p1); result <- err }()
	<-resolved
	m.remove(1)
	m.configMu.Unlock()
	if err := <-result; err == nil {
		t.Fatal("closed window saved")
	}
	if _, err := (&App{manager: m}).SaveProject(caller(2), p2); err != nil {
		t.Fatal(err)
	}
	disk, err := application.New(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Shutdown()
	projects := disk.Status().Settings.Projects
	if projects[0].Name != "one" || projects[1].Name != "saved" {
		t.Fatalf("unexpected catalog: %+v", projects)
	}
}
func TestClosedCallerWaitingToOpenCannotCreateWindow(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "app.exe")
	service, err := application.New(exe)
	if err != nil {
		t.Fatal(err)
	}
	m := &windowManager{sessions: map[uint]*session{1: {service: service}}, windows: map[uint]*wails.WebviewWindow{}}
	resolved := make(chan struct{})
	app := &App{manager: m, resolved: func() { close(resolved) }}
	m.openMu.Lock()
	result := make(chan error, 1)
	go func() { _, err := app.OpenProject(caller(1), "some-project", "new"); result <- err }()
	<-resolved
	m.remove(1)
	m.openMu.Unlock()
	if err := <-result; err == nil {
		t.Fatal("closed window opened a project")
	}
	if len(m.sessions) != 0 {
		t.Fatal("new session created")
	}
}
