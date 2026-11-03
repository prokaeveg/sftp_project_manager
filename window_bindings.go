package main

import (
	"awesomeProject/internal/application"
	"awesomeProject/internal/config"
	"awesomeProject/internal/gitstatus"
	"awesomeProject/internal/sshconfig"
	"awesomeProject/internal/transport"
	"context"
	"fmt"
)

func (a *App) SelectLocalDirectory(ctx context.Context, current string) (string, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero string
		return zero, err
	}
	return s.SelectLocalDirectory(current)
}
func (a *App) SaveProfile(ctx context.Context, profile config.Profile) (application.Status, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.Status
		return zero, err
	}
	if a.manager != nil {
		a.manager.openMu.Lock()
		defer a.manager.openMu.Unlock()
		if a.manager.projectOpenElsewhere(s, "legacy-project") {
			return application.Status{}, fmt.Errorf("проект уже открыт в другом окне")
		}
		a.manager.configMu.Lock()
		defer a.manager.configMu.Unlock()
		if s.closed.Load() {
			return application.Status{}, closedWindowError()
		}
		defer a.manager.broadcast(s)
	}
	return s.SaveProfile(profile)
}
func (a *App) SaveProject(ctx context.Context, project config.Project) (application.Status, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.Status
		return zero, err
	}
	if a.manager != nil {
		a.manager.openMu.Lock()
		defer a.manager.openMu.Unlock()
		if a.manager.projectOpenElsewhere(s, project.ID) {
			return application.Status{}, fmt.Errorf("проект уже открыт в другом окне")
		}
		a.manager.configMu.Lock()
		defer a.manager.configMu.Unlock()
		if s.closed.Load() {
			return application.Status{}, closedWindowError()
		}
		defer a.manager.broadcast(s)
	}
	return s.SaveProject(project)
}
func (a *App) PreviewPhpStormProject(ctx context.Context, root string) (application.PhpStormPreview, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.PhpStormPreview
		return zero, err
	}
	return s.PreviewPhpStormProject(root)
}
func (a *App) CreateImportedProject(ctx context.Context, project config.Project) (application.Status, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.Status
		return zero, err
	}
	if a.manager != nil {
		a.manager.configMu.Lock()
		defer a.manager.configMu.Unlock()
		if s.closed.Load() {
			return application.Status{}, closedWindowError()
		}
		defer a.manager.broadcast(s)
	}
	return s.CreateImportedProject(project)
}
func (a *App) SelectProject(ctx context.Context, projectID, connectionID, mappingID string) (application.Status, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.Status
		return zero, err
	}
	if a.manager != nil {
		a.manager.openMu.Lock()
		defer a.manager.openMu.Unlock()
		if projectID != s.service.Status().Settings.ActiveProjectID {
			var zero application.Status
			return zero, fmt.Errorf("используйте OpenProject для выбора проекта")
		}
		a.manager.configMu.Lock()
		defer a.manager.configMu.Unlock()
		if s.closed.Load() {
			return application.Status{}, closedWindowError()
		}
		defer a.manager.broadcast(s)
	}
	return s.SelectProject(projectID, connectionID, mappingID)
}
func (a *App) GetGitStatus(ctx context.Context, projectID string) (gitstatus.Snapshot, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero gitstatus.Snapshot
		return zero, err
	}
	return s.GetGitStatus(projectID)
}
func (a *App) CopyPaths(ctx context.Context, projectID, side, sourceID, targetID string, relatives []string, kind string) error {
	s, err := a.resolve(ctx)
	if err != nil {
		return err
	}
	return s.CopyPaths(projectID, side, sourceID, targetID, relatives, kind)
}
func (a *App) ListLocal(ctx context.Context, relative string) ([]application.FileEntry, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero []application.FileEntry
		return zero, err
	}
	return s.ListLocal(relative)
}
func (a *App) ListProjectLocal(ctx context.Context, relative string) ([]application.FileEntry, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero []application.FileEntry
		return zero, err
	}
	return s.ListProjectLocal(relative)
}
func (a *App) ListRemote(ctx context.Context, relative string) ([]application.FileEntry, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero []application.FileEntry
		return zero, err
	}
	return s.ListRemote(relative)
}
func (a *App) GetNavigationStatus(ctx context.Context) (application.NavigationStatus, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.NavigationStatus
		return zero, err
	}
	return s.GetNavigationStatus(), nil
}
func (a *App) DisconnectRemote(ctx context.Context) error {
	s, err := a.resolve(ctx)
	if err != nil {
		return err
	}
	s.DisconnectRemote()
	return nil
}
func (a *App) PreviewTransfer(ctx context.Context, direction, relative string) (application.Preview, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.Preview
		return zero, err
	}
	return s.PreviewTransfer(direction, relative)
}
func (a *App) ExecuteTransfer(ctx context.Context, token string, overwrite bool) error {
	s, err := a.resolve(ctx)
	if err != nil {
		return err
	}
	return s.ExecuteTransfer(token, overwrite)
}
func (a *App) PreviewBatch(ctx context.Context, direction string, relatives []string) (application.BatchPreview, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.BatchPreview
		return zero, err
	}
	return s.PreviewBatch(direction, relatives)
}
func (a *App) PreviewBatchForServer(ctx context.Context, projectID, connectionID, mappingID, direction string, relatives []string) (application.BatchPreview, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.BatchPreview
		return zero, err
	}
	return s.PreviewBatchForServer(projectID, connectionID, mappingID, direction, relatives)
}
func (a *App) ExecuteBatch(ctx context.Context, token string, existingPolicy string, includeEnvironment bool) (application.BatchResult, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.BatchResult
		return zero, err
	}
	return s.ExecuteBatch(token, existingPolicy, includeEnvironment)
}
func (a *App) CancelOperation(ctx context.Context) error {
	s, err := a.resolve(ctx)
	if err != nil {
		return err
	}
	s.CancelOperation()
	return nil
}
func (a *App) ReadLocalFile(ctx context.Context, projectID, relative string) (application.FileContent, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.FileContent
		return zero, err
	}
	return s.ReadLocalFile(projectID, relative)
}
func (a *App) ReadRemoteFile(ctx context.Context, projectID, connectionID, relative string) (application.FileContent, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.FileContent
		return zero, err
	}
	return s.ReadRemoteFile(projectID, connectionID, relative)
}
func (a *App) CompareFile(ctx context.Context, projectID, connectionID, relative string) (application.FileComparison, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.FileComparison
		return zero, err
	}
	return s.CompareFile(projectID, connectionID, relative)
}
func (a *App) SearchProjectFiles(ctx context.Context, projectID, query string) (application.FileSearchResult, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.FileSearchResult
		return zero, err
	}
	return s.SearchProjectFiles(projectID, query)
}
func (a *App) SetDirectorySearchExcluded(ctx context.Context, projectID, relative string, excluded bool) (application.Status, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.Status
		return zero, err
	}
	if a.manager != nil {
		a.manager.configMu.Lock()
		defer a.manager.configMu.Unlock()
		if s.closed.Load() {
			return application.Status{}, closedWindowError()
		}
		defer a.manager.broadcast(s)
	}
	return s.SetDirectorySearchExcluded(projectID, relative, excluded)
}
func (a *App) GetAuthPrompt(ctx context.Context) (*application.AuthPrompt, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero *application.AuthPrompt
		return zero, err
	}
	return s.GetAuthPrompt(), nil
}
func (a *App) RespondAuthPrompt(ctx context.Context, id, answer string, cancelled, remember bool) error {
	s, err := a.resolve(ctx)
	if err != nil {
		return err
	}
	return s.RespondAuthPrompt(id, answer, cancelled, remember)
}
func (a *App) ForgetSavedPassword(ctx context.Context, projectID, connectionID string) error {
	s, err := a.resolve(ctx)
	if err != nil {
		return err
	}
	return s.ForgetSavedPassword(projectID, connectionID)
}
func (a *App) GetQueue(ctx context.Context) (application.QueueState, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.QueueState
		return zero, err
	}
	return s.GetQueue(), nil
}
func (a *App) EnqueueBatch(ctx context.Context, token, policy string, includeEnvironment bool) (application.QueueState, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.QueueState
		return zero, err
	}
	return s.EnqueueBatch(token, policy, includeEnvironment)
}
func (a *App) CancelQueueJob(ctx context.Context, id string) error {
	s, err := a.resolve(ctx)
	if err != nil {
		return err
	}
	return s.CancelQueueJob(id)
}
func (a *App) SetQueuePaused(ctx context.Context, paused bool) error {
	s, err := a.resolve(ctx)
	if err != nil {
		return err
	}
	s.SetQueuePaused(paused)
	return nil
}
func (a *App) ClearFinishedQueue(ctx context.Context) error {
	s, err := a.resolve(ctx)
	if err != nil {
		return err
	}
	s.ClearFinishedQueue()
	return nil
}
func (a *App) PreviewQueueRetry(ctx context.Context, id string) (application.BatchPreview, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.BatchPreview
		return zero, err
	}
	return s.PreviewQueueRetry(id)
}
func (a *App) DiscoverSSH(ctx context.Context, configPath string) (sshconfig.Result, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero sshconfig.Result
		return zero, err
	}
	return s.DiscoverSSH(configPath)
}
func (a *App) EvaluateSSH(ctx context.Context, connection transport.Connection, trusted bool) (transport.Effective, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero transport.Effective
		return zero, err
	}
	return s.EvaluateSSH(connection, trusted)
}
func (a *App) GetStatus(ctx context.Context) (application.Status, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.Status
		return zero, err
	}
	return s.GetStatus(), nil
}
func (a *App) SaveProjectName(ctx context.Context, name string) (application.Status, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.Status
		return zero, err
	}
	if a.manager != nil {
		a.manager.configMu.Lock()
		defer a.manager.configMu.Unlock()
		if s.closed.Load() {
			return application.Status{}, closedWindowError()
		}
		defer a.manager.broadcast(s)
	}
	return s.SaveProjectName(name)
}
func (a *App) GetGitBranches(ctx context.Context, projectID string) ([]gitstatus.Branch, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero []gitstatus.Branch
		return zero, err
	}
	return s.GetGitBranches(projectID)
}
func (a *App) CompareGitFile(ctx context.Context, projectID, relative, ref string) (application.FileComparison, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.FileComparison
		return zero, err
	}
	return s.CompareGitFile(projectID, relative, ref)
}
func (a *App) PreviewDropTransfer(ctx context.Context, projectID, connectionID, direction, source, target string) (application.BatchPreview, error) {
	s, err := a.resolve(ctx)
	if err != nil {
		var zero application.BatchPreview
		return zero, err
	}
	return s.PreviewDropTransfer(projectID, connectionID, direction, source, target)
}
