// Package mapping maps paths beneath a project's local root to one remote
// POSIX root, and maps remote paths back into the local project.
package mapping

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

// Mapper represents one local-root to remote-root mapping.
type Mapper struct {
	localRoot  string
	remoteRoot string
}

// New creates a mapper for an existing absolute local project directory and
// an absolute POSIX remote directory. The local root and its existing
// ancestors must not contain symlinks or junctions. Root paths are validated
// when the mapper is created; callers should create a new mapper if the root
// is replaced or changed.
func New(localRoot, remoteRoot string) (*Mapper, error) {
	if strings.TrimSpace(localRoot) == "" {
		return nil, fmt.Errorf("local root is empty")
	}
	if !filepath.IsAbs(localRoot) {
		return nil, fmt.Errorf("local root must be an absolute path")
	}
	absRoot, err := filepath.Abs(localRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve local root: %w", err)
	}
	absRoot = filepath.Clean(absRoot)
	if err := rejectLinks(absRoot, false); err != nil {
		return nil, fmt.Errorf("invalid local root: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("stat local root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("local root is not a directory")
	}
	if remoteRoot == "" || !path.IsAbs(remoteRoot) {
		return nil, fmt.Errorf("remote root must be an absolute POSIX path")
	}
	if strings.ContainsRune(remoteRoot, '\\') {
		return nil, fmt.Errorf("remote root must use POSIX separators")
	}
	for _, r := range remoteRoot {
		if unicode.IsControl(r) {
			return nil, fmt.Errorf("remote root contains a control character")
		}
	}
	if hasDotDotSegment(remoteRoot, "/") {
		return nil, fmt.Errorf("remote root must not contain '..' segments")
	}
	return &Mapper{localRoot: absRoot, remoteRoot: path.Clean(remoteRoot)}, nil
}

// LocalToRemote maps a local path relative to the project root to its remote
// POSIX path. Existing symlinks and junctions in the target path are rejected.
func (m *Mapper) LocalToRemote(relativePath string) (string, error) {
	if relativePath == "" {
		return "", fmt.Errorf("local relative path is empty")
	}
	if filepath.IsAbs(relativePath) || filepath.VolumeName(relativePath) != "" {
		return "", fmt.Errorf("local path must be relative to the project root")
	}
	if hasDotDotSegment(relativePath, string(filepath.Separator)) {
		return "", fmt.Errorf("local path must not contain '..' segments")
	}
	clean := filepath.Clean(relativePath)
	if clean == "." {
		return m.remoteRoot, nil
	}
	target := filepath.Join(m.localRoot, clean)
	if !withinLocal(m.localRoot, target) {
		return "", fmt.Errorf("local path escapes the project root")
	}
	if err := rejectLinks(target, true); err != nil {
		return "", fmt.Errorf("invalid local path: %w", err)
	}
	rel, err := filepath.Rel(m.localRoot, target)
	if err != nil {
		return "", fmt.Errorf("resolve local path: %w", err)
	}
	remoteRel := filepath.ToSlash(rel)
	return path.Join(m.remoteRoot, remoteRel), nil
}

// RemoteRelativeToAbsolute maps a POSIX path relative to the configured remote
// root to an absolute remote path. It does not inspect the local filesystem or
// apply Windows filename rules, so it is suitable for remote directory listing.
func (m *Mapper) RemoteRelativeToAbsolute(relative string) (string, error) {
	if relative == "" || path.IsAbs(relative) {
		return "", fmt.Errorf("remote relative path must be non-empty and relative")
	}
	if strings.ContainsRune(relative, '\\') {
		return "", fmt.Errorf("remote relative path must use POSIX separators")
	}
	for _, r := range relative {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("remote relative path contains a control character")
		}
	}
	if hasDotDotSegment(relative, "/") {
		return "", fmt.Errorf("remote relative path must not contain '..' segments")
	}
	clean := path.Clean(relative)
	if clean == "." {
		return m.remoteRoot, nil
	}
	remote := path.Join(m.remoteRoot, clean)
	if m.remoteRoot != "/" && !strings.HasPrefix(remote, m.remoteRoot+"/") {
		return "", fmt.Errorf("remote relative path escapes the configured remote root")
	}
	return remote, nil
}

