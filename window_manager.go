package main

import (
	"awesomeProject/internal/application"
	"context"
	"fmt"
	wails "github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"sync"
)

type App struct {
	manager    *windowManager
	standalone *session
	resolved   func()
}
type windowManager struct {
	mu         sync.RWMutex
	addMu      sync.Mutex
	pending    *session
	configMu   sync.Mutex
	openMu     sync.Mutex
	sessions   map[uint]*session
	windows    map[uint]*wails.WebviewWindow
	app        *wails.App
	executable string
}

func (a *App) resolve(ctx context.Context) (*session, error) {
	if a.manager == nil && a.standalone != nil {
		return a.standalone, nil
	}
	if a.manager == nil || ctx == nil {
		return nil, fmt.Errorf("окно приложения не найдено")
	}
	window, ok := ctx.Value(wails.WindowKey).(wails.Window)
	if !ok || window == nil {
		return nil, fmt.Errorf("окно приложения не найдено")
	}
	a.manager.mu.RLock()
	s := a.manager.sessions[window.ID()]
	a.manager.mu.RUnlock()
	if s == nil || s.closed.Load() {
		return nil, fmt.Errorf("окно приложения закрыто")
	}
	if a.resolved != nil {
		a.resolved()
	}
	return s, nil
}
func (m *windowManager) broadcast(source *session) {
	settings := source.service.Status().Settings
	type entry struct {
		session *session
		window  *wails.WebviewWindow
	}
	m.mu.RLock()
	entries := make([]entry, 0, len(m.sessions))
	for id, s := range m.sessions {
		entries = append(entries, entry{s, m.windows[id]})
	}
	m.mu.RUnlock()
	for _, e := range entries {
		if e.session.closed.Load() {
			continue
		}
		if e.session != source {
			e.session.service.ApplySharedSettings(settings)
		}
		setWindowTitle(e.window, e.session)
	}
}
func setWindowTitle(w *wails.WebviewWindow, s *session) {
	if w == nil {
		return
	}
	settings := s.service.Status().Settings
	title := "SFTP Project Manager"
	for _, p := range settings.Projects {
		if p.ID == settings.ActiveProjectID {
			title = p.Name + " - " + title
			break
		}
	}
	w.SetTitle(title)
}
func (m *windowManager) registerWindowHook() {
	m.app.Window.OnCreate(func(window wails.Window) {
		w, ok := window.(*wails.WebviewWindow)
		if !ok {
			return
		}
		m.mu.Lock()
		s := m.pending
		if s != nil {
			s.desktop = &wailsDesktop{app: m.app, window: w}
			m.sessions[w.ID()] = s
			m.windows[w.ID()] = w
		}
		m.mu.Unlock()
		if s != nil {
			w.OnWindowEvent(events.Common.WindowClosing, func(*wails.WindowEvent) { m.remove(w.ID()) })
		}
	})
}
func (m *windowManager) add(s *session) {
	m.addMu.Lock()
	defer m.addMu.Unlock()
	m.mu.Lock()
	m.pending = s
	m.mu.Unlock()
	w := m.app.Window.NewWithOptions(wails.WebviewWindowOptions{Title: "SFTP Project Manager", URL: "/", Width: 1280, Height: 800, MinWidth: 960, MinHeight: 640})
	m.mu.Lock()
	m.pending = nil
	m.mu.Unlock()
	setWindowTitle(w, s)
}
func (m *windowManager) focusAny() {
	m.mu.RLock()
	var window *wails.WebviewWindow
	for _, w := range m.windows {
		window = w
		break
	}
	m.mu.RUnlock()
	if window != nil {
		window.UnMinimise()
		window.Show()
		window.Focus()
	}
}
func (m *windowManager) remove(id uint) {
	m.mu.Lock()
	s := m.sessions[id]
	if s != nil {
		s.closed.Store(true)
	}
	delete(m.sessions, id)
	delete(m.windows, id)
	m.mu.Unlock()
	if s != nil {
		s.shutdown()
	}
}
func (m *windowManager) shutdown() {
	m.mu.Lock()
	sessions := m.sessions
	for _, s := range sessions {
		s.closed.Store(true)
	}
	m.sessions = map[uint]*session{}
	m.windows = map[uint]*wails.WebviewWindow{}
	m.mu.Unlock()
	for _, s := range sessions {
		s.shutdown()
	}
}
func (s *session) shutdown() {
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if s.service != nil {
			s.service.Shutdown()
		}
	})
}

type ProjectOpenResult struct {
	Action string             `json:"action"`
	Status application.Status `json:"status"`
}

func (a *App) OpenProject(ctx context.Context, projectID, mode string) (ProjectOpenResult, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		return ProjectOpenResult{}, err
	}
	m := a.manager
	if m == nil {
		return ProjectOpenResult{}, fmt.Errorf("окна недоступны")
	}
	if mode != "current" && mode != "new" {
		return ProjectOpenResult{}, fmt.Errorf("неизвестный режим открытия")
	}
	m.openMu.Lock()
	defer m.openMu.Unlock()
	if s.closed.Load() {
		return ProjectOpenResult{}, closedWindowError()
	}
	m.mu.RLock()
	for id, other := range m.sessions {
		if !other.closed.Load() && other.service.Status().Settings.ActiveProjectID == projectID {
			w := m.windows[id]
			m.mu.RUnlock()
			if w != nil {
				w.UnMinimise()
				w.Show()
				w.Focus()
			}
			return ProjectOpenResult{Action: "focused", Status: s.GetStatus()}, nil
		}
	}
	m.mu.RUnlock()
	m.configMu.Lock()
	defer m.configMu.Unlock()
	if s.closed.Load() {
		return ProjectOpenResult{}, closedWindowError()
	}
	if mode == "current" {
		if _, err = s.service.SelectProject(projectID, "", ""); err != nil {
			return ProjectOpenResult{}, err
		}
		m.broadcast(s)
		return ProjectOpenResult{Action: "current", Status: s.GetStatus()}, nil
	}
	service, err := application.New(m.executable)
	if err != nil {
		return ProjectOpenResult{}, err
	}
	childCtx, cancel := context.WithCancel(context.Background())
	child := &session{service: service, ctx: childCtx, cancel: cancel}
	if _, err = service.SelectWindowProject(projectID, "", ""); err != nil {
		child.shutdown()
		return ProjectOpenResult{}, err
	}
	if s.closed.Load() {
		child.shutdown()
		return ProjectOpenResult{}, closedWindowError()
	}
	m.add(child)
	return ProjectOpenResult{Action: "new", Status: s.GetStatus()}, nil
}

func (m *windowManager) projectOpenElsewhere(source *session, id string) bool {
	if id == "" {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, other := range m.sessions {
		if other != source && !other.closed.Load() && other.service.Status().Settings.ActiveProjectID == id {
			return true
		}
	}
	return false
}

func closedWindowError() error { return fmt.Errorf("окно приложения закрыто") }
