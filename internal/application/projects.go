package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/exclusion"
	"awesomeProject/internal/mapping"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func pathContains(parent, child string) bool {
	p := strings.TrimSuffix(path.Clean(parent), "/")
	c := strings.TrimSuffix(path.Clean(child), "/")
	return c == p || p == "/" || strings.HasPrefix(c, p+"/")
}

func localPathContains(parent, child string) bool {
	p := strings.Trim(filepath.ToSlash(filepath.Clean(parent)), "/")
	c := strings.Trim(filepath.ToSlash(filepath.Clean(child)), "/")
	if runtime.GOOS == "windows" {
		p, c = strings.ToLower(p), strings.ToLower(c)
	}
	return c == p || p == "" || p == "." || strings.HasPrefix(c, p+"/")
}

func validateProject(project config.Project) error {
	project.Name = strings.TrimSpace(project.Name)
	if project.Name == "" || len([]rune(project.Name)) > 120 {
		return errors.New("укажите название проекта длиной от 1 до 120 символов")
	}
	root, err := mapping.New(project.LocalRoot, "/")
	if err != nil {
		return fmt.Errorf("project %q root: %w", project.Name, err)
	}
	for ci, c := range project.Connections {
		if err := exclusion.Validate(c.Excludes); err != nil {
			return fmt.Errorf("подключение %q: %w", c.Name, err)
		}
		label := strings.TrimSpace(c.Name)
		if label == "" || len([]rune(label)) > 120 {
			return fmt.Errorf("укажите название подключения %d длиной от 1 до 120 символов", ci+1)
		}
		if err := c.Connection.Validate(); err != nil {
			return fmt.Errorf("connection %q: %w", label, err)
		}
		if len(c.Mappings) == 0 {
			return fmt.Errorf("добавьте хотя бы одно сопоставление в подключение %q", label)
		}
		for mi, m := range c.Mappings {
			if strings.TrimSpace(m.LocalPath) == "" {
				return fmt.Errorf("подключение %q, сопоставление %d: укажите локальную папку", label, mi+1)
			}
			mapped, err := root.LocalToRemote(m.LocalPath)
			if err != nil {
				return fmt.Errorf("подключение %q, локальная папка %q: %w", label, m.LocalPath, err)
			}
			local, err := root.RemoteToLocal(mapped)
			if err != nil {
				return fmt.Errorf("подключение %q, локальная папка %q: %w", label, m.LocalPath, err)
			}
			info, err := os.Stat(local)
			if err != nil {
				return fmt.Errorf("подключение %q, локальная папка %q: %w", label, m.LocalPath, err)
			}
			if !info.IsDir() {
				return fmt.Errorf("подключение %q, локальный путь %q должен быть папкой", label, m.LocalPath)
			}
			if _, err := mapping.New(local, m.RemotePath); err != nil {
				return fmt.Errorf("подключение %q, сопоставление локальной папки %q: удалённый путь: %w", label, m.LocalPath, err)
			}
			for prev := 0; prev < mi; prev++ {
				old := c.Mappings[prev]
				if localPathContains(old.LocalPath, m.LocalPath) || localPathContains(m.LocalPath, old.LocalPath) {
					return fmt.Errorf("подключение %q: локальные папки %q и %q пересекаются", label, old.LocalPath, m.LocalPath)
				}
				if pathContains(old.RemotePath, m.RemotePath) || pathContains(m.RemotePath, old.RemotePath) {
					return fmt.Errorf("подключение %q: удалённые папки %q и %q пересекаются", label, old.RemotePath, m.RemotePath)
				}
			}
		}
	}
	return nil
}

