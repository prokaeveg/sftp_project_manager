// Package sshconfig discovers concrete aliases from OpenSSH client configuration
// files. It only reads configuration; it never starts an SSH process.
package sshconfig

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

const (
	maxDepth           = 16
	maxFiles           = 128
	maxBytes           = 2 << 20
	maxLine            = 64 << 10
	maxAliases         = 4096
	maxWarnings        = 256
	maxIncludeAttempts = 512
)

// Result is the read-only discovery result for an OpenSSH config.
type Result struct {
	ConfigPath string   `json:"configPath"`
	Aliases    []Alias  `json:"aliases"`
	Warnings   []string `json:"warnings"`
}

// Alias describes one concrete Host token. Conditional is true when the
// candidate came from an Include beneath a Host or Match block.
type Alias struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Line        int    `json:"line"`
	Conditional bool   `json:"conditional"`
}

// Discover reads configPath and its Include files. An empty configPath selects
// ~/.ssh/config. Includes are resolved relative to ~/.ssh, as OpenSSH does.
func Discover(configPath string) (Result, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return Result{}, errors.New("не удалось определить домашнюю папку пользователя")
	}
	sshDir := filepath.Join(home, ".ssh")
	if configPath == "" {
		configPath = filepath.Join(sshDir, "config")
	} else if strings.HasPrefix(configPath, "~/") || strings.HasPrefix(configPath, `~\`) {
		configPath = filepath.Join(home, configPath[2:])
	}
	absolute, err := filepath.Abs(configPath)
	if err != nil {
		return Result{}, fmt.Errorf("не удалось определить путь SSH-конфига: %w", err)
	}
	configPath = filepath.Clean(absolute)
	result := Result{ConfigPath: configPath, Aliases: []Alias{}, Warnings: []string{}}
	ctx := parser{result: &result, sshDir: sshDir, seenAliases: map[string]bool{}, active: map[string]bool{}}
	if err := ctx.readFile(configPath, 0, true); err != nil {
		return Result{}, fmt.Errorf("не удалось прочитать SSH-конфиг %q: %w (проверьте путь и права доступа)", configPath, err)
	}
	sort.Slice(result.Aliases, func(i, j int) bool { return result.Aliases[i].Name < result.Aliases[j].Name })
	return result, nil
}

type parser struct {
	result                  *Result
	sshDir                  string
	bytes                   int
	files                   int
	includeAttempts         int
	conditional             bool
	hostBlock               bool
	conditionalIncludeDepth int
	seenAliases             map[string]bool
	active                  map[string]bool
}

func (p *parser) warn(path string, line int, message string) {
	if len(p.result.Warnings) >= maxWarnings {
		return
	}
	if len(p.result.Warnings) == maxWarnings-1 {
		p.result.Warnings = append(p.result.Warnings, "достигнут предел числа предупреждений; остальные пропущены")
		return
	}
	p.result.Warnings = append(p.result.Warnings, fmt.Sprintf("%s:%d: %s", path, line, message))
}

func canonical(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		path = resolved
	}
	path, _ = filepath.Abs(path)
	path = filepath.Clean(path)
	if filepath.Separator == '\\' {
		path = strings.ToLower(path)
	}
	return path
}

func (p *parser) readFile(path string, depth int, root bool) error {
	if depth > maxDepth {
		p.warn(path, 0, "превышена максимальная глубина Include")
		return nil
	}
	key := canonical(path)
	if p.active[key] {
		p.warn(path, 0, "обнаружен цикл Include")
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if root {
			return err
		}
		p.warn(path, 0, "не удалось открыть Include-файл")
		return nil
	}
	if !info.Mode().IsRegular() {
		if root {
			return errors.New("путь не является обычным файлом")
		}
		p.warn(path, 0, "Include пропущен: это не обычный файл")
		return nil
	}
	if p.files >= maxFiles {
		p.warn(path, 0, "достигнут предел числа SSH-конфигов")
		return nil
	}
	if p.bytes >= maxBytes {
		if root {
			return errors.New("превышен предел общего размера SSH-конфигов")
		}
		p.warn(path, 0, "достигнут предел общего размера SSH-конфигов")
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		if root {
			return err
		}
		p.warn(path, 0, "не удалось открыть Include-файл")
		return nil
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() {
		if root {
			return errors.New("путь не является обычным файлом")
		}
		p.warn(path, 0, "Include пропущен: это не обычный файл")
		return nil
	}
	limit := int64(maxBytes-p.bytes) + 1
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		if root {
			return err
		}
		p.warn(path, 0, "ошибка чтения Include-файла")
		return nil
	}
	if len(data) > maxBytes-p.bytes {
		if root {
			return errors.New("превышен предел общего размера SSH-конфигов")
		}
		p.warn(path, 0, "превышен предел общего размера SSH-конфигов")
		return nil
	}
	p.bytes += len(data)
	p.files++
	p.active[key] = true
	defer delete(p.active, key)

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 4096), maxBytes+1)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if len(line) > maxLine {
			p.warn(path, lineNo, "строка длиннее допустимого лимита; пропущена")
			continue
		}
		words, quoteKind, ok := tokenize(line)
		if quoteKind == '\'' {
			p.warn(path, lineNo, "одинарные кавычки в SSH-конфиге не поддерживаются; строка пропущена")
			continue
		}
		if !ok || len(words) == 0 {
			if !ok {
				p.warn(path, lineNo, "не удалось разобрать строку SSH-конфига")
			}
			continue
		}
		key := strings.ToLower(strings.TrimSuffix(words[0], "="))
		args := words[1:]
		if strings.HasSuffix(words[0], "=") && len(args) == 0 {
			continue
		}
		if key == "host" {
			conditionalHost := p.conditionalIncludeDepth > 0
			if len(args) == 0 {
				p.warn(path, lineNo, "Host без шаблонов пропущен")
			}
			p.conditional = false
			p.hostBlock = true
			for _, token := range args {
				p.addAlias(path, lineNo, token, conditionalHost)
			}
			continue
		}
		if key == "match" {
			if len(args) == 0 {
				p.warn(path, lineNo, "Match без условий")
			}
			p.conditional = true
			for i := 0; i < len(args); i++ {
				if strings.EqualFold(strings.TrimPrefix(args[i], "!"), "exec") {
					p.warn(path, lineNo, "Match exec не выполняется при обнаружении SSH-алиасов")
				}
			}
			continue
		}
		if key == "include" {
			if len(args) == 0 {
				p.warn(path, lineNo, "Include без пути")
			}
			if p.isConditional() {
				p.warn(path, lineNo, "Include внутри условного блока: найденные алиасы не подтверждены")
			}
			for _, pattern := range args {
				p.include(path, lineNo, pattern, depth+1)
			}
			continue
		}
		if key == "proxycommand" || key == "localcommand" {
			p.warn(path, lineNo, key+" не выполняется при обнаружении алиасов")
		}
	}
	if err := scanner.Err(); err != nil {
		p.warn(path, lineNo+1, "ошибка чтения строки SSH-конфига или превышен лимит строки")
	}
	return nil
}

func (p *parser) addAlias(path string, line int, name string, conditionalHost bool) {
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "*?!@ \t") {
		return
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return
		}
	}
	// OpenSSH may interpolate aliases into proxy commands without shell escaping.
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._-", r) {
			p.warn(path, line, "алиас с неподдерживаемыми символами команды пропущен")
			return
		}
	}
	if p.seenAliases[name] {
		return
	}
	if len(p.result.Aliases) >= maxAliases {
		p.warn(path, line, "достигнут предел числа SSH-алиасов")
		return
	}
	p.seenAliases[name] = true
	p.result.Aliases = append(p.result.Aliases, Alias{Name: name, Source: path, Line: line, Conditional: conditionalHost || p.conditionalIncludeDepth > 0})
}

func (p *parser) isMatchConditional() bool { return p.conditional || p.conditionalIncludeDepth > 0 }

func (p *parser) isConditional() bool {
	return p.isMatchConditional() || p.hostBlock
}

func (p *parser) include(source string, line int, pattern string, depth int) {
	if p.files >= maxFiles || p.bytes >= maxBytes || p.includeAttempts >= maxIncludeAttempts {
		p.warn(source, line, "достигнут предел Include; дальнейшая обработка пропущена")
		return
	}
	if pattern == "" || strings.ContainsAny(pattern, "%${") || (strings.HasPrefix(pattern, "~") && !strings.HasPrefix(pattern, "~/") && !strings.HasPrefix(pattern, `~\`)) {
		p.warn(source, line, "Include с пользовательскими или динамическими подстановками пропущен")
		return
	}
	if strings.HasPrefix(pattern, "~/") || strings.HasPrefix(pattern, `~\`) {
		pattern = filepath.Join(p.home(), pattern[2:])
	} else if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(p.sshDir, pattern)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		p.warn(source, line, "некорректный шаблон Include")
		return
	}
	if len(matches) == 0 {
		p.warn(source, line, "шаблон Include не совпал ни с одним файлом")
		return
	}
	sort.Strings(matches)
	for index, match := range matches {
		if index >= maxFiles || p.files >= maxFiles || p.bytes >= maxBytes || p.includeAttempts >= maxIncludeAttempts {
			p.warn(source, line, "достигнут предел Include; остальные совпадения пропущены")
			break
		}
		p.includeAttempts++
		inConditionalBlock := p.isConditional()
		if inConditionalBlock {
			p.conditionalIncludeDepth++
		}
		_ = p.readFile(match, depth, false)
		if inConditionalBlock {
			p.conditionalIncludeDepth--
		}
	}
}

func (p *parser) home() string { h, _ := os.UserHomeDir(); return h }

// tokenize follows the whitespace, '=' and quoted-value structure used by
// ssh_config. Comments begin at an unquoted #; double quotes may contain spaces.
func tokenize(line string) ([]string, rune, bool) {
	var words []string
	var b strings.Builder
	var quote rune
	escaped, started, usedSingle := false, false, false
	flush := func() {
		if started {
			words = append(words, b.String())
			b.Reset()
			started = false
		}
	}
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escaped {
			b.WriteRune(r)
			escaped = false
			started = true
			continue
		}
		if r == '\\' && quote != '\'' {
			if i+1 < len(runes) && (runes[i+1] == '\\' || runes[i+1] == '"') {
				escaped = true
				started = true
				continue
			}
			b.WriteRune(r)
			started = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		if r == '"' || r == '\'' {
			quote = r
			if r == '\'' {
				usedSingle = true
			}
			started = true
			continue
		}
		if r == '#' {
			break
		}
		if r == ' ' || r == '\t' {
			flush()
			continue
		}
		b.WriteRune(r)
		started = true
	}
	if escaped {
		b.WriteRune('\\')
	}
	flush()
	if quote != 0 {
		return nil, quote, false
	}
	// Split assignment syntax only at the directive token. Equals signs in
	// values (notably Include paths) remain literal.
	if len(words) > 0 {
		if equal := strings.IndexRune(words[0], '='); equal >= 0 {
			first, value := words[0][:equal], words[0][equal+1:]
			words = append([]string{first}, words[1:]...)
			if value != "" {
				words = append(words[:1], append([]string{value}, words[1:]...)...)
			}
		}
		if len(words) >= 2 {
			if words[1] == "=" {
				words = append(words[:1], words[2:]...)
			} else if strings.HasPrefix(words[1], "=") {
				words[1] = words[1][1:]
			}
		}
	}
	if usedSingle {
		return nil, '\'', true
	}
	return words, 0, true
}
