package application

import (
	"context"
	"strings"
	"testing"
	"time"
)

func waitAuthPrompt(t *testing.T, s *Service) *AuthPrompt {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if prompt := s.GetAuthPrompt(); prompt != nil {
			return prompt
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("auth prompt did not appear")
	return nil
}

func TestAuthPromptAnswerIsSingleUseAndNotExposed(t *testing.T) {
	s := &Service{}
	result := make(chan string, 1)
	go func() { answer, _ := s.requestAuth(context.Background(), "Password:"); result <- answer }()
	prompt := waitAuthPrompt(t, s)
	if err := s.RespondAuthPrompt("stale", "wrong", false); err == nil {
		t.Fatal("accepted stale response")
	}
	secret := "пароль $`'&\""
	if err := s.RespondAuthPrompt(prompt.ID, secret, false); err != nil {
		t.Fatal(err)
	}
	if s.GetAuthPrompt() != nil {
		t.Fatal("answered prompt still exposed")
	}
	if err := s.RespondAuthPrompt(prompt.ID, secret, false); err == nil {
		t.Fatal("accepted duplicate response")
	}
	if answer := <-result; answer != secret {
		t.Fatal("answer changed")
	}
}

func TestAuthPromptCancellationAndShutdown(t *testing.T) {
	for _, mode := range []string{"response", "context", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			s := &Service{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := s.requestAuth(ctx, "Password:"); result <- err }()
			prompt := waitAuthPrompt(t, s)
			switch mode {
			case "response":
				if err := s.RespondAuthPrompt(prompt.ID, "discard this", true); err != nil {
					t.Fatal(err)
				}
			case "context":
				cancel()
			case "shutdown":
				s.shutdownAuth()
			}
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("cancellation succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation hung")
			}
			if s.GetAuthPrompt() != nil {
				t.Fatal("cancelled prompt still visible")
			}
			if err := s.RespondAuthPrompt(prompt.ID, "late", false); err == nil {
				t.Fatal("accepted late secret")
			}
		})
	}
}

func TestAuthPromptRejectsInvalidAnswersWithoutConsuming(t *testing.T) {
	s := &Service{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = s.requestAuth(ctx, "Password:") }()
	prompt := waitAuthPrompt(t, s)
	for _, invalid := range []string{"a\nb", "a\rb", "a\x00b", strings.Repeat("a", 4097)} {
		if err := s.RespondAuthPrompt(prompt.ID, invalid, false); err == nil {
			t.Fatal("accepted invalid answer")
		}
		if s.GetAuthPrompt() == nil {
			t.Fatal("invalid answer consumed prompt")
		}
	}
	cancel()
	<-done
}
