package application

import (
	"awesomeProject/internal/gitstatus"
	"context"
)

func (s *Service) GetGitStatus(ctx context.Context, projectID string) (gitstatus.Snapshot, error) {
	project, _, err := s.previewProject(projectID)
	if err != nil {
		return gitstatus.Snapshot{}, err
	}
	return gitstatus.Read(ctx, project.LocalRoot)
}
