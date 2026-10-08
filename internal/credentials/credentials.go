// Package credentials stores application credentials in the current user's
// Windows Credential Manager vault.
package credentials

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const namespace = "SFTPProjectManager/"

// Store reads, writes, and removes credentials owned by this application.
type Store interface {
	Read(target string) (string, bool, error)
	Write(target, value string) error
	DeletePrefix(prefix string) error
}

var ErrUnsupported = errors.New("credential storage is only supported on Windows")

func validateTarget(target string) error {
	if target == "" {
		return errors.New("credential target is empty")
	}
	for _, part := range strings.Split(target, "/") {
		if part == "" {
			return errors.New("credential target has an empty path segment")
		}
		for _, r := range part {
			if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
				return errors.New("credential target must contain lowercase hexadecimal segments")
			}
		}
	}
	return nil
}

func validateValue(value string) error {
	if !utf8.ValidString(value) {
		return errors.New("credential value must be valid UTF-8")
	}
	if len(value) > 4096 {
		return errors.New("credential value exceeds 4096 bytes")
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("credential value cannot contain CR, LF, or NUL")
	}
	return nil
}

func qualifiedTarget(target string) (string, error) {
	if err := validateTarget(target); err != nil {
		return "", err
	}
	return namespace + target, nil
}

func qualifiedPrefix(prefix string) (string, error) {
	if err := validateTarget(prefix); err != nil {
		return "", fmt.Errorf("invalid credential prefix: %w", err)
	}
	return namespace + prefix, nil
}
