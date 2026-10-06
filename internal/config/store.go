// Package config stores portable application settings independently of the UI.
package config

import (
	"awesomeProject/internal/exclusion"
	"awesomeProject/internal/transport"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const SchemaVersion = 2

type Settings struct {
	SchemaVersion      int       `json:"schemaVersion"`
	ProjectName        string    `json:"projectName"`
	Profile            *Profile  `json:"profile,omitempty"`
	Projects           []Project `json:"projects"`
	ActiveProjectID    string    `json:"activeProjectID,omitempty"`
	ActiveConnectionID string    `json:"activeConnectionID,omitempty"`
	ActiveMappingID    string    `json:"activeMappingID,omitempty"`
}

type Project struct {
	ID                  string              `json:"id"`
	Name                string              `json:"name"`
	LocalRoot           string              `json:"localRoot"`
	Connections         []ProjectConnection `json:"connections"`
	DefaultConnectionID string              `json:"defaultConnectionID,omitempty"`
}
type ProjectConnection struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Connection transport.Connection `json:"connection"`
	Mappings   []Mapping            `json:"mappings"`
	Excludes   []string             `json:"excludes,omitempty"`
}
type Mapping struct {
	ID         string `json:"id"`
	LocalPath  string `json:"localPath"`
	RemotePath string `json:"remotePath"`
}

type Profile struct {
	Name        string               `json:"name"`
	LocalRoot   string               `json:"localRoot"`
	RemoteRoot  string               `json:"remoteRoot"`
	Connection  transport.Connection `json:"connection"`
	Excludes    []string             `json:"excludes,omitempty"`
	ExcludeBase string               `json:"excludeBase,omitempty"`
}

func Default() Settings { return Settings{SchemaVersion: SchemaVersion, Projects: []Project{}} }

func validateSettings(settings Settings) error {
	if settings.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported settings schema: %d", settings.SchemaVersion)
	}
	projects, connections, mappings := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, p := range settings.Projects {
		if strings.TrimSpace(p.ID) == "" || projects[p.ID] {
			return errors.New("invalid or duplicate project ID")
		}
		projects[p.ID] = true
		if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.LocalRoot) == "" || !filepath.IsAbs(p.LocalRoot) {
			return errors.New("invalid project structure")
		}
		defaultExists := p.DefaultConnectionID == ""
		for _, c := range p.Connections {
			if c.ID == p.DefaultConnectionID {
				defaultExists = true
			}
			if err := exclusion.Validate(c.Excludes); err != nil {
				return fmt.Errorf("подключение %q: %w", c.Name, err)
			}
			if strings.TrimSpace(c.ID) == "" || connections[c.ID] {
				return errors.New("invalid or duplicate connection ID")
			}
			connections[c.ID] = true
			if strings.TrimSpace(c.Name) == "" || len(c.Mappings) == 0 {
				return errors.New("invalid connection structure")
			}
			for _, m := range c.Mappings {
				if strings.TrimSpace(m.ID) == "" || mappings[m.ID] {
					return errors.New("invalid or duplicate mapping ID")
				}
				mappings[m.ID] = true
				if strings.TrimSpace(m.LocalPath) == "" || filepath.IsAbs(m.LocalPath) || filepath.VolumeName(m.LocalPath) != "" || strings.TrimSpace(m.RemotePath) == "" || !path.IsAbs(m.RemotePath) || strings.ContainsRune(m.RemotePath, '\\') || hasDotDot(m.LocalPath) || hasDotDot(m.RemotePath) {
					return errors.New("invalid mapping structure")
				}
			}
		}
		if !defaultExists {
			return errors.New("default connection references an unknown connection")
		}
	}
	selected := settings.ActiveProjectID != "" || settings.ActiveConnectionID != "" || settings.ActiveMappingID != ""
	if !selected {
		if len(settings.Projects) > 0 {
			return errors.New("projects exist without an active selection")
		}
		return nil
	}
	if settings.ActiveProjectID == "" {
		return errors.New("incomplete active selection")
	}
	for _, p := range settings.Projects {
		if p.ID == settings.ActiveProjectID {
			if len(p.Connections) == 0 && settings.ActiveConnectionID == "" && settings.ActiveMappingID == "" {
				return nil
			}
			if settings.ActiveConnectionID == "" || settings.ActiveMappingID == "" {
				return errors.New("incomplete active selection")
			}
			for _, c := range p.Connections {
				if c.ID == settings.ActiveConnectionID {
					for _, m := range c.Mappings {
						if m.ID == settings.ActiveMappingID {
							return nil
						}
					}
				}
			}
		}
	}
	return errors.New("active selection references an unknown project, connection, or mapping")
}

