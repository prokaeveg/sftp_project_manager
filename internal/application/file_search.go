package application

import (
	"awesomeProject/internal/exclusion"
	"awesomeProject/internal/mapping"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type FileSearchResult struct {
	Files   []FileEntry `json:"files"`
	Limited bool        `json:"limited"`
}

func filenameScore(name, query string) int {
	name = strings.ToLower(name)
	if name == query {
		return 0
	}
	if strings.HasPrefix(name, query) {
		return 1
	}
	if strings.Contains(name, query) {
		return 2
	}
	chars := []rune(query)
	i := 0
	for _, c := range name {
		if i < len(chars) && c == chars[i] {
			i++
		}
	}
	if i == len(chars) {
		return 3
	}
	return -1
}

func (s *Service) SearchProjectFiles(parent context.Context, projectID, query string) (FileSearchResult, error) {
	result := FileSearchResult{Files: []FileEntry{}}
	if err := parent.Err(); err != nil {
		return result, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if len([]rune(query)) > 120 {
		return result, errors.New("поисковый запрос должен быть до 120 символов")
	}
	project, selected, err := s.previewProject(projectID)
	if err != nil {
		return result, err
	}
	mapper, err := mapping.New(project.LocalRoot, "/")
	if err != nil {
		return result, err
	}
	var excludes []string
	if c, err := previewConnection(project, selected); err == nil {
		excludes = c.Excludes
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	visited := 0
	err = filepath.WalkDir(project.LocalRoot, func(filename string, entry fs.DirEntry, walkErr error) error {
		if parent.Err() != nil {
			return parent.Err()
		}
		if ctx.Err() != nil || visited >= 50000 {
			result.Limited = true
			return fs.SkipAll
		}
		visited++
		if walkErr != nil {
			result.Limited = true
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(project.LocalRoot, filename)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == "." {
			return nil
		}
		if entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 || exclusion.Match(excludes, relative) || (entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules")) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if _, err := mapper.LocalToRemote(relative); err != nil {
				return fs.SkipDir
			}
			return nil
		}
		score := filenameScore(entry.Name(), query)
		if score < 0 {
			return nil
		}
		if _, err := mapper.LocalToRemote(relative); err != nil {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		result.Files = append(result.Files, FileEntry{Name: entry.Name(), RelativePath: relative, Size: info.Size()})
		sort.Slice(result.Files, func(i, j int) bool {
			a, b := result.Files[i], result.Files[j]
			sa, sb := filenameScore(a.Name, query), filenameScore(b.Name, query)
			if sa != sb {
				return sa < sb
			}
			return a.RelativePath < b.RelativePath
		})
		if len(result.Files) > 100 {
			result.Files = result.Files[:100]
			result.Limited = true
		}
		return nil
	})
	return result, err
}
