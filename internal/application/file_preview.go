package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/exclusion"
	"awesomeProject/internal/mapping"
	"awesomeProject/internal/transfer"
	"awesomeProject/internal/transport"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

const maxPreviewBytes = 2 * 1024 * 1024

type FileContent struct {
	ProjectID    string `json:"projectID"`
	ConnectionID string `json:"connectionID"`
	Server       string `json:"server"`
	Name         string `json:"name"`
	RelativePath string `json:"relativePath"`
	Path         string `json:"path"`
	Text         string `json:"text"`
	Size         int64  `json:"size"`
	Binary       bool   `json:"binary"`
	Truncated    bool   `json:"truncated"`
	Encoding     string `json:"encoding"`
	hash         [32]byte
}

type FileComparison struct {
	Local      FileContent `json:"local"`
	Remote     FileContent `json:"remote"`
	Equal      bool        `json:"equal"`
	Comparable bool        `json:"comparable"`
}

func (s *Service) previewProject(projectID string) (config.Project, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.status.Settings.Projects {
		if p.ID == projectID {
			selected := ""
			if projectID == s.status.Settings.ActiveProjectID {
				selected = s.status.Settings.ActiveConnectionID
			}
			return cloneProject(p), selected, nil
		}
	}
	return config.Project{}, "", errors.New("проект не найден")
}

func previewConnection(project config.Project, id string) (config.ProjectConnection, error) {
	for _, c := range project.Connections {
		if c.ID == id {
			return c, nil
		}
	}
	return config.ProjectConnection{}, errors.New("сервер проекта не найден")
}

func localPreviewPath(project config.Project, relative string) (string, error) {
	mapper, err := mapping.New(project.LocalRoot, "/")
	if err != nil {
		return "", err
	}
	remote, err := mapper.LocalToRemote(relative)
	if err != nil {
		return "", err
	}
	return mapper.RemoteToLocal(remote)
}

func previewContent(reader io.Reader, info os.FileInfo) (FileContent, error) {
	if !info.Mode().IsRegular() {
		return FileContent{}, errors.New("предпросмотр доступен только для обычных файлов")
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxPreviewBytes+1))
	if err != nil {
		return FileContent{}, err
	}
	result := FileContent{Name: info.Name(), Size: info.Size(), Truncated: len(data) > maxPreviewBytes, hash: sha256.Sum256(data)}
	if result.Truncated {
		data = data[:maxPreviewBytes]
	}
	if len(data) >= 2 && ((data[0] == 0xff && data[1] == 0xfe) || (data[0] == 0xfe && data[1] == 0xff)) {
		var order binary.ByteOrder = binary.LittleEndian
		result.Encoding = "UTF-16 LE"
		if data[0] == 0xfe {
			order = binary.BigEndian
			result.Encoding = "UTF-16 BE"
		}
		data = data[2:]
		if len(data)%2 != 0 {
			result.Binary = true
			return result, nil
		}
		units := make([]uint16, len(data)/2)
		for i := range units {
			units[i] = order.Uint16(data[i*2:])
		}
		result.Text = string(utf16.Decode(units))
	} else {
		data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
		// A size cut may split the final UTF-8 rune, but invalid bytes elsewhere
		// must not silently be replaced or presented as a reliable text diff.
		if result.Truncated {
			for i := 0; i < 3 && len(data) > 0 && !utf8.Valid(data); i++ {
				data = data[:len(data)-1]
			}
		}
		result.Binary = bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)
		if !result.Binary {
			result.Encoding = "UTF-8"
			result.Text = string(data)
		}
	}
	if strings.IndexByte(result.Text, 0) >= 0 {
		result.Binary = true
		result.Text = ""
	}
	return result, nil
}

func readLocalPreview(project config.Project, relative string, excludes []string) (FileContent, error) {
	filename, err := localPreviewPath(project, relative)
	if err != nil {
		return FileContent{}, err
	}
	relative = filepath.ToSlash(relative)
	if exclusion.Match(excludes, relative) {
		return FileContent{}, fmt.Errorf("путь %q исключён для этого подключения", relative)
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return FileContent{}, err
	}
	if !info.Mode().IsRegular() {
		return FileContent{}, errors.New("предпросмотр доступен только для обычных файлов")
	}
	file, err := os.Open(filename)
	if err != nil {
		return FileContent{}, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return FileContent{}, err
	}
	result, err := previewContent(file, info)
	result.ProjectID, result.RelativePath, result.Path = project.ID, path.Clean(relative), filename
	return result, err
}

