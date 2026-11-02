// Package gitstatus reads Git's index and worktree status without modifying either.
package gitstatus

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type File struct {
	IsDir        bool   `json:"isDir,omitempty"`
	Path         string `json:"path"`
	Status       string `json:"status"`
	PreviousPath string `json:"previousPath,omitempty"`
}
type Snapshot struct {
	Available bool   `json:"available"`
	Message   string `json:"message,omitempty"`
	Files     []File `json:"files"`
}

const outputLimit = 8 * 1024 * 1024
const fileLimit = 100000

var errLimit = errors.New("Git: превышен лимит данных статуса")

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > outputLimit {
		return 0, errLimit
	}
	return b.Buffer.Write(p)
}
func command(ctx context.Context, executable, root string, args ...string) ([]byte, error) {
	argv := append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-C", root}, args...)
	cmd := exec.CommandContext(ctx, executable, argv...)
	hideWindow(cmd)
	// Do not let a parent shell redirect status to an unrelated repository/index.
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	cmd.WaitDelay = time.Second
	var out limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func safePath(name string) bool {
	return name != "" && !filepath.IsAbs(name) && name != ".." && !strings.HasPrefix(name, "../") && !strings.Contains(name, "\x00")
}
func scopedPath(name, prefix string) (string, bool) {
	if prefix != "" {
		if !strings.HasPrefix(name, prefix) {
			return "", false
		}
		name = strings.TrimPrefix(name, prefix)
	}
	return name, safePath(name)
}
func classify(code string) string {
	if code == "!!" {
		return "ignored"
	}
	if code == "??" {
		return "untracked"
	}
	if strings.Contains(code, "U") || code == "AA" || code == "DD" {
		return "conflict"
	}
	if strings.Contains(code, "D") {
		return "deleted"
	}
	if strings.Contains(code, "A") || strings.Contains(code, "C") {
		return "added"
	}
	return "modified"
}
func parse(tracked, status []byte, prefix string) ([]File, error) {
	files := map[string]File{}
	for _, raw := range bytes.Split(tracked, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		if name, ok := scopedPath(string(raw), prefix); ok {
			files[name] = File{Path: name, Status: "clean"}
		}
		if len(files) > fileLimit {
			return nil, errLimit
		}
	}
	records := bytes.Split(status, []byte{0})
	for i := 0; i < len(records); i++ {
		record := string(records[i])
		if record == "" {
			continue
		}
		if len(record) < 4 || record[2] != ' ' {
			return nil, errors.New("Git: некорректный формат статуса")
		}
		code, name := record[:2], record[3:]
		previous := ""
		if strings.ContainsAny(code, "RC") {
			i++
			if i >= len(records) || len(records[i]) == 0 {
				return nil, errors.New("Git: неполное переименование")
			}
			previous = string(records[i])
		}
		isDir := strings.HasSuffix(name, "/")
		name = strings.TrimSuffix(name, "/")
		name, ok := scopedPath(name, prefix)
		if !ok {
			continue
		}
		old, _ := scopedPath(previous, prefix)
		files[name] = File{Path: name, Status: classify(code), PreviousPath: old, IsDir: isDir}
		if old != "" {
			delete(files, old)
		}
		if len(files) > fileLimit {
			return nil, errLimit
		}
	}
	result := make([]File, 0, len(files))
	for _, file := range files {
		result = append(result, file)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}
func Read(ctx context.Context, root string) (Snapshot, error) {
	result := Snapshot{Files: []File{}}
	executable, err := exec.LookPath("git")
	if err != nil {
		result.Message = "Git не найден в PATH"
		return result, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	prefix, err := command(ctx, executable, root, "rev-parse", "--show-prefix")
	if err != nil {
		if ctx.Err() != nil {
			return result, errors.New("Git: чтение статуса отменено или превышен таймаут")
		}
		result.Message = "Папка проекта не является доступным Git-репозиторием"
		return result, nil
	}
	tracked, err := command(ctx, executable, root, "ls-files", "-z", "--cached", "--full-name", "--", ".")
	if err != nil {
		return result, errors.New("Git: не удалось прочитать индекс (таймаут или лимит 8 МиБ)")
	}
	status, err := command(ctx, executable, root, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching", "--ignore-submodules=all", "--", ".")
	if err != nil {
		return result, errors.New("Git: не удалось прочитать изменения (таймаут или лимит 8 МиБ)")
	}
	result.Files, err = parse(tracked, status, strings.TrimSuffix(strings.TrimSuffix(string(prefix), "\n"), "\r"))
	if err != nil {
		return result, err
	}
	result.Available = true
	return result, nil
}
