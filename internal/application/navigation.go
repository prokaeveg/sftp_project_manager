package application

import (
	"awesomeProject/internal/transport"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/pkg/sftp"
)

type NavigationStatus struct {
	State        string `json:"state"`
	ProjectID    string `json:"projectID"`
	ConnectionID string `json:"connectionID"`
}

type navigationKey struct {
	projectID, connectionID, executable string
	connection                          transport.Connection
}

type navigationSession struct {
	key           navigationKey
	ctx           context.Context
	cancel        context.CancelFunc
	session       *transport.Session
	requestCancel context.CancelFunc
	idleTimer     *time.Timer
	inUse         bool
	idleVersion   uint64
}

func (s *Service) navigationKeyLocked() navigationKey {
	key := navigationKey{projectID: s.status.Settings.ActiveProjectID, connectionID: s.status.Settings.ActiveConnectionID, executable: s.status.SSHPath}
	for _, project := range s.status.Settings.Projects {
		if project.ID != key.projectID {
			continue
		}
		for _, connection := range project.Connections {
			if connection.ID == key.connectionID {
				key.connection = connection.Connection
				return key
			}
		}
	}
	if profile := s.status.Settings.Profile; profile != nil {
		key.connection = profile.Connection
	}
	return key
}

// Call while holding mu. Reaping happens after releasing it, because the
// listing's cancellation callback may also need mu to finish.
func (s *Service) detachNavigationLocked(nav *navigationSession) *transport.Session {
	if nav == nil || s.navigation != nav {
		return nil
	}
	s.navigation = nil
	if nav.idleTimer != nil {
		nav.idleTimer.Stop()
	}
	nav.cancel()
	if nav.requestCancel != nil {
		nav.requestCancel()
	}
	return nav.session
}

func (s *Service) reconcileNavigationLocked() *transport.Session {
	if s.navigation != nil && s.navigation.key != s.navigationKeyLocked() {
		return s.detachNavigationLocked(s.navigation)
	}
	return nil
}

func closeNavigationSession(session *transport.Session) {
	if session != nil {
		session.Close()
	}
}

func (s *Service) dropNavigation(nav *navigationSession) {
	s.mu.Lock()
	session := s.detachNavigationLocked(nav)
	s.mu.Unlock()
	closeNavigationSession(session)
}

func (s *Service) DisconnectRemote() {
	s.mu.Lock()
	session := s.detachNavigationLocked(s.navigation)
	s.mu.Unlock()
	closeNavigationSession(session)
}

func (s *Service) GetNavigationStatus() NavigationStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := NavigationStatus{State: "disconnected", ProjectID: s.status.Settings.ActiveProjectID, ConnectionID: s.status.Settings.ActiveConnectionID}
	if nav := s.navigation; nav != nil && nav.key == s.navigationKeyLocked() {
		state.State = "connecting"
		if nav.session != nil {
			state.State = "connected"
		}
	}
	return state
}

func (s *Service) shutdownNavigation() {
	s.mu.Lock()
	s.navigationClosed = true
	session := s.detachNavigationLocked(s.navigation)
	s.mu.Unlock()
	closeNavigationSession(session)
}

func (s *Service) navigationOperation(parent context.Context, key navigationKey, action func(*transport.Session) error) (retErr error) {
	if !s.operationMu.TryLock() {
		return errors.New("другая операция ещё выполняется")
	}
	defer s.operationMu.Unlock()
	requestTimeout := 30 * time.Second
	if key.connection.AuthMode == "password" || key.connection.AuthMode == "interactive" {
		requestTimeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, requestTimeout)
	defer cancel()
	s.mu.Lock()
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return err
	}
	if s.navigationClosed || key != s.navigationKeyLocked() {
		s.mu.Unlock()
		return errors.New("подключение изменилось или запрос отменён; повторите чтение")
	}
	obsolete := s.reconcileNavigationLocked()
	nav := s.navigation
	if nav == nil {
		lifeCtx, lifeCancel := context.WithCancel(context.Background())
		nav = &navigationSession{key: key, ctx: lifeCtx, cancel: lifeCancel}
		s.navigation = nav
	}
	if nav.idleTimer != nil {
		nav.idleTimer.Stop()
		nav.idleTimer = nil
	}
	nav.idleVersion++
	nav.inUse, nav.requestCancel = true, cancel
	s.cancelOperation = cancel
	open := s.navigationOpen
	if open == nil {
		open = transport.Open
	}
	s.mu.Unlock()
	closeNavigationSession(obsolete)

	// Timeout/cancel must close the long-lived process to interrupt SFTP I/O,
	// including the handshake. Successful reads stop this watcher instead.
	watchDone := make(chan struct{})
	stopWatch := context.AfterFunc(ctx, func() { defer close(watchDone); s.dropNavigation(nav) })
	defer func() {
		if !stopWatch() {
			<-watchDone
		}
		if ctx.Err() != nil {
			retErr = fmt.Errorf("чтение остановлено: %w", errors.Join(ctx.Err(), retErr))
		}
		s.mu.Lock()
		s.cancelOperation, s.activeSession = nil, nil
		var closing *transport.Session
		if s.navigation == nav {
			nav.inUse, nav.requestCancel = false, nil
			var statusError *sftp.StatusError
			// A protocol status response (including generic SSH_FX_FAILURE)
			// is a filesystem error, not evidence that the channel is broken.
			healthy := nav.session != nil && ctx.Err() == nil && (retErr == nil || errors.Is(retErr, os.ErrNotExist) || errors.Is(retErr, os.ErrPermission) || errors.As(retErr, &statusError))
			if !healthy {
				closing = s.detachNavigationLocked(nav)
			} else {
				idle := s.navigationIdle
				if idle <= 0 {
					idle = 10 * time.Minute
				}
				version := nav.idleVersion
				nav.idleTimer = time.AfterFunc(idle, func() {
					s.mu.Lock()
					var expired *transport.Session
					if s.navigation == nav && !nav.inUse && nav.idleVersion == version {
						expired = s.detachNavigationLocked(nav)
					}
					s.mu.Unlock()
					closeNavigationSession(expired)
				})
			}
		}
		s.mu.Unlock()
		closeNavigationSession(closing)
	}()

	if nav.session == nil {
		session, err := s.openAuthenticated(nav.ctx, key.executable, key.connection, open)
		if err != nil {
			return err
		}
		s.mu.Lock()
		if s.navigation != nav || nav.ctx.Err() != nil || ctx.Err() != nil {
			s.mu.Unlock()
			session.Close()
			return errors.New("подключение изменилось или запрос отменён; повторите чтение")
		}
		nav.session = session
		s.mu.Unlock()
		go func() {
			select {
			case <-session.Done():
				s.dropNavigation(nav)
			case <-nav.ctx.Done():
			}
		}()
	}
	s.mu.Lock()
	if s.navigation != nav || ctx.Err() != nil {
		s.mu.Unlock()
		return errors.New("подключение изменилось или запрос отменён; повторите чтение")
	}
	s.activeSession = nav.session
	s.mu.Unlock()
	return action(nav.session)
}
