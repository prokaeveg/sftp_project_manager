package application

import (
	"awesomeProject/internal/config"
	"context"
	"errors"
	"os"
	"path"
	"slices"
)

// A drop explicitly supplies both locations. Deployment mappings are not used.
func dropTransferProfile(project config.Project, connection config.ProjectConnection, direction, source, target string) (config.Profile, string, error) {
	if direction != "upload" && direction != "download" {
		return config.Profile{}, "", errors.New("неизвестное направление передачи")
	}
	sourceSide, targetSide := "local", "remote"
	if direction == "download" {
		sourceSide, targetSide = targetSide, sourceSide
	}
	source, err := cleanCopiedPath(source, sourceSide)
	if err != nil || source == "." {
		return config.Profile{}, "", errors.New("выберите файл или папку внутри корня")
	}
	target, err = cleanCopiedPath(target, targetSide)
	if err != nil {
		return config.Profile{}, "", err
	}
	localRelative, remoteRelative := path.Dir(source), target
	if direction == "download" {
		localRelative, remoteRelative = target, path.Dir(source)
		if remoteRootEntryExcluded(connection, source) {
			return config.Profile{}, "", errors.New("источник исключён правилами подключения")
		}
	} else if remoteRootEntryExcluded(connection, path.Join(target, path.Base(source))) {
		return config.Profile{}, "", errors.New("назначение исключено правилами подключения")
	}
	local, err := localPreviewPath(project, localRelative)
	if err != nil {
		return config.Profile{}, "", err
	}
	info, err := os.Lstat(local)
	if err != nil {
		return config.Profile{}, "", err
	}
	if !realDirectory(info) {
		return config.Profile{}, "", errors.New("локальная папка должна быть обычным каталогом")
	}
	remote, err := config.ResolveDeploymentPath(connection.RootPath, remoteRelative)
	if err != nil {
		return config.Profile{}, "", err
	}
	return config.Profile{Name: project.Name, LocalRoot: local, RemoteRoot: remote, Connection: connection.Connection, Excludes: slices.Clone(connection.Excludes), ExcludeBase: localRelative}, path.Base(source), nil
}

func (s *Service) PreviewDropTransfer(ctx context.Context, projectID, connectionID, direction, source, target string) (BatchPreview, error) {
	project, _, err := s.previewProject(projectID)
	if err != nil {
		return BatchPreview{}, err
	}
	connection, err := previewConnection(project, connectionID)
	if err != nil {
		return BatchPreview{}, err
	}
	profile, name, err := dropTransferProfile(project, connection, direction, source, target)
	if err != nil {
		return BatchPreview{}, err
	}
	return s.previewBatchWithProfile(ctx, profile, direction, []string{name}, projectID, connectionID)
}