func (s *Service) ReadLocalFile(projectID, relative string) (FileContent, error) {
	project, selected, err := s.previewProject(projectID)
	if err != nil {
		return FileContent{}, err
	}
	var excludes []string
	if c, err := previewConnection(project, selected); err == nil {
		excludes = c.Excludes
	}
	return readLocalPreview(project, relative, excludes)
}

func (s *Service) readServerPreview(ctx context.Context, project config.Project, connection config.ProjectConnection, relative string) (FileContent, error) {
	mapper, err := mapping.New(project.LocalRoot, connection.RootPath)
	if err != nil {
		return FileContent{}, err
	}
	remote, err := mapper.RemoteRelativeToAbsolute(relative)
	if err != nil {
		return FileContent{}, err
	}
	if remoteRootEntryExcluded(connection, relative) {
		return FileContent{}, fmt.Errorf("путь %q исключён для этого подключения", relative)
	}
	var result FileContent
	action := func(session *transport.Session) error {
		if err := transfer.CheckRemotePath(session.Client, path.Dir(remote)); err != nil {
			return err
		}
		info, err := session.Client.Lstat(remote)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("предпросмотр доступен только для обычных файлов")
		}
		file, err := session.Client.Open(remote)
		if err != nil {
			return err
		}
		defer file.Close()
		result, err = previewContent(file, info)
		result.ProjectID, result.ConnectionID, result.Server = project.ID, connection.ID, connection.Name
		result.RelativePath, result.Path = path.Clean(relative), remote
		return err
	}
	s.mu.Lock()
	key := s.navigationKeyLocked()
	s.mu.Unlock()
	if key.projectID == project.ID && key.connectionID == connection.ID && key.connection == connection.Connection {
		err = s.navigationOperation(ctx, key, action)
	} else {
		profile := config.Profile{LocalRoot: project.LocalRoot, RemoteRoot: connection.RootPath, Connection: connection.Connection}
		err = s.remoteOperation(ctx, profile, 2*time.Minute, func(_ context.Context, session *transport.Session) error { return action(session) })
	}
	return result, err
}

func (s *Service) ReadRemoteFile(ctx context.Context, projectID, connectionID, relative string) (FileContent, error) {
	project, _, err := s.previewProject(projectID)
	if err != nil {
		return FileContent{}, err
	}
	connection, err := previewConnection(project, connectionID)
	if err != nil {
		return FileContent{}, err
	}
	return s.readServerPreview(ctx, project, connection, relative)
}

func deployedPreviewPath(project config.Project, connection config.ProjectConnection, relative string) (string, error) {
	filename, err := localPreviewPath(project, relative)
	if err != nil {
		return "", err
	}
	projectPath, err := filepath.Rel(project.LocalRoot, filename)
	if err != nil {
		return "", err
	}
	projectPath = filepath.ToSlash(projectPath)
	best, found := -1, ""
	for _, m := range connection.Mappings {
		base := path.Clean(filepath.ToSlash(m.LocalPath))
		if base != "." && projectPath != base && !strings.HasPrefix(projectPath, base+"/") {
			continue
		}
		suffix := projectPath
		if base != "." {
			suffix = strings.TrimPrefix(strings.TrimPrefix(projectPath, base), "/")
		}
		if len(base) > best {
			best, found = len(base), path.Join(m.DeploymentPath, suffix)
		}
	}
	if best < 0 {
		return "", errors.New("файл находится вне маппингов выбранного сервера")
	}
	return found, nil
}

func (s *Service) CompareFile(ctx context.Context, projectID, connectionID, relative string) (FileComparison, error) {
	project, _, err := s.previewProject(projectID)
	if err != nil {
		return FileComparison{}, err
	}
	connection, err := previewConnection(project, connectionID)
	if err != nil {
		return FileComparison{}, err
	}
	remotePath, err := deployedPreviewPath(project, connection, relative)
	if err != nil {
		return FileComparison{}, err
	}
	local, err := readLocalPreview(project, relative, connection.Excludes)
	if err != nil {
		return FileComparison{}, err
	}
	remote, err := s.readServerPreview(ctx, project, connection, remotePath)
	if err != nil {
		return FileComparison{}, err
	}
	full := !local.Truncated && !remote.Truncated
	return FileComparison{Local: local, Remote: remote, Equal: full && local.hash == remote.hash, Comparable: full && !local.Binary && !remote.Binary}, nil
}
