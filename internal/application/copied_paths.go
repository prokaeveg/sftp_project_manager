package application

import (
	"awesomeProject/internal/config"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

func cleanCopiedPath(relative, side string) (string, error) {
	if side == "local" {
		relative = filepath.ToSlash(relative)
	}
	if (len(relative) > 1 && relative[1] == ':') || relative == "" || path.IsAbs(relative) || filepath.VolumeName(relative) != "" || strings.Contains(relative, "\\") {
		return "", errors.New("путь должен находиться внутри корня проекта или сервера")
	}
	for _, part := range strings.Split(relative, "/") {
		if part == ".." {
			return "", errors.New("путь выходит за пределы корня")
		}
	}
	for _, ch := range relative {
		if unicode.IsControl(ch) {
			return "", errors.New("путь содержит управляющие символы")
		}
	}
	return path.Clean(relative), nil
}
func mappedProjectPath(connection config.ProjectConnection, relative string) (string, error) {
	best, result := -1, ""
	for _, m := range connection.Mappings {
		base := path.Clean(m.DeploymentPath)
		if base != "." && relative != base && !strings.HasPrefix(relative, base+"/") {
			continue
		}
		suffix := relative
		if base != "." {
			suffix = strings.TrimPrefix(strings.TrimPrefix(relative, base), "/")
		}
		if len(base) > best {
			best = len(base)
			result = path.Join(filepath.ToSlash(m.LocalPath), suffix)
		}
	}
	if best < 0 {
		return "", errors.New("путь находится вне маппингов исходного сервера")
	}
	return result, nil
}
func mappedServerPath(connection config.ProjectConnection, relative string) (string, error) {
	best, result := -1, ""
	for _, m := range connection.Mappings {
		base := path.Clean(filepath.ToSlash(m.LocalPath))
		if base != "." && relative != base && !strings.HasPrefix(relative, base+"/") {
			continue
		}
		suffix := relative
		if base != "." {
			suffix = strings.TrimPrefix(strings.TrimPrefix(relative, base), "/")
		}
		if len(base) > best {
			best = len(base)
			result = path.Join(m.DeploymentPath, suffix)
		}
	}
	if best < 0 {
		return "", errors.New("путь находится вне маппингов выбранного сервера")
	}
	return config.ResolveDeploymentPath(connection.RootPath, result)
}

// CopiedPaths resolves names only: no filesystem reads, SSH, transfer or settings writes.
// All paths must resolve before the caller replaces the clipboard.
func (s *Service) CopiedPaths(projectID, side, sourceID, targetID string, relatives []string, kind string) (string, error) {
	if side != "local" && side != "remote" {
		return "", errors.New("неизвестная сторона дерева")
	}
	if kind != "local" && kind != "project" && kind != "server" {
		return "", errors.New("неизвестный вид пути")
	}
	if len(relatives) == 0 || len(relatives) > 1000 {
		return "", errors.New("выберите от 1 до 1000 файлов или папок")
	}
	project, _, err := s.previewProject(projectID)
	if err != nil {
		return "", err
	}
	var source, target config.ProjectConnection
	if side == "remote" {
		source, err = previewConnection(project, sourceID)
		if err != nil {
			return "", err
		}
	}
	if kind == "server" {
		if targetID == "" {
			if side == "remote" {
				targetID = sourceID
			} else {
				targetID = project.DefaultConnectionID
			}
		}
		target, err = previewConnection(project, targetID)
		if err != nil {
			return "", errors.New("добавьте или выберите сервер для копирования пути")
		}
	}
	result := make([]string, 0, len(relatives))
	size := 0
	for _, relative := range relatives {
		relative, err = cleanCopiedPath(relative, side)
		if err != nil {
			return "", err
		}
		value := ""
		if side == "remote" && kind == "server" && source.ID == target.ID {
			value, err = config.ResolveDeploymentPath(source.RootPath, relative)
		} else {
			projectPath := relative
			if side == "remote" {
				projectPath, err = mappedProjectPath(source, relative)
				if err != nil {
					return "", err
				}
			}
			projectPath, err = cleanCopiedPath(projectPath, "local")
			if err != nil {
				return "", err
			}
			switch kind {
			case "project":
				value = projectPath
			case "local":
				value = filepath.Join(project.LocalRoot, filepath.FromSlash(projectPath))
			case "server":
				value, err = mappedServerPath(target, projectPath)
			}
		}
		if err != nil {
			return "", fmt.Errorf("не удалось скопировать путь: %w", err)
		}
		size += len(value) + 1
		if size > 1024*1024 {
			return "", errors.New("список путей превышает 1 МиБ")
		}
		result = append(result, value)
	}
	return strings.Join(result, "\n"), nil
}
