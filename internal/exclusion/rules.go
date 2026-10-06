// Package exclusion matches project-relative paths against exclusion patterns.
package exclusion

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxPatterns    = 100
	maxPatternSize = 512
)

// Validate checks whether patterns use the supported, project-root-relative
// POSIX glob syntax.
func Validate(patterns []string) error {
	if len(patterns) > maxPatterns {
		return fmt.Errorf("слишком много правил исключения: максимум %d", maxPatterns)
	}

	for i, pattern := range patterns {
		rule := i + 1
		if !utf8.ValidString(pattern) {
			return fmt.Errorf("правило %d: некорректная кодировка UTF-8", rule)
		}
		if len(pattern) > maxPatternSize {
			return fmt.Errorf("правило %d: длина превышает %d байт", rule, maxPatternSize)
		}
		if strings.TrimSpace(pattern) == "" {
			return fmt.Errorf("правило %d: правило не может быть пустым", rule)
		}
		if strings.TrimSpace(pattern) != pattern {
			return fmt.Errorf("правило %d: удалите пробелы в начале и конце", rule)
		}
		if strings.HasPrefix(pattern, "/") {
			return fmt.Errorf("правило %d: укажите путь относительно корня проекта", rule)
		}
		if strings.ContainsAny(pattern, `\[]`) {
			return fmt.Errorf("правило %d: символы \\, [ и ] не поддерживаются", rule)
		}
		if strings.ContainsRune(pattern, ':') {
			return fmt.Errorf("правило %d: двоеточие не поддерживается", rule)
		}
		if pattern[0] == '!' || pattern[0] == '#' {
			return fmt.Errorf("правило %d: правило не может начинаться с %q", rule, pattern[0])
		}

		for _, r := range pattern {
			if unicode.IsControl(r) {
				return fmt.Errorf("правило %d: управляющие символы не поддерживаются", rule)
			}
		}

		for _, segment := range strings.Split(pattern, "/") {
			if segment == "" {
				return fmt.Errorf("правило %d: путь содержит пустой сегмент", rule)
			}
			if segment == "." || segment == ".." {
				return fmt.Errorf("правило %d: сегменты . и .. не поддерживаются", rule)
			}
			if strings.Contains(segment, "**") && segment != "**" {
				return fmt.Errorf("правило %d: ** можно использовать только как отдельный сегмент пути", rule)
			}
		}
	}

	return nil
}

// Match reports whether relative or any of its ancestors matches one of the
// project-root-relative patterns. relative must be a canonical, non-root
// project-relative path using POSIX separators. Invalid patterns or paths
// never match.
func Match(patterns []string, relative string) bool {
	if Validate(patterns) != nil || !validRelativePath(relative) {
		return false
	}

	pathSegments := strings.Split(relative, "/")
	for _, pattern := range patterns {
		if matchesAnyAncestor(strings.Split(pattern, "/"), pathSegments) {
			return true
		}
	}
	return false
}

func validRelativePath(path string) bool {
	if path == "" || !utf8.ValidString(path) || strings.HasPrefix(path, "/") || strings.Contains(path, `\`) {
		return false
	}
	for _, r := range path {
		if unicode.IsControl(r) {
			return false
		}
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func matchesAnyAncestor(pattern, path []string) bool {
	// dp[j] means the pattern prefix processed so far matches the first j path
	// segments. Keeping one row bounds memory and avoids recursive backtracking.
	dp := make([]bool, len(path)+1)
	dp[0] = true

	for _, patternSegment := range pattern {
		next := make([]bool, len(path)+1)
		if patternSegment == "**" {
			next[0] = dp[0]
			for j := 1; j <= len(path); j++ {
				next[j] = dp[j] || next[j-1]
			}
		} else {
			for j := 1; j <= len(path); j++ {
				next[j] = dp[j-1] && matchSegment(patternSegment, path[j-1])
			}
		}
		dp = next
	}

	for _, matched := range dp {
		if matched {
			return true
		}
	}
	return false
}

func matchSegment(pattern, value string) bool {
	patternRunes := []rune(pattern)
	valueRunes := []rune(value)
	// dp[j] means the pattern prefix processed so far matches j value runes.
	dp := make([]bool, len(valueRunes)+1)
	dp[0] = true

	for _, patternRune := range patternRunes {
		next := make([]bool, len(valueRunes)+1)
		if patternRune == '*' {
			next[0] = dp[0]
			for j := 1; j <= len(valueRunes); j++ {
				next[j] = dp[j] || next[j-1]
			}
		} else {
			for j := 1; j <= len(valueRunes); j++ {
				next[j] = dp[j-1] && (patternRune == '?' || strings.EqualFold(string(patternRune), string(valueRunes[j-1])))
			}
		}
		dp = next
	}
	return dp[len(valueRunes)]
}
