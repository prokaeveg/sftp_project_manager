package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	evaluateStdoutLimit = 2 << 20
	evaluateStderrLimit = 16 << 10
)

// Effective contains only the OpenSSH configuration fields used by the UI.
// Command values are intentionally omitted because they may contain secrets.
type Effective struct {
	Hostname        string   `json:"hostname"`
	User            string   `json:"user"`
	Port            int      `json:"port"`
	IdentityFiles   []string `json:"identityFiles"`
	ProxyJump       string   `json:"proxyJump"`
	HasProxyCommand bool     `json:"hasProxyCommand"`
	ConfigPath      string   `json:"configPath"`
	Alias           string   `json:"alias"`
}

type evaluationResult struct {
	stdout []byte
	stderr []byte
	exit   uint32
	tooBig bool
}

type boundedOutput struct {
	data   []byte
	tooBig bool
}

func readBounded(reader io.ReadCloser, limit int) boundedOutput {
	defer reader.Close()
	var result boundedOutput
	buffer := make([]byte, 32*1024)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			remaining := limit - len(result.data)
			if remaining > 0 {
				keep := min(n, remaining)
				result.data = append(result.data, buffer[:keep]...)
			}
			if n > remaining {
				result.tooBig = true
			}
		}
		if err != nil {
			return result
		}
	}
}

// Evaluate asks OpenSSH to print its effective configuration without starting
// an SSH session. Config Match exec directives run as OpenSSH defines.
func Evaluate(ctx context.Context, executable string, connection Connection) (Effective, error) {
	if connection.Mode != "ssh_alias" {
		return Effective{}, errors.New("Оценка конфигурации доступна только для SSH-алиаса")
	}
	if err := connection.Validate(); err != nil {
		return Effective{}, err
	}
	if executable == "" {
		return Effective{}, errors.New("OpenSSH не найден")
	}
	args := []string{"-G", "-T"}
	if connection.ConfigPath != "" {
		args = append(args, "-F", connection.ConfigPath)
	}
	args = append(args, "-s", "--", connection.Alias, "sftp")
	result, err := runEvaluation(ctx, executable, args)
	if err != nil {
		if ctx.Err() != nil {
			return Effective{}, ctx.Err()
		}
		return Effective{}, errors.New("Не удалось вычислить конфигурацию OpenSSH")
	}
	if result.tooBig {
		return Effective{}, errors.New("Вывод конфигурации OpenSSH превышает допустимый размер")
	}
	if result.exit != 0 {
		return Effective{}, errors.New("OpenSSH не смог вычислить конфигурацию")
	}
	return parseEffective(result.stdout, connection)
}

func parseEffective(output []byte, connection Connection) (Effective, error) {
	effective := Effective{
		IdentityFiles: []string{}, ConfigPath: connection.ConfigPath, Alias: connection.Alias,
	}
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(key) {
		case "hostname":
			effective.Hostname = value
		case "user":
			effective.User = value
		case "port":
			port, err := strconv.Atoi(value)
			if err != nil || port < 1 || port > 65535 {
				return Effective{}, errors.New("OpenSSH вернул некорректный порт")
			}
			effective.Port = port
		case "identityfile":
			effective.IdentityFiles = append(effective.IdentityFiles, value)
		case "proxyjump":
			if value != "none" {
				effective.ProxyJump = value
			}
		case "proxycommand":
			effective.HasProxyCommand = value != "none"
		}
	}
	if effective.Hostname == "" || effective.User == "" || effective.Port == 0 {
		return Effective{}, fmt.Errorf("OpenSSH не вернул обязательные параметры подключения")
	}
	return effective, nil
}
