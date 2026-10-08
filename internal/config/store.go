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
	"unicode"
)

const SchemaVersion = 3

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
	ID                        string              `json:"id"`
	Name                      string              `json:"name"`
	LocalRoot                 string              `json:"localRoot"`
	Connections               []ProjectConnection `json:"connections"`
	DefaultConnectionID       string              `json:"defaultConnectionID,omitempty"`
	ExcludeDotDirectories     bool                `json:"excludeDotDirectories,omitempty"`
	SearchExcludedDirectories []string            `json:"searchExcludedDirectories,omitempty"`
	SearchIncludedDirectories []string            `json:"searchIncludedDirectories,omitempty"`
}
type ProjectConnection struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Connection transport.Connection `json:"connection"`
	RootPath   string               `json:"rootPath"`
	Mappings   []Mapping            `json:"mappings"`
	Excludes   []string             `json:"excludes,omitempty"`
}
type Mapping struct {
	ID             string `json:"id"`
	LocalPath      string `json:"localPath"`
	DeploymentPath string `json:"deploymentPath"`
	RemotePath     string `json:"remotePath,omitempty"` // Deprecated schema-1/2 compatibility.
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

// NormalizeProject migrates legacy absolute mapping destinations into a
// connection root plus paths relative to that root. It returns a copy and
// clears deprecated RemotePath values so subsequent saves are canonical.
func NormalizeProject(project Project) (Project, error) {
	project.SearchExcludedDirectories = append([]string(nil), project.SearchExcludedDirectories...)
	project.SearchIncludedDirectories = append([]string(nil), project.SearchIncludedDirectories...)
	for _, directories := range [][]string{project.SearchExcludedDirectories, project.SearchIncludedDirectories} {
		if len(directories) > 1000 {
			return Project{}, errors.New("too many search directory exclusions")
		}
		for _, directory := range directories {
			if directory == "." || !validDeploymentPath(directory) {
				return Project{}, fmt.Errorf("invalid search directory path %q", directory)
			}
		}
	}
	project.Connections = append([]ProjectConnection(nil), project.Connections...)
	for ci := range project.Connections {
		connection := &project.Connections[ci]
		connection.Mappings = append([]Mapping(nil), connection.Mappings...)
		legacy := false
		for _, m := range connection.Mappings {
			if m.RemotePath != "" {
				legacy = true
				break
			}
		}
		if legacy {
			remotePaths := make([]string, len(connection.Mappings))
			for i, m := range connection.Mappings {
				if strings.TrimSpace(m.RemotePath) == "" || !validAbsolutePOSIX(m.RemotePath) {
					return Project{}, fmt.Errorf("connection %q has invalid legacy remote path", connection.Name)
				}
				remotePaths[i] = path.Clean(m.RemotePath)
			}
			if connection.RootPath == "" {
				connection.RootPath = commonRemoteAncestor(remotePaths)
			} else if err := ValidateRootPath(connection.RootPath); err != nil {
				return Project{}, fmt.Errorf("connection %q: %w", connection.Name, err)
			}
			root := path.Clean(connection.RootPath)
			for i := range connection.Mappings {
				remote := remotePaths[i]
				deployment := ""
				if remote == root {
					deployment = "."
				} else if root == "/" {
					deployment = strings.TrimPrefix(remote, "/")
				} else if strings.HasPrefix(remote, root+"/") {
					deployment = strings.TrimPrefix(remote, root+"/")
				} else {
					return Project{}, fmt.Errorf("connection %q legacy remote path %q is outside rootPath %q", connection.Name, remote, root)
				}
				connection.Mappings[i].DeploymentPath = deployment
			}
		}
		for i := range connection.Mappings {
			m := &connection.Mappings[i]
			if m.DeploymentPath == "" {
				m.DeploymentPath = "."
			}
			m.RemotePath = ""
		}
		if connection.RootPath == "" && len(connection.Mappings) == 0 {
			connection.RootPath = "/"
		}
		if err := ValidateRootPath(connection.RootPath); err != nil {
			return Project{}, fmt.Errorf("connection %q: %w", connection.Name, err)
		}
	}
	return project, nil
}

// ValidateRootPath accepts absolute POSIX server paths only.
func ValidateRootPath(rootPath string) error {
	if !validAbsolutePOSIX(rootPath) {
		return errors.New("server root path must be an absolute POSIX path")
	}
	return nil
}

// ResolveDeploymentPath joins a server root with a safe relative deployment
// path. The returned path is always absolute and remains under rootPath.
func ResolveDeploymentPath(rootPath, deploymentPath string) (string, error) {
	if err := ValidateRootPath(rootPath); err != nil {
		return "", err
	}
	if !validDeploymentPath(deploymentPath) {
		return "", errors.New("deployment path must be a safe relative POSIX path")
	}
	rootPath = path.Clean(rootPath)
	if deploymentPath == "." {
		return rootPath, nil
	}
	return path.Join(rootPath, deploymentPath), nil
}

func validAbsolutePOSIX(value string) bool {
	if !path.IsAbs(value) || strings.ContainsRune(value, '\\') || hasDotDot(value) || hasControl(value) {
		return false
	}
	return path.Clean(value) == value || strings.TrimSuffix(value, "/") == path.Clean(value)
}

func validDeploymentPath(value string) bool {
	if value == "." {
		return true
	}
	if value == "" || path.IsAbs(value) || strings.ContainsRune(value, '\\') || hasDotDot(value) || hasControl(value) {
		return false
	}
	return path.Clean(value) == value && !strings.HasPrefix(value, "../")
}

func hasControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func commonRemoteAncestor(paths []string) string {
	if len(paths) == 1 {
		return paths[0]
	}
	segments := strings.Split(strings.TrimPrefix(paths[0], "/"), "/")
	for _, candidate := range paths[1:] {
		other := strings.Split(strings.TrimPrefix(candidate, "/"), "/")
		limit := len(segments)
		if len(other) < limit {
			limit = len(other)
		}
		i := 0
		for i < limit && segments[i] == other[i] {
			i++
		}
		segments = segments[:i]
	}
	if len(segments) == 0 {
		return "/"
	}
	return "/" + strings.Join(segments, "/")
}

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
			if strings.TrimSpace(c.Name) == "" {
				return errors.New("invalid connection structure")
			}
			if err := ValidateRootPath(c.RootPath); err != nil {
				return fmt.Errorf("connection %q: %w", c.Name, err)
			}
			for _, m := range c.Mappings {
				if strings.TrimSpace(m.ID) == "" || mappings[m.ID] {
					return errors.New("invalid or duplicate mapping ID")
				}
				mappings[m.ID] = true
				if strings.TrimSpace(m.LocalPath) == "" || filepath.IsAbs(m.LocalPath) || filepath.VolumeName(m.LocalPath) != "" || hasDotDot(m.LocalPath) || m.RemotePath != "" {
					return errors.New("invalid mapping structure")
				}
				if _, err := ResolveDeploymentPath(c.RootPath, m.DeploymentPath); err != nil {
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
			if settings.ActiveConnectionID == "" {
				return errors.New("incomplete active selection")
			}
			for _, c := range p.Connections {
				if c.ID == settings.ActiveConnectionID {
					if len(c.Mappings) == 0 {
						if settings.ActiveMappingID == "" {
							return nil
						}
						return errors.New("active selection references an unknown mapping")
					}
					if settings.ActiveMappingID == "" {
						return errors.New("incomplete active selection")
					}
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
					remote, err := ResolveDeploymentPath(c.RootPath, m.DeploymentPath)
					if err != nil {
						return
					}
					settings.Profile = &Profile{Name: p.Name, LocalRoot: local, RemoteRoot: remote, Connection: c.Connection, Excludes: append([]string(nil), c.Excludes...), ExcludeBase: base}
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
		s.Projects = []Project{{ID: "legacy-project", Name: old.Profile.Name, LocalRoot: old.Profile.LocalRoot, Connections: []ProjectConnection{{ID: "legacy-connection", Name: "Основное подключение", Connection: old.Profile.Connection, RootPath: old.Profile.RemoteRoot, Mappings: []Mapping{{ID: "legacy-mapping", LocalPath: ".", DeploymentPath: "."}}}}}}
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
	} else if settings.SchemaVersion == 2 {
		for i := range settings.Projects {
			project, err := NormalizeProject(settings.Projects[i])
			if err != nil {
				return Settings{}, fmt.Errorf("invalid schema-2 project: %w", err)
			}
			settings.Projects[i] = project
		}
		settings.SchemaVersion = SchemaVersion
	} else if settings.SchemaVersion != SchemaVersion {
		return Settings{}, fmt.Errorf("неподдерживаемая версия настроек: %d", settings.SchemaVersion)
	}
	if settings.SchemaVersion == SchemaVersion {
		for i := range settings.Projects {
			project, err := NormalizeProject(settings.Projects[i])
			if err != nil {
				return Settings{}, fmt.Errorf("invalid project: %w", err)
			}
			settings.Projects[i] = project
		}
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
	settings.Projects = append([]Project(nil), settings.Projects...)
	for i := range settings.Projects {
		project, err := NormalizeProject(settings.Projects[i])
		if err != nil {
			return err
		}
		settings.Projects[i] = project
	}
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
		if version.SchemaVersion == 1 || version.SchemaVersion == 2 {
			if err := s.replace(fmt.Sprintf("settings.json.v%d.bak", version.SchemaVersion), old); err != nil {
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
