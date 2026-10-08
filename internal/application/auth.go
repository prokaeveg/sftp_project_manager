package application

import (
	"context"
	"errors"
	"strings"
	"time"
)

// AuthPrompt contains only the question. Answers never enter settings/status.
type AuthPrompt struct {
	ID          string `json:"id"`
	Prompt      string `json:"prompt"`
	CanRemember bool   `json:"canRemember"`
}

type authAnswer struct {
	value     string
	cancelled bool
	remember  bool
}
type pendingAuth struct {
	prompt   AuthPrompt
	answer   chan authAnswer
	ctx      context.Context
	answered bool
}

func (s *Service) requestAuth(ctx context.Context, prompt string) (string, error) {
	answer, err := s.requestAuthAnswer(ctx, prompt, false)
	return answer.value, err
}

func (s *Service) requestAuthAnswer(ctx context.Context, prompt string, canRemember bool) (authAnswer, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	id, err := newID()
	if err != nil {
		return authAnswer{}, errors.New("не удалось открыть запрос авторизации")
	}
	if len(prompt) > 4096 {
		prompt = prompt[:4096]
	}
	request := &pendingAuth{prompt: AuthPrompt{ID: id, Prompt: prompt, CanRemember: canRemember}, answer: make(chan authAnswer, 1), ctx: ctx}
	s.authMu.Lock()
	if s.authClosed || ctx.Err() != nil {
		s.authMu.Unlock()
		return authAnswer{}, context.Canceled
	}
	if s.authPending != nil {
		s.authMu.Unlock()
		return authAnswer{}, errors.New("другой запрос авторизации ещё выполняется")
	}
	s.authPending = request
	s.authMu.Unlock()
	defer func() {
		s.authMu.Lock()
		if s.authPending == request {
			s.authPending = nil
		}
		s.authMu.Unlock()
	}()
	select {
	case answer := <-request.answer:
		if answer.cancelled {
			return authAnswer{}, context.Canceled
		}
		if err := ctx.Err(); err != nil {
			return authAnswer{}, err
		}
		return answer, nil
	case <-ctx.Done():
		return authAnswer{}, ctx.Err()
	}
}

func (s *Service) GetAuthPrompt() *AuthPrompt {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.authPending == nil || s.authPending.answered || s.authPending.ctx.Err() != nil {
		return nil
	}
	prompt := s.authPending.prompt
	return &prompt
}

func (s *Service) RespondAuthPrompt(id, answer string, cancelled bool, remember ...bool) error {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	request := s.authPending
	if request == nil || request.prompt.ID != id || request.answered || request.ctx.Err() != nil {
		return errors.New("запрос авторизации устарел")
	}
	if !cancelled && (len(answer) > 4096 || strings.ContainsAny(answer, "\r\n\x00")) {
		return errors.New("ответ слишком длинный или содержит недопустимые символы")
	}
	if cancelled {
		answer = ""
	}
	request.answered = true
	request.answer <- authAnswer{value: answer, cancelled: cancelled, remember: !cancelled && request.prompt.CanRemember && len(remember) > 0 && remember[0]}
	return nil
}

func (s *Service) shutdownAuth() {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	s.authClosed = true
	if request := s.authPending; request != nil && !request.answered {
		request.answered = true
		request.answer <- authAnswer{cancelled: true}
	}
}
