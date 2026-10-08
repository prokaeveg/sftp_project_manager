package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"awesomeProject/internal/config"
	"awesomeProject/internal/transport"
)

type passwordMemoryStore struct {
	mu       sync.Mutex
	values   map[string]string
	reads    []string
	writes   map[string]string
	deletes  []string
	writeErr error
}

func newPasswordMemoryStore() *passwordMemoryStore {
	return &passwordMemoryStore{values: make(map[string]string), writes: make(map[string]string)}
}

func (s *passwordMemoryStore) Read(target string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = append(s.reads, target)
	value, found := s.values[target]
	return value, found, nil
}

func (s *passwordMemoryStore) Write(target, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return s.writeErr
	}
	s.writes[target] = value
	s.values[target] = value
	return nil
}

func (s *passwordMemoryStore) DeletePrefix(prefix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes = append(s.deletes, prefix)
	for target := range s.values {
		if strings.HasPrefix(target, prefix) {
			delete(s.values, target)
		}
	}
	return nil
}

func (s *passwordMemoryStore) snapshot() (reads []string, writes map[string]string, deletes []string, values map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reads = append([]string(nil), s.reads...)
	deletes = append([]string(nil), s.deletes...)
	writes = make(map[string]string, len(s.writes))
	for key, value := range s.writes {
		writes[key] = value
	}
	values = make(map[string]string, len(s.values))
	for key, value := range s.values {
		values[key] = value
	}
	return
}

func passwordTestConnection() transport.Connection {
	return transport.Connection{AuthMode: "password", Mode: "manual", Host: "sftp.example.test", User: "alice", Port: 22}
}

type promptResult struct {
	value string
	err   error
}

func requestAttemptPrompt(t *testing.T, attempt *passwordAttempt, prompt string) <-chan promptResult {
	t.Helper()
	result := make(chan promptResult, 1)
	go func() {
		value, err := attempt.prompt(context.Background(), prompt)
		result <- promptResult{value: value, err: err}
	}()
	return result
}

func awaitAttemptPrompt(t *testing.T, result <-chan promptResult) promptResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(time.Second):
		t.Fatal("password prompt did not finish")
		return promptResult{}
	}
}

func respondToPrompt(t *testing.T, service *Service, answer string, remember bool) {
	t.Helper()
	prompt := waitAuthPrompt(t, service)
	if !prompt.CanRemember {
		t.Fatal("password prompt did not allow remembering")
	}
	if err := service.RespondAuthPrompt(prompt.ID, answer, false, remember); err != nil {
		t.Fatal(err)
	}
}