func (s *Service) SaveProject(project config.Project) (Status, error) {
	project = cloneProject(project)
	project.Name = strings.TrimSpace(project.Name)
	if err := validateProject(project); err != nil {
		return Status{}, err
	}
	if project.ID == "" {
		id, err := newID()
		if err != nil {
			return Status{}, err
		}
		project.ID = id
	}
	for ci := range project.Connections {
		project.Connections[ci].Name = strings.TrimSpace(project.Connections[ci].Name)
		if project.Connections[ci].ID == "" {
			id, err := newID()
			if err != nil {
				return Status{}, err
			}
			project.Connections[ci].ID = id
		}
		for mi := range project.Connections[ci].Mappings {
			m := &project.Connections[ci].Mappings[mi]
			if m.ID == "" {
				id, err := newID()
				if err != nil {
					return Status{}, err
				}
				m.ID = id
			}
			m.LocalPath = filepath.ToSlash(filepath.Clean(m.LocalPath))
			m.RemotePath = path.Clean(m.RemotePath)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	settings := cloneSettings(s.status.Settings)
	idx := -1
	for i, p := range settings.Projects {
		if p.ID == project.ID {
			idx = i
			break
		}
	}
	defaultExists := project.DefaultConnectionID == ""
	for _, connection := range project.Connections {
		if connection.ID == project.DefaultConnectionID {
			defaultExists = true
		}
	}
	if !defaultExists {
		// Removing the previous default through this edit picks a remaining
		// server. Arbitrary unknown IDs still indicate an invalid request.
		if idx < 0 || settings.Projects[idx].DefaultConnectionID != project.DefaultConnectionID {
			return Status{}, errors.New("сервер по умолчанию не найден в проекте")
		}
		project.DefaultConnectionID = ""
	}
	if project.DefaultConnectionID == "" && len(project.Connections) > 0 {
		project.DefaultConnectionID = project.Connections[0].ID
	}
	preferredConnection, preferredMapping := "", ""
	if settings.ActiveProjectID == project.ID {
		preferredConnection, preferredMapping = settings.ActiveConnectionID, settings.ActiveMappingID
	}
	if idx < 0 {
		settings.Projects = append(settings.Projects, project)
	} else {
		settings.Projects[idx] = project
	}
	settings.ProjectName = project.Name
	settings.ActiveProjectID = project.ID
	settings.ActiveConnectionID, settings.ActiveMappingID = projectSelection(project, preferredConnection, preferredMapping)
	configFlatten(&settings)
	if err := s.store.Save(settings); err != nil {
		return Status{}, err
	}
	// A saved exclusion change invalidates previews prepared under the old policy.
	if idx >= 0 {
		for _, previous := range s.status.Settings.Projects[idx].Connections {
			found := false
			for _, current := range project.Connections {
				if previous.ID == current.ID {
					found = true
					if !slices.Equal(previous.Excludes, current.Excludes) {
						s.pending = nil
					}
					break
				}
			}
			if !found {
				s.pending = nil
			}
		}
	}
	s.status.Settings = cloneSettings(settings)
	return s.statusCopyLocked(), nil
}

func projectSelection(project config.Project, preferredConnection, preferredMapping string) (string, string) {
	if len(project.Connections) == 0 {
		return "", ""
	}
	connectionID := project.DefaultConnectionID
	for _, connection := range project.Connections {
		if connection.ID == preferredConnection {
			connectionID = preferredConnection
			break
		}
	}
	for _, connection := range project.Connections {
		if connection.ID == connectionID {
			for _, mapping := range connection.Mappings {
				if mapping.ID == preferredMapping {
					return connection.ID, mapping.ID
				}
			}
			return connection.ID, connection.Mappings[0].ID
		}
	}
	return project.Connections[0].ID, project.Connections[0].Mappings[0].ID
}

func cloneProject(p config.Project) config.Project {
	out := p
	out.Connections = make([]config.ProjectConnection, len(p.Connections))
	for i, c := range p.Connections {
		out.Connections[i] = c
		out.Connections[i].Mappings = append([]config.Mapping(nil), c.Mappings...)
		out.Connections[i].Excludes = append([]string(nil), c.Excludes...)
	}
	return out
}

func configFlatten(settings *config.Settings) {
	settings.Profile = nil
	for _, p := range settings.Projects {
		if p.ID != settings.ActiveProjectID {
			continue
		}
		for _, c := range p.Connections {
			if c.ID != settings.ActiveConnectionID {
				continue
			}
			for _, m := range c.Mappings {
				if m.ID == settings.ActiveMappingID {
					local := p.LocalRoot
					if filepath.Clean(m.LocalPath) != "." {
						local = filepath.Join(local, filepath.FromSlash(m.LocalPath))
					}
					base := filepath.ToSlash(filepath.Clean(m.LocalPath))
					if base == "." {
						base = ""
					}
					settings.Profile = &config.Profile{Name: p.Name, LocalRoot: local, RemoteRoot: m.RemotePath, Connection: c.Connection, Excludes: append([]string(nil), c.Excludes...), ExcludeBase: base}
					return
				}
			}
		}
	}
}

func (s *Service) SelectProject(projectID, connectionID, mappingID string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings := cloneSettings(s.status.Settings)
	var project *config.Project
	var connection *config.ProjectConnection
	var selected *config.Mapping
	for i := range settings.Projects {
		p := &settings.Projects[i]
		if p.ID == projectID {
			project = p
			if connectionID == "" && mappingID == "" {
				connectionID, mappingID = projectSelection(*p, "", "")
			}
			for j := range p.Connections {
				c := &p.Connections[j]
				if c.ID == connectionID {
					connection = c
					if mappingID == "" {
						mappingID = c.Mappings[0].ID
					}
					for k := range c.Mappings {
						m := &c.Mappings[k]
						if m.ID == mappingID {
							selected = m
							break
						}
					}
				}
			}
		}
	}
	if project == nil || len(project.Connections) > 0 && (connection == nil || selected == nil) || len(project.Connections) == 0 && (connectionID != "" || mappingID != "") {
		return Status{}, errors.New("проект, подключение или сопоставление не найдены")
	}
	settings.ActiveProjectID = projectID
	settings.ActiveConnectionID = connectionID
	settings.ActiveMappingID = mappingID
	settings.ProjectName = project.Name
	configFlatten(&settings)
	// Ensure the selected local subdirectory remains safely beneath its project root.
	root, err := mapping.New(project.LocalRoot, "/")
	if err != nil {
		return Status{}, err
	}
	if selected != nil {
		remote, err := root.LocalToRemote(selected.LocalPath)
		if err != nil {
			return Status{}, err
		}
		local, err := root.RemoteToLocal(remote)
		if err != nil {
			return Status{}, err
		}
		info, err := os.Stat(local)
		if err != nil {
			return Status{}, err
		}
		if !info.IsDir() {
			return Status{}, errors.New("выбранная локальная папка должна быть каталогом")
		}
	}
	if err = s.store.Save(settings); err != nil {
		return Status{}, err
	}
	s.status.Settings = cloneSettings(settings)
	return s.statusCopyLocked(), nil
}

func (s *Service) statusCopyLocked() Status {
	out := s.status
	out.Settings = cloneSettings(s.status.Settings)
	return out
}