// Missing defaults in older schema-2 settings become first-connection defaults
// in memory. Loading never writes a migration or changes the active selection.
func normalizeDefaults(settings *Settings) {
	for i := range settings.Projects {
		project := &settings.Projects[i]
		if project.DefaultConnectionID == "" && len(project.Connections) > 0 {
			project.DefaultConnectionID = project.Connections[0].ID
		}
	}
}

func hasDotDot(p string) bool {
	for _, segment := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if segment == ".." {
			return true
		}
	}
	return false
}

func flatten(settings *Settings) {
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
						local = filepath.Join(local, filepath.Clean(m.LocalPath))
					}
					base := filepath.ToSlash(filepath.Clean(m.LocalPath))
					if base == "." {
						base = ""
					}
					settings.Profile = &Profile{Name: p.Name, LocalRoot: local, RemoteRoot: m.RemotePath, Connection: c.Connection, Excludes: append([]string(nil), c.Excludes...), ExcludeBase: base}
					return
				}
			}
		}
	}
}

func migrateLegacy(data []byte) (Settings, error) {
	var old struct {
		SchemaVersion int      `json:"schemaVersion"`
		ProjectName   string   `json:"projectName"`
		Profile       *Profile `json:"profile,omitempty"`
	}
	if err := json.Unmarshal(data, &old); err != nil {
		return Settings{}, err
	}
	s := Default()
	s.ProjectName = old.ProjectName
	s.Profile = old.Profile
	if old.Profile != nil {
		s.Projects = []Project{{ID: "legacy-project", Name: old.Profile.Name, LocalRoot: old.Profile.LocalRoot, Connections: []ProjectConnection{{ID: "legacy-connection", Name: "Основное подключение", Connection: old.Profile.Connection, Mappings: []Mapping{{ID: "legacy-mapping", LocalPath: ".", RemotePath: old.Profile.RemoteRoot}}}}}}
		s.ActiveProjectID = "legacy-project"
		s.ActiveConnectionID = "legacy-connection"
		s.ActiveMappingID = "legacy-mapping"
	}
	return s, nil
}

func DataDir(executable string) (string, error) {
	if !filepath.IsAbs(executable) {
		return "", errors.New("путь приложения должен быть абсолютным")
	}
	return filepath.Join(filepath.Dir(executable), "data"), nil
}

type Store struct{ dir string }

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("перенесите приложение в папку, доступную для записи: %w", err)
	}
	f, err := os.CreateTemp(dir, ".write-check-*")
	if err != nil {
		return nil, fmt.Errorf("перенесите приложение в папку, доступную для записи: %w", err)
	}
	name := f.Name()
	closeErr := f.Close()
	removeErr := os.Remove(name)
	if err := errors.Join(closeErr, removeErr); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path() string { return filepath.Join(s.dir, "settings.json") }

func decode(data []byte) (Settings, error) {
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{}, fmt.Errorf("повреждён settings.json; сохраните файл и восстановите settings.json.bak вручную: %w", err)
	}
	if settings.SchemaVersion == 1 {
		var err error
		settings, err = migrateLegacy(data)
		if err != nil {
			return Settings{}, err
		}
	} else if settings.SchemaVersion != SchemaVersion {
		return Settings{}, fmt.Errorf("неподдерживаемая версия настроек: %d", settings.SchemaVersion)
	}
	if settings.Projects == nil {
		settings.Projects = []Project{}
	}
	if err := validateSettings(settings); err != nil {
		return Settings{}, fmt.Errorf("invalid settings structure: %w", err)
	}
	normalizeDefaults(&settings)
	flatten(&settings)
	return settings, nil
}

func (s *Store) Load() (Settings, error) {
	data, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	return decode(data)
}

func (s *Store) Save(settings Settings) error {
	if err := validateSettings(settings); err != nil {
		return err
	}
	if settings.SchemaVersion != SchemaVersion {
		return errors.New("неподдерживаемая версия настроек")
	}
	settings.Projects = append([]Project(nil), settings.Projects...)
	normalizeDefaults(&settings)
	old, err := os.ReadFile(s.path())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if _, err := decode(old); err != nil {
			return err
		}
		backup := "settings.json.bak"
		var version struct {
			SchemaVersion int `json:"schemaVersion"`
		}
		_ = json.Unmarshal(old, &version)
		if err := s.replace(backup, old); err != nil {
			return fmt.Errorf("резервная копия настроек: %w", err)
		}
		if version.SchemaVersion == 1 {
			if err := s.replace("settings.json.v1.bak", old); err != nil {
				return fmt.Errorf("legacy settings backup: %w", err)
			}
		}
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return s.replace("settings.json", append(data, '\n'))
}

func (s *Store) replace(name string, data []byte) error {
	f, err := os.CreateTemp(s.dir, ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(s.dir, name))
}
