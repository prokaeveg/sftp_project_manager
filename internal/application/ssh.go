package application

import (
	"awesomeProject/internal/sshconfig"
	"awesomeProject/internal/transport"
	"context"
	"errors"
	"time"
)

// DiscoverSSH reads text only. It does not evaluate Match exec or connect.
func (s *Service) DiscoverSSH(configPath string) (sshconfig.Result, error) {
	return sshconfig.Discover(configPath)
}

func (s *Service) EvaluateSSH(parent context.Context, connection transport.Connection, trusted bool) (transport.Effective, error) {
	if !trusted {
		return transport.Effective{}, errors.New("подтвердите доверие SSH-конфигу перед проверкой настроек")
	}
	if !s.operationMu.TryLock() {
		return transport.Effective{}, errors.New("другая операция ещё выполняется")
	}
	defer s.operationMu.Unlock()
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	s.mu.Lock()
	s.cancelOperation = cancel
	executable := s.status.SSHPath
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.cancelOperation = nil; s.mu.Unlock() }()
	result, err := transport.Evaluate(ctx, executable, connection)
	if errors.Is(err, context.Canceled) {
		return transport.Effective{}, errors.New("проверка настроек отменена")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return transport.Effective{}, errors.New("проверка настроек превысила 15 секунд и остановлена")
	}
	return result, err
}