// RemoteToLocal maps an absolute remote POSIX path beneath the remote root to
// a local path. The result need not exist, but existing path components must
// not be symlinks or junctions and every remote name must be valid on Windows.
func (m *Mapper) RemoteToLocal(remotePath string) (string, error) {
	if remotePath == "" || !path.IsAbs(remotePath) {
		return "", fmt.Errorf("remote path must be absolute")
	}
	if hasDotDotSegment(remotePath, "/") {
		return "", fmt.Errorf("remote path must not contain '..' segments")
	}
	clean := path.Clean(remotePath)
	var rel string
	switch {
	case clean == m.remoteRoot:
		rel = "."
	case m.remoteRoot == "/":
		rel = strings.TrimPrefix(clean, "/")
	case strings.HasPrefix(clean, m.remoteRoot+"/"):
		rel = strings.TrimPrefix(clean, m.remoteRoot+"/")
	default:
		return "", fmt.Errorf("remote path is outside the configured remote root")
	}
	if rel == "." || rel == "" {
		return m.localRoot, nil
	}
	segments := strings.Split(rel, "/")
	for _, segment := range segments {
		if err := validateWindowsName(segment); err != nil {
			return "", fmt.Errorf("invalid remote path segment %q: %w", segment, err)
		}
	}
	if err := rejectCaseCollisions(m.localRoot, segments); err != nil {
		return "", fmt.Errorf("invalid local target: %w", err)
	}
	target := m.localRoot
	for _, segment := range segments {
		target = filepath.Join(target, segment)
	}
	if !withinLocal(m.localRoot, target) {
		return "", fmt.Errorf("remote path escapes the project root")
	}
	if err := rejectLinks(target, true); err != nil {
		return "", fmt.Errorf("invalid local target: %w", err)
	}
	return target, nil
}

// rejectCaseCollisions catches an existing Windows name with the same
// case-insensitive spelling before a download can address it under another
// spelling. Missing directories are valid because download targets may not
// exist yet.
func rejectCaseCollisions(root string, segments []string) error {
	parent := root
	for _, segment := range segments {
		entries, err := os.ReadDir(parent)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), segment) && entry.Name() != segment {
				return fmt.Errorf("target name %q collides with existing name %q by case", segment, entry.Name())
			}
		}
		parent = filepath.Join(parent, segment)
	}
	return nil
}

func hasDotDotSegment(value, separator string) bool {
	// Accept either local separator on Windows; on POSIX only '/' has path
	// meaning, while a backslash remains an ordinary filename character.
	if separator == `\` {
		value = strings.ReplaceAll(value, "/", `\`)
	}
	for _, segment := range strings.Split(value, separator) {
		if segment == ".." {
			return true
		}
	}
	return false
}

func withinLocal(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// rejectLinks checks every existing component. A missing component is allowed
// for download targets; the first missing component ends the check because
// deeper components cannot exist without it.
func rejectLinks(target string, allowMissing bool) error {
	volume := filepath.VolumeName(target)
	rest := strings.TrimLeft(target[len(volume):], `/\`)
	current := volume
	if current == "" || !strings.HasSuffix(current, string(filepath.Separator)) {
		current += string(filepath.Separator)
	}
	for _, segment := range strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' }) {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			if allowMissing && os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if isLinkOrJunction(current, info) {
			return fmt.Errorf("path component %q is a symlink or junction", current)
		}
	}
	return nil
}

func validateWindowsName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("empty or traversal name")
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return fmt.Errorf("Windows names cannot end in a dot or space")
	}
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(`<>":|?*\`, r) {
			return fmt.Errorf("Windows name contains an invalid character")
		}
	}
	base := name
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimRight(base, " .")
	upper := strings.ToUpper(base)
	switch upper {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return fmt.Errorf("Windows device names are reserved")
	}
	if len([]rune(upper)) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) {
		last := []rune(upper)[3]
		if last >= '1' && last <= '9' || last == '¹' || last == '²' || last == '³' {
			return fmt.Errorf("Windows device names are reserved")
		}
	}
	return nil
}
