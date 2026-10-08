package application

import (
	"awesomeProject/internal/credentials"
	"awesomeProject/internal/transport"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

func credentialConnectionKey(connection transport.Connection) string {
	data, _ := json.Marshal(connection)
	return credentialHash(string(data))
}

func credentialHash(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func (s *Service) passwordStore() credentials.Store {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.credentials == nil {
		s.credentials = credentials.New()
	}
	return s.credentials
}

// Only OpenSSH password questions may be saved. Other prompts (key passphrases,
// confirmation, keyboard-interactive OTP) never receive a persistent answer.
func rememberablePasswordPrompt(connection transport.Connection, prompt string) bool {
	return connection.AuthMode == "password" && strings.HasSuffix(strings.TrimSpace(prompt), "'s password:")
}

type passwordAttempt struct {
	mu          sync.Mutex
	service     *Service
	connection  transport.Connection
	store       credentials.Store
	used        map[string]bool
	save        map[string]string
	promptError error
}

func (s *Service) newPasswordAttempt(connection transport.Connection) *passwordAttempt {
	return &passwordAttempt{service: s, connection: connection, store: s.passwordStore(), used: make(map[string]bool), save: make(map[string]string)}
}

func (a *passwordAttempt) prompt(ctx context.Context, prompt string) (answerValue string, retErr error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	defer func() {
		if retErr != nil {
			a.promptError = retErr
		}
	}()
	canRemember := rememberablePasswordPrompt(a.connection, prompt)
	key := credentialConnectionKey(a.connection) + "/" + credentialHash(prompt)
	if canRemember {
		if !a.used[key] {
			a.used[key] = true
			value, found, err := a.store.Read(key)
			if err != nil {
				return "", errors.New("не удалось прочитать пароль из хранилища Windows")
			}
			if found {
				return value, nil
			}
		} else {
			// Repeated question means the previous answer was rejected. Do not
			// keep supplying the same stored password on every SSH attempt.
			if err := a.store.DeletePrefix(key); err != nil {
				return "", errors.New("не удалось удалить отклонённый пароль из хранилища Windows")
			}
		}
		delete(a.save, key)
	}
	answer, err := a.service.requestAuthAnswer(ctx, prompt, canRemember)
	if err != nil {
		return "", err
	}
	if answer.remember {
		a.save[key] = answer.value
	}
	return answer.value, nil
}

func (a *passwordAttempt) saveSuccessful(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	defer clear(a.save)
	if err := ctx.Err(); err != nil {
		return err
	}
	for key, value := range a.save {
		if err := a.store.Write(key, value); err != nil {
			return errors.New("не удалось сохранить пароль в хранилище Windows; повторите вход без галочки сохранения")
		}
	}
	return nil
}

func (s *Service) openAuthenticated(ctx context.Context, executable string, connection transport.Connection, open func(context.Context, string, transport.Connection) (*transport.Session, error)) (*transport.Session, error) {
	if open == nil {
		open = transport.Open
	}
	if connection.AuthMode != "password" {
		return open(transport.WithAuthPrompt(ctx, s.requestAuth), executable, connection)
	}
	attempt := s.newPasswordAttempt(connection)
	defer func() { attempt.mu.Lock(); clear(attempt.save); attempt.mu.Unlock() }()
	session, err := open(transport.WithAuthPrompt(ctx, attempt.prompt), executable, connection)
	if err != nil {
		attempt.mu.Lock()
		promptErr := attempt.promptError
		attempt.mu.Unlock()
		if promptErr != nil && ctx.Err() == nil && !errors.Is(promptErr, context.Canceled) && !errors.Is(promptErr, context.DeadlineExceeded) {
			return nil, promptErr
		}
		return nil, err
	}
	if err := attempt.saveSuccessful(ctx); err != nil {
		session.Close()
		return nil, err
	}
	return session, nil
}

func (s *Service) ForgetSavedPassword(projectID, connectionID string) error {
	if !s.operationMu.TryLock() {
		return errors.New("дождитесь завершения операции перед удалением пароля")
	}
	defer s.operationMu.Unlock()
	s.mu.Lock()
	var connection *transport.Connection
	for _, project := range s.status.Settings.Projects {
		if project.ID != projectID {
			continue
		}
		for _, server := range project.Connections {
			if server.ID == connectionID {
				value := server.Connection
				connection = &value
				break
			}
		}
	}
	s.mu.Unlock()
	if connection == nil {
		return errors.New("сохранённое подключение не найдено")
	}
	if err := s.passwordStore().DeletePrefix(credentialConnectionKey(*connection)); err != nil {
		return errors.New("не удалось удалить пароль из хранилища Windows")
	}
	return nil
}