func TestSavedPasswordWritesOnlyAfterSuccessfulAttempt(t *testing.T) {
	store := newPasswordMemoryStore()
	service := &Service{credentials: store}
	connection := passwordTestConnection()
	attempt := service.newPasswordAttempt(connection)
	prompt := "alice@sftp.example.test's password:"
	key := credentialConnectionKey(connection) + "/" + credentialHash(prompt)
	secret := "пароль🔐"

	result := requestAttemptPrompt(t, attempt, prompt)
	respondToPrompt(t, service, secret, true)
	if got := awaitAttemptPrompt(t, result); got.err != nil || got.value != secret {
		t.Fatalf("prompt answer = (%q, %v), want entered password", got.value, got.err)
	}
	_, writes, _, values := store.snapshot()
	if len(writes) != 0 || len(values) != 0 {
		t.Fatalf("password was stored before successful authentication: writes=%v values=%v", writes, values)
	}
	if err := attempt.saveSuccessful(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, writes, _, values = store.snapshot()
	if writes[key] != secret || values[key] != secret || len(writes) != 1 {
		t.Fatalf("successful attempt did not store exactly its password: writes=%v values=%v", writes, values)
	}
}

func TestFailedAndCanceledAttemptsDoNotSavePassword(t *testing.T) {
	t.Run("failed authentication", func(t *testing.T) {
		store := newPasswordMemoryStore()
		service := &Service{credentials: store}
		attempt := service.newPasswordAttempt(passwordTestConnection())
		result := requestAttemptPrompt(t, attempt, "alice@sftp.example.test's password:")
		respondToPrompt(t, service, "rejected", true)
		if got := awaitAttemptPrompt(t, result); got.err != nil {
			t.Fatal(got.err)
		}
		// An unsuccessful OpenSSH attempt never calls saveSuccessful.
		_, writes, _, _ := store.snapshot()
		if len(writes) != 0 {
			t.Fatalf("failed authentication saved a password: %v", writes)
		}
	})

	t.Run("canceled prompt", func(t *testing.T) {
		store := newPasswordMemoryStore()
		service := &Service{credentials: store}
		attempt := service.newPasswordAttempt(passwordTestConnection())
		result := requestAttemptPrompt(t, attempt, "alice@sftp.example.test's password:")
		prompt := waitAuthPrompt(t, service)
		if err := service.RespondAuthPrompt(prompt.ID, "discarded", true, true); err != nil {
			t.Fatal(err)
		}
		if got := awaitAttemptPrompt(t, result); !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled prompt error = %v, want context.Canceled", got.err)
		}
		if err := attempt.saveSuccessful(context.Background()); err != nil {
			t.Fatal(err)
		}
		_, writes, _, _ := store.snapshot()
		if len(writes) != 0 {
			t.Fatalf("canceled prompt saved a password: %v", writes)
		}
	})

	t.Run("canceled successful attempt", func(t *testing.T) {
		store := newPasswordMemoryStore()
		service := &Service{credentials: store}
		attempt := service.newPasswordAttempt(passwordTestConnection())
		result := requestAttemptPrompt(t, attempt, "alice@sftp.example.test's password:")
		respondToPrompt(t, service, "not saved", true)
		if got := awaitAttemptPrompt(t, result); got.err != nil {
			t.Fatal(got.err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := attempt.saveSuccessful(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("saveSuccessful error = %v, want context.Canceled", err)
		}
		_, writes, _, _ := store.snapshot()
		if len(writes) != 0 {
			t.Fatalf("canceled attempt saved a password: %v", writes)
		}
	})
}

func TestUncheckedPasswordIsNotSaved(t *testing.T) {
	store := newPasswordMemoryStore()
	service := &Service{credentials: store}
	attempt := service.newPasswordAttempt(passwordTestConnection())
	result := requestAttemptPrompt(t, attempt, "alice@sftp.example.test's password:")
	prompt := waitAuthPrompt(t, service)
	if !prompt.CanRemember {
		t.Fatal("password prompt should allow remembering")
	}
	if err := service.RespondAuthPrompt(prompt.ID, "one time", false, false); err != nil {
		t.Fatal(err)
	}
	if got := awaitAttemptPrompt(t, result); got.err != nil || got.value != "one time" {
		t.Fatalf("prompt result = (%q, %v)", got.value, got.err)
	}
	if err := attempt.saveSuccessful(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, writes, _, values := store.snapshot()
	if len(writes) != 0 || len(values) != 0 {
		t.Fatalf("unchecked password was saved: writes=%v values=%v", writes, values)
	}
}

func TestRememberedPasswordIsUsedOnceThenDeletedAfterRejection(t *testing.T) {
	store := newPasswordMemoryStore()
	service := &Service{credentials: store}
	connection := passwordTestConnection()
	prompt := "alice@sftp.example.test's password:"
	key := credentialConnectionKey(connection) + "/" + credentialHash(prompt)
	store.values[key] = "old password"
	attempt := service.newPasswordAttempt(connection)

	firstValue, firstErr := attempt.prompt(context.Background(), prompt)
	if firstErr != nil || firstValue != "old password" {
		t.Fatalf("first prompt = (%q, %v), want saved password", firstValue, firstErr)
	}
	result := requestAttemptPrompt(t, attempt, prompt)
	uiPrompt := waitAuthPrompt(t, service)
	if !uiPrompt.CanRemember {
		t.Fatal("manual retry should allow remembering")
	}
	_, _, deletes, values := store.snapshot()
	if len(deletes) != 1 || deletes[0] != key {
		t.Fatalf("rejected saved password deletion = %v, want exact key %q", deletes, key)
	}
	if _, found := values[key]; found {
		t.Fatal("rejected saved password remained in the store")
	}
	if err := service.RespondAuthPrompt(uiPrompt.ID, "replacement", false, true); err != nil {
		t.Fatal(err)
	}
	if got := awaitAttemptPrompt(t, result); got.err != nil || got.value != "replacement" {
		t.Fatalf("manual retry = (%q, %v)", got.value, got.err)
	}
	if service.GetAuthPrompt() != nil {
		t.Fatal("answered prompt remained active")
	}
	if err := attempt.saveSuccessful(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, writes, _, values := store.snapshot()
	if writes[key] != "replacement" || values[key] != "replacement" {
		t.Fatalf("replacement password was not saved after success: writes=%v values=%v", writes, values)
	}
}

func TestRememberIsUnavailableForInteractiveAndPassphrasePrompts(t *testing.T) {
	cases := []struct {
		name       string
		connection transport.Connection
		prompt     string
	}{
		{name: "keyboard interactive", connection: transport.Connection{AuthMode: "interactive", Mode: "manual", Host: "host.test", User: "alice", Port: 22}, prompt: "Verification code: "},
		{name: "key passphrase", connection: passwordTestConnection(), prompt: "Enter passphrase for key '/keys/id_ed25519':"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newPasswordMemoryStore()
			service := &Service{credentials: store}
			attempt := service.newPasswordAttempt(tc.connection)
			result := requestAttemptPrompt(t, attempt, tc.prompt)
			prompt := waitAuthPrompt(t, service)
			if prompt.CanRemember {
				t.Fatal("interactive/passphrase prompt allowed remembering")
			}
			if err := service.RespondAuthPrompt(prompt.ID, "answer", false, true); err != nil {
				t.Fatal(err)
			}
			if got := awaitAttemptPrompt(t, result); got.err != nil || got.value != "answer" {
				t.Fatalf("prompt result = (%q, %v)", got.value, got.err)
			}
			if err := attempt.saveSuccessful(context.Background()); err != nil {
				t.Fatal(err)
			}
			reads, writes, deletes, values := store.snapshot()
			if len(reads)+len(writes)+len(deletes)+len(values) != 0 {
				t.Fatalf("non-password prompt touched credential store: reads=%v writes=%v deletes=%v values=%v", reads, writes, deletes, values)
			}
		})
	}
}

func TestCredentialKeyIsolatedByConnectionAndPrompt(t *testing.T) {
	base := passwordTestConnection()
	basePrompt := "alice@sftp.example.test's password:"
	baseKey := credentialConnectionKey(base) + "/" + credentialHash(basePrompt)
	variants := []struct {
		name       string
		connection transport.Connection
		prompt     string
	}{
		{name: "host", connection: func() transport.Connection { c := base; c.Host = "other.example.test"; return c }(), prompt: basePrompt},
		{name: "port", connection: func() transport.Connection { c := base; c.Port = 2222; return c }(), prompt: basePrompt},
		{name: "user", connection: func() transport.Connection { c := base; c.User = "bob"; return c }(), prompt: basePrompt},
		{name: "alias", connection: transport.Connection{AuthMode: "password", Mode: "ssh_alias", Alias: "production"}, prompt: basePrompt},
		{name: "config path", connection: func() transport.Connection { c := base; c.ConfigPath = `C:\ssh\special config`; return c }(), prompt: basePrompt},
		{name: "prompt", connection: base, prompt: "alice@sftp.example.test's password for another account:"},
	}
	for _, tc := range variants {
		t.Run(tc.name, func(t *testing.T) {
			got := credentialConnectionKey(tc.connection) + "/" + credentialHash(tc.prompt)
			if got == baseKey {
				t.Fatalf("%s reused another connection's password key", tc.name)
			}
		})
	}

	store := newPasswordMemoryStore()
	service := &Service{credentials: store}
	store.values[baseKey] = "base secret"
	attempt := service.newPasswordAttempt(base)
	if value, err := attempt.prompt(context.Background(), basePrompt); err != nil || value != "base secret" {
		t.Fatalf("base credential lookup = (%q, %v)", value, err)
	}
	reads, _, _, _ := store.snapshot()
	if len(reads) != 1 || reads[0] != baseKey {
		t.Fatalf("credential lookup used unexpected key: %v", reads)
	}
}

func TestForgetSavedPasswordUsesResolvedConnectionKey(t *testing.T) {
	store := newPasswordMemoryStore()
	service := &Service{credentials: store}
	connection := transport.Connection{AuthMode: "password", Mode: "ssh_alias", Alias: "prod", ConfigPath: `C:\ssh\config`, Host: "ignored-host", User: "ignored-user", Port: 2202}
	service.status.Settings = config.Settings{Projects: []config.Project{{ID: "project-id", Connections: []config.ProjectConnection{{ID: "connection-id", Connection: connection}}}}}
	secret := "never pass this to delete"
	store.values["sentinel"] = secret

	if err := service.ForgetSavedPassword("project-id", "connection-id"); err != nil {
		t.Fatal(err)
	}
	_, writes, deletes, values := store.snapshot()
	wantPrefix := credentialConnectionKey(connection)
	if len(deletes) != 1 || deletes[0] != wantPrefix {
		t.Fatalf("ForgetSavedPassword delete prefix = %v, want resolved connection key %q", deletes, wantPrefix)
	}
	if strings.Contains(deletes[0], secret) || len(writes) != 0 {
		t.Fatal("raw password was passed to delete or ForgetSavedPassword wrote a value")
	}
	if _, found := values["sentinel"]; !found {
		t.Fatal("forgetting a password removed an unrelated credential")
	}
	if err := service.ForgetSavedPassword("project-id", "missing-connection"); err == nil {
		t.Fatal("ForgetSavedPassword accepted an unknown connection")
	}
	_, _, deletes, _ = store.snapshot()
	if len(deletes) != 1 {
		t.Fatalf("unknown connection triggered credential deletion: %v", deletes)
	}
}

func TestSaveFailureDoesNotExposePassword(t *testing.T) {
	secret := "top secret"
	store := newPasswordMemoryStore()
	store.writeErr = errors.New("backend detail containing no password")
	service := &Service{credentials: store}
	attempt := service.newPasswordAttempt(passwordTestConnection())
	result := requestAttemptPrompt(t, attempt, "alice@sftp.example.test's password:")
	respondToPrompt(t, service, secret, true)
	if got := awaitAttemptPrompt(t, result); got.err != nil {
		t.Fatal(got.err)
	}
	err := attempt.saveSuccessful(context.Background())
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("save error = %v, want generic error without password", err)
	}
}
