package main

import (
	"awesomeProject/internal/application"
	"awesomeProject/internal/config"
	"awesomeProject/internal/gitstatus"
	"awesomeProject/internal/mapping"
	"awesomeProject/internal/sshconfig"
	"awesomeProject/internal/transport"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// session keeps one window's application service and desktop operations isolated.
type session struct {
	service *application.Service
	ctx     context.Context
	desktop desktopAdapter
	cancel  context.CancelFunc
	once    sync.Once
	closed  atomic.Bool
}

// SelectLocalDirectory changes only the draft in the UI, never saved settings.
func (a *session) SelectLocalDirectory(current string) (string, error) {
	if a.desktop == nil {
		return "", fmt.Errorf("Диалог выбора папки доступен после запуска окна приложения")
	}
	initial := ""
	if filepath.IsAbs(current) {
		if info, err := os.Stat(current); err == nil && info.IsDir() {
			initial = current
		}
	}
	selected, err := a.desktop.SelectDirectory(initial)
	if err != nil {
		return "", fmt.Errorf("Не удалось открыть диалог выбора папки: %w", err)
	}
	if selected == "" {
		return "", nil
	}
	// Use the same root validation as SaveProfile, independently of remote fields.
	if _, err := mapping.New(selected, "/"); err != nil {
		return "", fmt.Errorf("Нельзя выбрать эту папку проекта: %w", err)
	}
	return filepath.Clean(selected), nil
}

func (a *session) SaveProfile(profile config.Profile) (application.Status, error) {
	if _, err := a.service.SaveProfile(profile); err != nil {
		return application.Status{}, err
	}
	return a.GetStatus(), nil
}
func (a *session) SaveProject(project config.Project) (application.Status, error) {
	if _, err := a.service.SaveProject(project); err != nil {
		return application.Status{}, err
	}
	return a.GetStatus(), nil
}
func (a *session) PreviewPhpStormProject(root string) (application.PhpStormPreview, error) {
	return a.service.PreviewPhpStormProject(root)
}
func (a *session) CreateImportedProject(project config.Project) (application.Status, error) {
	if _, err := a.service.CreateImportedProject(project); err != nil {
		return application.Status{}, err
	}
	return a.GetStatus(), nil
}

func (a *session) SelectProject(projectID, connectionID, mappingID string) (application.Status, error) {
	if _, err := a.service.SelectProject(projectID, connectionID, mappingID); err != nil {
		return application.Status{}, err
	}
	return a.GetStatus(), nil
}

// GetGitStatus reads only the selected project's local repository.
func (a *session) GetGitStatus(projectID string) (gitstatus.Snapshot, error) {
	return a.service.GetGitStatus(a.ctx, projectID)
}

func (a *session) CopyPaths(projectID, side, sourceID, targetID string, relatives []string, kind string) error {
	if a.desktop == nil {
		return fmt.Errorf("буфер обмена доступен только в окне приложения")
	}
	text, err := a.service.CopiedPaths(projectID, side, sourceID, targetID, relatives, kind)
	if err != nil {
		return err
	}
	if err := a.desktop.SetClipboard(text); err != nil {
		return fmt.Errorf("не удалось записать пути в буфер обмена: %w", err)
	}
	return nil
}

func (a *session) ListLocal(relative string) ([]application.FileEntry, error) {
	return a.service.ListLocal(relative)
}
func (a *session) ListProjectLocal(relative string) ([]application.FileEntry, error) {
	return a.service.ListProjectLocal(relative)
}
func (a *session) ListRemote(relative string) ([]application.FileEntry, error) {
	return a.service.ListRemote(a.ctx, relative)
}
func (a *session) GetNavigationStatus() application.NavigationStatus {
	return a.service.GetNavigationStatus()
}
func (a *session) DisconnectRemote() { a.service.DisconnectRemote() }
func (a *session) PreviewTransfer(direction, relative string) (application.Preview, error) {
	return a.service.PreviewTransfer(direction, relative)
}
func (a *session) ExecuteTransfer(token string, overwrite bool) error {
	return a.service.ExecuteTransfer(a.ctx, token, overwrite)
}
func (a *session) PreviewBatch(direction string, relatives []string) (application.BatchPreview, error) {
	return a.service.PreviewBatch(a.ctx, direction, relatives)
}
func (a *session) PreviewBatchForServer(projectID, connectionID, mappingID, direction string, relatives []string) (application.BatchPreview, error) {
	return a.service.PreviewBatchForServer(a.ctx, projectID, connectionID, mappingID, direction, relatives)
}
func (a *session) ExecuteBatch(token string, existingPolicy string, includeEnvironment bool) (application.BatchResult, error) {
	return a.service.ExecuteBatch(a.ctx, token, existingPolicy, includeEnvironment)
}
func (a *session) CancelOperation() { a.service.CancelOperation() }

func (a *session) ReadLocalFile(projectID, relative string) (application.FileContent, error) {
	return a.service.ReadLocalFile(projectID, relative)
}
func (a *session) ReadRemoteFile(projectID, connectionID, relative string) (application.FileContent, error) {
	return a.service.ReadRemoteFile(a.ctx, projectID, connectionID, relative)
}
func (a *session) CompareFile(projectID, connectionID, relative string) (application.FileComparison, error) {
	return a.service.CompareFile(a.ctx, projectID, connectionID, relative)
}
func (a *session) SearchProjectFiles(projectID, query string) (application.FileSearchResult, error) {
	return a.service.SearchProjectFiles(a.ctx, projectID, query)
}
func (a *session) SetDirectorySearchExcluded(projectID, relative string, excluded bool) (application.Status, error) {
	return a.service.SetDirectorySearchExcluded(projectID, relative, excluded)
}

func (a *session) GetAuthPrompt() *application.AuthPrompt { return a.service.GetAuthPrompt() }
func (a *session) RespondAuthPrompt(id, answer string, cancelled, remember bool) error {
	return a.service.RespondAuthPrompt(id, answer, cancelled, remember)
}
func (a *session) ForgetSavedPassword(projectID, connectionID string) error {
	return a.service.ForgetSavedPassword(projectID, connectionID)
}

func (a *session) GetQueue() application.QueueState { return a.service.GetQueue() }
func (a *session) EnqueueBatch(token, policy string, includeEnvironment bool) (application.QueueState, error) {
	return a.service.EnqueueBatch(a.ctx, token, policy, includeEnvironment)
}
func (a *session) CancelQueueJob(id string) error { return a.service.CancelQueueJob(id) }
func (a *session) SetQueuePaused(paused bool)     { a.service.SetQueuePaused(paused) }
func (a *session) ClearFinishedQueue()            { a.service.ClearFinishedQueue() }
func (a *session) PreviewQueueRetry(id string) (application.BatchPreview, error) {
	return a.service.PreviewQueueRetry(a.ctx, id)
}

func (a *session) DiscoverSSH(configPath string) (sshconfig.Result, error) {
	return a.service.DiscoverSSH(configPath)
}
func (a *session) EvaluateSSH(connection transport.Connection, trusted bool) (transport.Effective, error) {
	return a.service.EvaluateSSH(a.ctx, connection, trusted)
}

func (a *session) GetStatus() application.Status {
	status := a.service.Status()
	status.WebviewVersion, status.WebviewMessage = webviewInfo()
	return status
}
func (a *session) SaveProjectName(name string) (application.Status, error) {
	if _, err := a.service.SaveProjectName(name); err != nil {
		return application.Status{}, err
	}
	return a.GetStatus(), nil
}

func (a *session) GetGitBranches(projectID string) ([]gitstatus.Branch, error) {
	return a.service.GetGitBranches(a.ctx, projectID)
}
func (a *session) CompareGitFile(projectID, relative, ref string) (application.FileComparison, error) {
	return a.service.CompareGitFile(a.ctx, projectID, relative, ref)
}

func (a *session) PreviewDropTransfer(projectID, connectionID, direction, source, target string) (application.BatchPreview, error) {
	return a.service.PreviewDropTransfer(a.ctx, projectID, connectionID, direction, source, target)
}
