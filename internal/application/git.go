package application

import (
	"awesomeProject/internal/gitstatus"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"time"
)

func (s *Service) GetGitStatus(ctx context.Context, projectID string) (gitstatus.Snapshot, error) {
	project, _, err := s.previewProject(projectID)
	if err != nil {
		return gitstatus.Snapshot{}, err
	}
	return gitstatus.Read(ctx, project.LocalRoot)
}

func (s *Service) GetGitBranches(ctx context.Context, projectID string) ([]gitstatus.Branch, error) {
	project, _, err := s.previewProject(projectID)
	if err != nil {
		return nil, err
	}
	return gitstatus.Branches(ctx, project.LocalRoot)
}

type gitBlobInfo struct {
	name string
	size int64
}

func (i gitBlobInfo) Name() string       { return i.name }
func (i gitBlobInfo) Size() int64        { return i.size }
func (i gitBlobInfo) Mode() os.FileMode  { return 0644 }
func (i gitBlobInfo) ModTime() time.Time { return time.Time{} }
func (i gitBlobInfo) IsDir() bool        { return false }
func (i gitBlobInfo) Sys() any           { return nil }

func (s *Service) CompareGitFile(ctx context.Context, projectID, relative, ref string) (FileComparison, error) {
	project, _, err := s.previewProject(projectID)
	if err != nil {
		return FileComparison{}, err
	}
	local, err := readLocalPreview(project, relative, nil)
	if err != nil {
		return FileComparison{}, err
	}
	data, err := gitstatus.ReadBlob(ctx, project.LocalRoot, ref, filepath.ToSlash(relative))
	if err != nil {
		return FileComparison{}, err
	}
	remote, err := previewContent(bytes.NewReader(data), gitBlobInfo{filepath.Base(relative), int64(len(data))})
	if err != nil {
		return FileComparison{}, err
	}
	remote.ProjectID, remote.RelativePath, remote.Path, remote.Server = projectID, relative, ref+":"+relative, "Git · "+ref
	full := !local.Truncated && !remote.Truncated
	return FileComparison{Local: local, Remote: remote, Equal: full && local.hash == remote.hash, Comparable: full && !local.Binary && !remote.Binary}, nil
}
