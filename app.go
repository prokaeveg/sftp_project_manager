package main

import (
	"awesomeProject/internal/application"
	"awesomeProject/internal/config"
	"awesomeProject/internal/mapping"
	"awesomeProject/internal/sshconfig"
	"awesomeProject/internal/transport"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the narrow bridge between Vue and the application service.
type App struct {
	service *application.Service
	ctx     context.Context
	uiCtx   context.Context
}

func (a *App) startup(ctx context.Context) { a.uiCtx = ctx }

// SelectLocalDirectory changes only the draft in the UI, never saved settings.
func (a *App) SelectLocalDirectory(current string) (string, error) {
	if a.uiCtx == nil {
		return "", fmt.Errorf("Диалог выбора папки доступен после запуска окна приложения")
	}
	options := runtime.OpenDialogOptions{Title: "Выберите локальную папку проекта"}
	if filepath.IsAbs(current) {
		if info, err := os.Stat(current); err == nil && info.IsDir() {
			options.DefaultDirectory = current
		}
	}
	selected, err := runtime.OpenDirectoryDialog(a.uiCtx, options)
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

func (a *App) SaveProfile(profile config.Profile) (application.Status, error) {
	if _, err := a.service.SaveProfile(profile); err != nil {
		return application.Status{}, err
	}
	return a.GetStatus(), nil
}
func (a *App) SaveProject(project config.Project) (application.Status, error) {
	if _, err := a.service.SaveProject(project); err != nil {
		return application.Status{}, err
	}
	return a.GetStatus(), nil
}
func (a *App) SelectProject(projectID, connectionID, mappingID string) (application.Status, error) {
	if _, err := a.service.SelectProject(projectID, connectionID, mappingID); err != nil {
		return application.Status{}, err
	}
	return a.GetStatus(), nil
}
func (a *App) ListLocal(relative string) ([]application.FileEntry, error) {
	return a.service.ListLocal(relative)
}
func (a *App) ListProjectLocal(relative string) ([]application.FileEntry, error) {
	return a.service.ListProjectLocal(relative)
}
func (a *App) ListRemote(relative string) ([]application.FileEntry, error) {
	return a.service.ListRemote(a.ctx, relative)
}
func (a *App) GetNavigationStatus() application.NavigationStatus {
	return a.service.GetNavigationStatus()
}
func (a *App) DisconnectRemote() { a.service.DisconnectRemote() }
func (a *App) PreviewTransfer(direction, relative string) (application.Preview, error) {
	return a.service.PreviewTransfer(direction, relative)
}
func (a *App) ExecuteTransfer(token string, overwrite bool) error {
	return a.service.ExecuteTransfer(a.ctx, token, overwrite)
}
func (a *App) PreviewBatch(direction string, relatives []string) (application.BatchPreview, error) {
	return a.service.PreviewBatch(a.ctx, direction, relatives)
}
func (a *App) PreviewBatchForServer(projectID, connectionID, mappingID, direction string, relatives []string) (application.BatchPreview, error) {
	return a.service.PreviewBatchForServer(a.ctx, projectID, connectionID, mappingID, direction, relatives)
}
func (a *App) ExecuteBatch(token string, existingPolicy string, includeEnvironment bool) (application.BatchResult, error) {
	return a.service.ExecuteBatch(a.ctx, token, existingPolicy, includeEnvironment)
}
func (a *App) CancelOperation() { a.service.CancelOperation() }

func (a *App) ReadLocalFile(projectID, relative string) (application.FileContent, error) {
	return a.service.ReadLocalFile(projectID, relative)
}
func (a *App) ReadRemoteFile(projectID, connectionID, relative string) (application.FileContent, error) {
	return a.service.ReadRemoteFile(a.ctx, projectID, connectionID, relative)
}
func (a *App) CompareFile(projectID, connectionID, relative string) (application.FileComparison, error) {
	return a.service.CompareFile(a.ctx, projectID, connectionID, relative)
}
func (a *App) SearchProjectFiles(projectID, query string) (application.FileSearchResult, error) {
	return a.service.SearchProjectFiles(a.ctx, projectID, query)
}

func (a *App) GetAuthPrompt() *application.AuthPrompt { return a.service.GetAuthPrompt() }
func (a *App) RespondAuthPrompt(id, answer string, cancelled, remember bool) error {
	return a.service.RespondAuthPrompt(id, answer, cancelled, remember)
}
func (a *App) ForgetSavedPassword(projectID, connectionID string) error {
	return a.service.ForgetSavedPassword(projectID, connectionID)
}

func (a *App) GetQueue() application.QueueState { return a.service.GetQueue() }
func (a *App) EnqueueBatch(token, policy string, includeEnvironment bool) (application.QueueState, error) {
	return a.service.EnqueueBatch(a.ctx, token, policy, includeEnvironment)
}
func (a *App) CancelQueueJob(id string) error { return a.service.CancelQueueJob(id) }
func (a *App) SetQueuePaused(paused bool)     { a.service.SetQueuePaused(paused) }
func (a *App) ClearFinishedQueue()            { a.service.ClearFinishedQueue() }
func (a *App) PreviewQueueRetry(id string) (application.BatchPreview, error) {
	return a.service.PreviewQueueRetry(a.ctx, id)
}

func (a *App) DiscoverSSH(configPath string) (sshconfig.Result, error) {
	return a.service.DiscoverSSH(configPath)
}
func (a *App) EvaluateSSH(connection transport.Connection, trusted bool) (transport.Effective, error) {
	return a.service.EvaluateSSH(a.ctx, connection, trusted)
}

func (a *App) GetStatus() application.Status {
	status := a.service.Status()
	status.WebviewVersion, status.WebviewMessage = webviewInfo()
	return status
}
func (a *App) SaveProjectName(name string) (application.Status, error) {
	if _, err := a.service.SaveProjectName(name); err != nil {
		return application.Status{}, err
	}
	return a.GetStatus(), nil
}
