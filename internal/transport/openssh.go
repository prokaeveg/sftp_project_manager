// Package transport runs the system OpenSSH SFTP subsystem without a shell.
package transport

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/pkg/sftp"
)

type Connection struct {
	Mode           string `json:"mode"`
	Alias          string `json:"alias"`
	ConfigPath     string `json:"configPath"`
	Host           string `json:"host"`
	User           string `json:"user"`
	Port           int    `json:"port"`
	IdentityFile   string `json:"identityFile"`
	KnownHostsFile string `json:"knownHostsFile"`
}

func validToken(value string) bool {
	return value != "" && !strings.HasPrefix(value, "-") && !strings.ContainsAny(value, " \t\r\n\x00")
}

func (c Connection) Validate() error {
	if c.Mode == "ssh_alias" {
		if !validToken(c.Alias) {
			return errors.New("укажите конкретный SSH-алиас")
		}
		for _, r := range c.Alias {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '_' && r != '-' {
				return errors.New("алиас может содержать только буквы, цифры, точку, подчёркивание и дефис")
			}
		}
	} else if c.Mode == "manual" {
		if !validToken(c.Host) || strings.ContainsAny(c.Host, "@/\\") || !validToken(c.User) {
			return errors.New("укажите корректные host и user")
		}
		if c.Port < 1 || c.Port > 65535 {
			return errors.New("порт должен быть от 1 до 65535")
		}
	} else {
		return errors.New("неизвестный режим подключения")
	}
	for _, file := range []string{c.ConfigPath, c.IdentityFile, c.KnownHostsFile} {
		if file != "" && (!filepath.IsAbs(file) || strings.ContainsAny(file, "\r\n\x00\"")) {
			return errors.New("пути SSH-файлов должны быть абсолютными и без управляющих символов или кавычек")
		}
	}
	return nil
}

func Arguments(c Connection) ([]string, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	args := []string{"-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UpdateHostKeys=no", "-o", "ConnectTimeout=10", "-o", "ConnectionAttempts=1", "-o", "ServerAliveInterval=5", "-o", "ServerAliveCountMax=2", "-o", "ProxyJump=none", "-o", "ProxyCommand=none", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ForwardAgent=no", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no", "-o", "LogLevel=INFO"}
	if c.Mode == "ssh_alias" && supportsConfigProxy() {
		args = removeProxyOverrides(args)
	}
	args = append(args, "-o", "ForkAfterAuthentication=no", "-o", "StdinNull=no", "-o", "RequestTTY=no")
	if c.ConfigPath != "" && c.Mode == "ssh_alias" {
		args = append(args, "-F", c.ConfigPath)
	}
	if c.KnownHostsFile != "" {
		// OpenSSH option values containing spaces must be quoted even as individual argv elements.
		args = append(args, "-o", `UserKnownHostsFile="`+filepath.ToSlash(c.KnownHostsFile)+`"`, "-o", "GlobalKnownHostsFile=none")
	}
	target := c.Alias
	if c.Mode == "manual" {
		args = append(args, "-F", nullConfig(), "-l", c.User, "-p", strconv.Itoa(c.Port))
		if c.IdentityFile != "" {
			args = append(args, "-i", c.IdentityFile, "-o", "IdentitiesOnly=yes")
		}
		target = c.Host
	}
	return append(args, "-s", "--", target, "sftp"), nil
}

type diagnostics struct {
	mu   sync.Mutex
	data []byte
}

func (d *diagnostics) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.data) < 16384 {
		n := min(len(p), 16384-len(d.data))
		d.data = append(d.data, p[:n]...)
	}
	return len(p), nil
}
func (d *diagnostics) message() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	value := strings.ToLower(string(d.data))
	switch {
	case strings.Contains(value, "host key verification failed"), strings.Contains(value, "host identification has changed"):
		return "ключ сервера неизвестен или изменился; проверьте отпечаток и known_hosts вручную. Подключение остановлено"
	case strings.Contains(value, "permission denied"):
		return "SSH-авторизация не удалась: нужен доступный ключ или SSH-agent; пароль, passphrase и MFA интерактивно не поддерживаются"
	case strings.Contains(value, "could not resolve"), strings.Contains(value, "connect to host"), strings.Contains(value, "connection refused"), strings.Contains(value, "connection reset"), strings.Contains(value, "timed out"), strings.Contains(value, "network is unreachable"):
		return "сервер недоступен: проверьте host, порт и сеть"
	default:
		return "не удалось запустить SSH/SFTP; проверьте подключение и доверенный SSH-конфиг"
	}
}

type Session struct {
	Client  *sftp.Client
	process *managedProcess
	cancel  context.CancelFunc
	once    sync.Once
}

func Open(ctx context.Context, executable string, connection Connection) (*Session, error) {
	args, err := Arguments(connection)
	if err != nil {
		return nil, err
	}
	if executable == "" {
		return nil, errors.New("OpenSSH не найден в PATH")
	}
	if connection.Mode == "ssh_alias" && !supportsConfigProxy() {
		return nil, errors.New("подключения по SSH-алиасу доступны только в Windows-сборке")
	}
	childCtx, cancel := context.WithCancel(ctx)
	var diagnostic diagnostics
	process, err := startManaged(childCtx, executable, args, true, &diagnostic)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("не удалось запустить OpenSSH: %w", err)
	}
	client, err := sftp.NewClientPipe(process.Output, process.Input)
	if err != nil {
		_ = process.Close()
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New(diagnostic.message())
	}
	return &Session{Client: client, process: process, cancel: cancel}, nil
}

// Close interrupts I/O and reaps the SSH process. It is safe to call repeatedly.
func (s *Session) Close() {
	s.once.Do(func() { s.cancel(); _ = s.process.Close(); _ = s.Client.Close() })
}

func removeProxyOverrides(args []string) []string {
	out := make([]string, 0, len(args)-4)
	for i := 0; i < len(args); i++ {
		if args[i] == "-o" && i+1 < len(args) && (strings.HasPrefix(args[i+1], "ProxyJump=") || strings.HasPrefix(args[i+1], "ProxyCommand=")) {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}
