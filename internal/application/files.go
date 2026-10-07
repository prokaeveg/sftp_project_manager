package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/exclusion"
	"awesomeProject/internal/mapping"
	"awesomeProject/internal/transfer"
	"awesomeProject/internal/transport"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

type FileEntry struct {
	Name         string `json:"name"`
	RelativePath string `json:"relativePath"`
	Size         int64  `json:"size"`
	IsDir        bool   `json:"isDir"`
	IsLink       bool   `json:"isLink"`
	Excluded     bool   `json:"excluded"`
}

type Preview struct {
	Token       string `json:"token"`
	Direction   string `json:"direction"`
	Server      string `json:"server"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
}
type transferPlan struct {
	preview  Preview
	profile  config.Profile
	relative string
	batch    *batchPlan
}

func validateProfile(profile config.Profile) error {
	if err := exclusion.Validate(profile.Excludes); err != nil {
		return err
	}
	if profile.ExcludeBase != "" && profile.ExcludeBase != "." {
		return errors.New("для корневого профиля исключения задаются от корня проекта")
	}
	if strings.TrimSpace(profile.Name) == "" || len([]rune(profile.Name)) > 120 {
		return errors.New("укажите название проекта до 120 символов")
	}
	if err := profile.Connection.Validate(); err != nil {
		return err
	}
	_, err := mapping.New(profile.LocalRoot, profile.RemoteRoot)
	return err
}

func (s *Service) SaveProfile(profile config.Profile) (Status, error) {
	var navigationToClose *transport.Session
	defer func() { closeNavigationSession(navigationToClose) }()
	profile.Name = strings.TrimSpace(profile.Name)
	if err := validateProfile(profile); err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	settings := cloneSettings(s.status.Settings)
	settings.ProjectName = profile.Name
	legacy := config.Project{ID: "legacy-project", Name: profile.Name, LocalRoot: profile.LocalRoot, DefaultConnectionID: "legacy-connection", Connections: []config.ProjectConnection{{ID: "legacy-connection", Name: "Default", Connection: profile.Connection, RootPath: profile.RemoteRoot, Excludes: append([]string(nil), profile.Excludes...), Mappings: []config.Mapping{{ID: "legacy-mapping", LocalPath: ".", DeploymentPath: "."}}}}}
	idx := -1
	for i, p := range settings.Projects {
		if p.ID == legacy.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		settings.Projects = append(settings.Projects, legacy)
	} else {
		settings.Projects[idx] = legacy
	}
	settings.ActiveProjectID = legacy.ID
	settings.ActiveConnectionID = legacy.Connections[0].ID
	settings.ActiveMappingID = legacy.Connections[0].Mappings[0].ID
	configFlatten(&settings)
	if err := s.store.Save(settings); err != nil {
		s.mu.Unlock()
		return Status{}, err
	}
	if idx >= 0 && len(s.status.Settings.Projects[idx].Connections) > 0 && !slices.Equal(s.status.Settings.Projects[idx].Connections[0].Excludes, profile.Excludes) {
		s.pending = nil
	}
	s.status.Settings = cloneSettings(settings)
	navigationToClose = s.reconcileNavigationLocked()
	s.mu.Unlock()
	return s.Status(), nil
}

func (s *Service) profile() (config.Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.Settings.Profile == nil {
		return config.Profile{}, errors.New("сначала сохраните профиль проекта")
	}
	profile := *s.status.Settings.Profile
	profile.Excludes = append([]string(nil), profile.Excludes...)
	return profile, nil
}

func excluded(profile config.Profile, relative string) bool {
	return exclusion.Match(profile.Excludes, path.Join(profile.ExcludeBase, filepath.ToSlash(relative)))
}

func checkExcluded(profile config.Profile, relative string) error {
	if excluded(profile, relative) {
		return fmt.Errorf("путь %q исключён для этого подключения; измените исключения и сохраните проект", relative)
	}
	return nil
}

func (s *Service) CancelOperation() {
	s.mu.Lock()
	cancel, session := s.cancelOperation, s.activeSession
	navigationRequest := s.navigation != nil && s.navigation.inUse && s.navigation.session == session
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// The request watcher owns navigation cancellation. Closing its session
	// directly after releasing mu could race with the next successful reuse.
	if session != nil && !navigationRequest {
		session.Close()
	}
}

func (s *Service) Shutdown() {
	s.shutdownAuth()
	s.shutdownNavigation()
	s.shutdownQueue()
	s.CancelOperation()
	// Wait until any in-flight connect or transfer has reaped its SSH process.
	s.operationMu.Lock()
	s.operationMu.Unlock()
}

func (s *Service) remoteOperation(parent context.Context, profile config.Profile, duration time.Duration, action func(context.Context, *transport.Session) error) error {
	ctx, cancel := context.WithTimeout(parent, duration)
	defer cancel()
	for !s.operationMu.TryLock() {
		if wait, _ := parent.Value(queueWaitKey{}).(bool); !wait {
			return errors.New("другая операция ещё выполняется")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Millisecond):
		}
	}
	defer s.operationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.cancelOperation = cancel
	executable := s.status.SSHPath
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.cancelOperation = nil; s.mu.Unlock() }()
	session, err := transport.Open(transport.WithAuthPrompt(ctx, s.requestAuth), executable, profile.Connection)
	if err != nil {
		return err
	}
	defer session.Close()
	s.mu.Lock()
	s.activeSession = session
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.activeSession = nil; s.mu.Unlock() }()
	if err := action(ctx, session); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("операция остановлена: %w", errors.Join(ctx.Err(), err))
		}
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("недостаточно прав на файл или каталог: %w", err)
		}
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("файл или каталог не найден: %w", err)
		}
		return err
	}
	return nil
}

func (s *Service) ListLocal(relative string) ([]FileEntry, error) {
	profile, err := s.profile()
	if err != nil {
		return nil, err
	}
	return listLocal(profile, relative)
}

// ListProjectLocal lists the full selected project, independently of a server
// or mapping. Connection exclusions remain relative to the project root.
func (s *Service) ListProjectLocal(relative string) ([]FileEntry, error) {
	s.mu.Lock()
	var profile config.Profile
	for _, project := range s.status.Settings.Projects {
		if project.ID != s.status.Settings.ActiveProjectID {
			continue
		}
		profile = config.Profile{Name: project.Name, LocalRoot: project.LocalRoot, RemoteRoot: "/"}
		for _, connection := range project.Connections {
			if connection.ID == s.status.Settings.ActiveConnectionID {
				profile.Excludes = slices.Clone(connection.Excludes)
				break
			}
		}
		break
	}
	s.mu.Unlock()
	if profile.LocalRoot == "" {
		return nil, errors.New("сначала выберите проект")
	}
	return listLocal(profile, relative)
}

func listLocal(profile config.Profile, relative string) ([]FileEntry, error) {
	mapper, err := mapping.New(profile.LocalRoot, profile.RemoteRoot)
	if err != nil {
		return nil, err
	}
	remote, err := mapper.LocalToRemote(relative)
	if err != nil {
		return nil, err
	}
	local, err := mapper.RemoteToLocal(remote)
	if err != nil {
		return nil, err
	}
	if err := checkExcluded(profile, relative); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(local)
	if err != nil {
		return nil, err
	}
	result := make([]FileEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		entryRelative := filepath.ToSlash(filepath.Join(relative, entry.Name()))
		result = append(result, FileEntry{Name: entry.Name(), RelativePath: entryRelative, Size: info.Size(), IsDir: entry.IsDir(), IsLink: entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0, Excluded: excluded(profile, entryRelative)})
	}
	sortEntries(result)
	return result, nil
}

func sortEntries(entries []FileEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})
}

func (s *Service) ListRemote(ctx context.Context, relative string) ([]FileEntry, error) {
	s.mu.Lock()
	var projectRoot string
	var connection config.ProjectConnection
	for _, project := range s.status.Settings.Projects {
		if project.ID != s.status.Settings.ActiveProjectID {
			continue
		}
		projectRoot = project.LocalRoot
		for _, candidate := range project.Connections {
			if candidate.ID == s.status.Settings.ActiveConnectionID {
				connection = candidate
				connection.Excludes = slices.Clone(candidate.Excludes)
				connection.Mappings = slices.Clone(candidate.Mappings)
				break
			}
		}
		break
	}
	if connection.ID == "" {
		s.mu.Unlock()
		return nil, errors.New("сначала выберите сервер проекта")
	}
	key := s.navigationKeyLocked()
	s.mu.Unlock()
	mapper, err := mapping.New(projectRoot, connection.RootPath)
	if err != nil {
		return nil, err
	}
	remote, err := mapper.RemoteRelativeToAbsolute(relative)
	if err != nil {
		return nil, err
	}
	if remoteRootEntryExcluded(connection, relative) {
		return nil, fmt.Errorf("путь %q исключён для этого подключения", relative)
	}
	result := []FileEntry{}
	err = s.navigationOperation(ctx, key, func(session *transport.Session) error {
		if err := transfer.CheckRemotePath(session.Client, remote); err != nil {
			return err
		}
		entries, err := session.Client.ReadDir(remote)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name() == "" || entry.Name() == "." || entry.Name() == ".." || strings.ContainsAny(entry.Name(), "/\\") {
				return errors.New("сервер вернул недопустимое имя файла")
			}
			entryRelative := path.Join(relative, entry.Name())
			result = append(result, FileEntry{Name: entry.Name(), RelativePath: entryRelative, Size: entry.Size(), IsDir: entry.IsDir(), IsLink: entry.Mode()&os.ModeSymlink != 0, Excluded: remoteRootEntryExcluded(connection, entryRelative)})
		}
		return nil
	})
	sortEntries(result)
	return result, err
}

func (s *Service) PreviewTransfer(direction, relative string) (Preview, error) {
	if direction != "upload" && direction != "download" {
		return Preview{}, errors.New("неизвестное направление передачи")
	}
	profile, err := s.profile()
	if err != nil {
		return Preview{}, err
	}
	mapper, err := mapping.New(profile.LocalRoot, profile.RemoteRoot)
	if err != nil {
		return Preview{}, err
	}
	var remote string
	if direction == "download" {
		remote, err = mapper.RemoteRelativeToAbsolute(relative)
	} else {
		remote, err = mapper.LocalToRemote(relative)
	}
	if err != nil {
		return Preview{}, err
	}
	local, err := mapper.RemoteToLocal(remote)
	if err != nil {
		return Preview{}, err
	}
	if err := checkExcluded(profile, relative); err != nil {
		return Preview{}, err
	}
	server := profile.Connection.Alias
	if profile.Connection.Mode == "manual" {
		server = fmt.Sprintf("%s@%s:%d", profile.Connection.User, profile.Connection.Host, profile.Connection.Port)
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return Preview{}, err
	}
	preview := Preview{Token: hex.EncodeToString(random), Direction: direction, Server: server, Source: local, Destination: remote}
	if direction == "download" {
		preview.Source, preview.Destination = remote, local
	}
	s.mu.Lock()
	s.pending = &transferPlan{preview: preview, profile: profile, relative: relative}
	s.mu.Unlock()
	return preview, nil
}

func (s *Service) ExecuteTransfer(ctx context.Context, token string, overwrite bool) error {
	s.mu.Lock()
	if s.pending == nil || s.pending.batch != nil || s.pending.preview.Token != token {
		s.mu.Unlock()
		return errors.New("предпросмотр устарел; выберите файл повторно")
	}
	plan := *s.pending
	s.pending = nil
	s.mu.Unlock()
	// Revalidate local paths immediately before I/O; the connection remains the preview snapshot.
	mapper, err := mapping.New(plan.profile.LocalRoot, plan.profile.RemoteRoot)
	if err != nil {
		return err
	}
	var remote string
	if plan.preview.Direction == "download" {
		remote, err = mapper.RemoteRelativeToAbsolute(plan.relative)
	} else {
		remote, err = mapper.LocalToRemote(plan.relative)
	}
	if err != nil {
		return err
	}
	local, err := mapper.RemoteToLocal(remote)
	if err != nil {
		return err
	}
	if err := checkExcluded(plan.profile, plan.relative); err != nil {
		return err
	}
	return s.remoteOperation(ctx, plan.profile, 5*time.Minute, func(operationCtx context.Context, session *transport.Session) error {
		// Connecting can take seconds. Check local links and casing again after the handshake.
		mapper, err := mapping.New(plan.profile.LocalRoot, plan.profile.RemoteRoot)
		if err != nil {
			return err
		}
		if plan.preview.Direction == "download" {
			remote, err = mapper.RemoteRelativeToAbsolute(plan.relative)
		} else {
			remote, err = mapper.LocalToRemote(plan.relative)
		}
		if err != nil {
			return err
		}
		local, err = mapper.RemoteToLocal(remote)
		if err != nil {
			return err
		}
		if plan.preview.Direction == "upload" {
			return transfer.Upload(operationCtx, session.Client, local, remote, overwrite)
		}
		return transfer.Download(operationCtx, session.Client, remote, local, overwrite)
	})
}
