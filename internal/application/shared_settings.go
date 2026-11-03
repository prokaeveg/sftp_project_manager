package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/transport"
)

// ApplySharedSettings replaces the shared project catalog in this window while
// retaining its local selection whenever that project still exists.
func (s *Service) ApplySharedSettings(settings config.Settings) {
	var navigationToClose *transport.Session
	s.mu.Lock()

	current := s.status.Settings
	settings = cloneSettings(settings)
	projectID := current.ActiveProjectID
	connectionID, mappingID := current.ActiveConnectionID, current.ActiveMappingID
	var selected *config.Project
	if projectID != "" {
		for i := range settings.Projects {
			if settings.Projects[i].ID == projectID {
				selected = &settings.Projects[i]
				break
			}
		}
	}
	if selected == nil {
		settings.ActiveProjectID, settings.ActiveConnectionID, settings.ActiveMappingID = "", "", ""
		settings.ProjectName = ""
	} else {
		settings.ActiveProjectID = selected.ID
		settings.ActiveConnectionID, settings.ActiveMappingID = projectSelection(*selected, connectionID, mappingID)
		settings.ProjectName = selected.Name
	}
	configFlatten(&settings)
	s.status.Settings = settings
	navigationToClose = s.reconcileNavigationLocked()
	s.mu.Unlock()

	closeNavigationSession(navigationToClose)
}
