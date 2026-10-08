package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/credentials"
	"awesomeProject/internal/transport"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Status struct {
	WebviewVersion string          `json:"webviewVersion"`
	WebviewMessage string          `json:"webviewMessage"`
	Platform       string          `json:"platform"`
	DataDir        string          `json:"dataDir"`
	WebviewDataDir string          `json:"webviewDataDir"`
	SSHPath        string          `json:"sshPath"`
	SSHMessage     string          `json:"sshMessage"`
	Settings       config.Settings `json:"settings"`
}

type Service struct {
	authMu           sync.Mutex
	authPending      *pendingAuth
	authClosed       bool
	credentials      credentials.Store
	mu               sync.Mutex
	store            *config.Store
	status           Status
	operationMu      sync.Mutex
	cancelOperation  func()
	activeSession    *transport.Session
	pending          *transferPlan
	queue            []*queueEntry
	queuePaused      bool
	queueClosed      bool
	queueWorker      chan struct{}
	navigation       *navigationSession
	navigationClosed bool
	navigationIdle   time.Duration
	navigationOpen   func(context.Context, string, transport.Connection) (*transport.Session, error)
}

func New(executable string) (*Service, error) {
	dir, err := config.DataDir(executable)
	if err != nil {
		return nil, err
	}
	store, err := config.New(dir)
	if err != nil {
		return nil, err
	}
	settings, err := store.Load()
	if err != nil {
		return nil, err
	}
	// Create settings on first launch, never replace an invalid existing file.
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); os.IsNotExist(err) {
		if err := store.Save(settings); err != nil {
			return nil, err
		}
	}
	webviewDir := filepath.Join(dir, "webview2")
	if err := os.MkdirAll(webviewDir, 0700); err != nil {
		return nil, err
	}
	sshPath, sshErr := exec.LookPath("ssh.exe")
	message := "OpenSSH найден. Доступна авторизация по ключу, SSH-agent или паролю через диалог."
	if sshErr != nil {
		message = "ssh.exe не найден в PATH. Для следующего этапа потребуется доступный OpenSSH-клиент."
	}
	return &Service{store: store, status: Status{Platform: runtime.GOOS + "/" + runtime.GOARCH, DataDir: dir, WebviewDataDir: webviewDir, SSHPath: sshPath, SSHMessage: message, Settings: settings}}, nil
}

func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status
	status.Settings = cloneSettings(status.Settings)
	return status
}

func cloneSettings(in config.Settings) config.Settings {
	out := in
	out.Projects = make([]config.Project, len(in.Projects))
	for i, p := range in.Projects {
		out.Projects[i] = cloneProject(p)
	}
	if in.Profile != nil {
		p := *in.Profile
		p.Excludes = append([]string(nil), in.Profile.Excludes...)
		out.Profile = &p
	}
	return out
}

func (s *Service) SaveProjectName(name string) (config.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = strings.TrimSpace(name)
	if len([]rune(name)) > 120 {
		return config.Settings{}, fmt.Errorf("название должно содержать не больше 120 символов")
	}
	settings := cloneSettings(s.status.Settings)
	settings.ProjectName = name
	if err := s.store.Save(settings); err != nil {
		return config.Settings{}, err
	}
	s.status.Settings = cloneSettings(settings)
	return settings, nil
}
