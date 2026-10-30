package application

import (
	"awesomeProject/internal/config"
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Marks belong to project search, independently of deployment exclusions.
// The walker prunes marked ancestors, so an unmarked child cannot bypass them.
func directorySearchExcluded(project config.Project, relative string) bool {
	relative = path.Clean(filepath.ToSlash(relative))
	for _, excluded := range project.SearchExcludedDirectories {
		if localPathContains(excluded, relative) {
			return true
		}
	}
	if !project.ExcludeDotDirectories || relative == "." {
		return false
	}
	current := ""
	for _, segment := range strings.Split(relative, "/") {
		current = path.Join(current, segment)
		if !strings.HasPrefix(segment, ".") {
			continue
		}
		included := slices.ContainsFunc(project.SearchIncludedDirectories, func(value string) bool { return localPathContains(value, current) && localPathContains(current, value) })
		if !included {
			return true
		}
	}
	return false
}

func (s *Service) SetDirectorySearchExcluded(projectID, relative string, excluded bool) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings := cloneSettings(s.status.Settings)
	index := slices.IndexFunc(settings.Projects, func(p config.Project) bool { return p.ID == projectID })
	if index < 0 {
		return Status{}, errors.New("проект не найден")
	}
	project := &settings.Projects[index]
	filename, err := localPreviewPath(*project, relative)
	if err != nil {
		return Status{}, err
	}
	relative, err = filepath.Rel(project.LocalRoot, filename)
	if err != nil {
		return Status{}, err
	}
	relative = filepath.ToSlash(relative)
	if relative == "." {
		return Status{}, errors.New("корень проекта нельзя исключить из поиска")
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return Status{}, err
	}
	if !info.IsDir() {
		return Status{}, errors.New("выберите обычную локальную папку")
	}
	same := func(value string) bool {
		return localPathContains(value, relative) && localPathContains(relative, value)
	}
	project.SearchExcludedDirectories = slices.DeleteFunc(project.SearchExcludedDirectories, same)
	project.SearchIncludedDirectories = slices.DeleteFunc(project.SearchIncludedDirectories, same)
	if excluded {
		project.SearchExcludedDirectories = append(project.SearchExcludedDirectories, relative)
	} else {
		if directorySearchExcluded(*project, path.Dir(relative)) {
			return Status{}, errors.New("сначала снимите исключение с родительской папки")
		}
		if project.ExcludeDotDirectories && strings.HasPrefix(path.Base(relative), ".") {
			project.SearchIncludedDirectories = append(project.SearchIncludedDirectories, relative)
		}
	}
	if err := s.store.Save(settings); err != nil {
		return Status{}, err
	}
	s.status.Settings = settings
	return s.statusCopyLocked(), nil
}
