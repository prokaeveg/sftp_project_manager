package gitstatus

import (
	"context"
	"errors"
	"os/exec"
	"path"
	"strings"
	"time"
)

type Branch struct {
	Ref  string `json:"ref"`
	Name string `json:"name"`
}

func Branches(ctx context.Context, root string) ([]Branch, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	executable, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	data, err := command(ctx, executable, root, "for-each-ref", "--sort=refname", "--format=%(refname)%00%(symref)", "refs/heads/", "refs/remotes/")
	if err != nil {
		return nil, errors.New("Git: не удалось прочитать ветки")
	}
	result := []Branch{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		ref, sym, ok := strings.Cut(strings.TrimSuffix(line, "\r"), "\x00")
		if !ok || sym != "" {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/remotes/")
		result = append(result, Branch{Ref: ref, Name: name})
	}
	return result, nil
}

func ReadBlob(ctx context.Context, root, ref, relative string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if !safePath(relative) || strings.Contains(relative, "\\") || path.Clean(relative) != relative {
		return nil, errors.New("Git: некорректный путь")
	}
	branches, err := Branches(ctx, root)
	if err != nil {
		return nil, err
	}
	found := false
	for _, b := range branches {
		if b.Ref == ref {
			found = true
			break
		}
	}
	if !found {
		return nil, errors.New("Git: выбранная ветка больше не существует")
	}
	executable, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	commit, err := command(ctx, executable, root, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return nil, errors.New("Git: не удалось прочитать коммит ветки")
	}
	prefix, err := command(ctx, executable, root, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}
	object := strings.TrimSpace(string(commit)) + ":" + strings.TrimRight(string(prefix), "\r\n") + relative
	kind, err := command(ctx, executable, root, "cat-file", "-t", object)
	if err != nil || strings.TrimSpace(string(kind)) != "blob" {
		return nil, errors.New("Git: файл отсутствует в выбранной ветке")
	}
	data, err := command(ctx, executable, root, "cat-file", "blob", object)
	if err != nil {
		return nil, errors.New("Git: не удалось прочитать файл (таймаут или лимит 8 МиБ)")
	}
	return data, nil
}
