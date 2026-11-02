package gitstatus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}
func put(t *testing.T, root, name, content string) {
	t.Helper()
	file := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestReadRealRepositoryWithoutMutation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	git(t, root, "init", "-q")
	git(t, root, "config", "user.email", "fixture@example.invalid")
	git(t, root, "config", "user.name", "Fixture")
	for _, name := range []string{"clean.txt", "modified.txt", "deleted.txt", "rename old.txt", "sub/unchanged.txt", "sub/changed.txt"} {
		put(t, root, name, "original\n")
	}
	put(t, root, ".gitignore", "ignored.txt\nignored-dir/\n*.log\n!keep.log\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "fixture")
	put(t, root, "modified.txt", "modified\n")
	put(t, root, "sub/changed.txt", "changed\n")
	put(t, root, "untracked orange.txt", "new")
	put(t, root, "new.txt", "added")
	put(t, root, "ignored.txt", "ignored")
	put(t, root, "ignored-dir/nested/file.txt", "ignored")
	put(t, root, "sub/ignored.log", "ignored")
	put(t, root, "keep.log", "visible")
	git(t, root, "add", "new.txt")
	git(t, root, "mv", "rename old.txt", "rename new.txt")
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := Read(context.Background(), root)
	if err != nil || !snapshot.Available {
		t.Fatalf("read: %v", err)
	}
	statuses := map[string]string{}
	for _, file := range snapshot.Files {
		statuses[file.Path] = file.Status
	}
	for name, want := range map[string]string{"clean.txt": "clean", "modified.txt": "modified", "deleted.txt": "deleted", "rename new.txt": "modified", "untracked orange.txt": "untracked", "new.txt": "added"} {
		if statuses[name] != want {
			t.Errorf("%s = %s, want %s", name, statuses[name], want)
		}
	}
	for _, name := range []string{"ignored.txt", "ignored-dir", "sub/ignored.log"} {
		if statuses[name] != "ignored" {
			t.Fatalf("ignored status missing for %s: %s", name, statuses[name])
		}
	}
	if statuses["keep.log"] != "untracked" {
		t.Fatal("gitignore negation not honored")
	}
	if _, ok := statuses["rename old.txt"]; ok {
		t.Fatal("old rename path retained")
	}
	after, _ := os.ReadFile(filepath.Join(root, ".git", "index"))
	if string(before) != string(after) {
		t.Fatal("Git index mutated")
	}
	sub, err := Read(context.Background(), filepath.Join(root, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Files) != 3 {
		t.Fatalf("subtree statuses: %+v", sub.Files)
	}
	for _, file := range sub.Files {
		if file.Path != "changed.txt" && file.Path != "unchanged.txt" && file.Path != "ignored.log" {
			t.Fatalf("path outside project: %s", file.Path)
		}
	}
}
func TestParseNullDelimitedNamesAndConflicts(t *testing.T) {
	files, err := parse([]byte("old.txt\x00clean.txt\x00"), []byte("R  new name.txt\x00old.txt\x00?? line\nbreak.txt\x00UU conflict.txt\x00AM added.txt\x00?? nested-repo/\x00"), "")
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]File{}
	for _, file := range files {
		statuses[file.Path] = file
	}
	if !statuses["nested-repo"].IsDir {
		t.Fatal("nested repository directory lost")
	}
	if statuses["new name.txt"].PreviousPath != "old.txt" || statuses["line\nbreak.txt"].Status != "untracked" || statuses["conflict.txt"].Status != "conflict" || statuses["added.txt"].Status != "added" {
		t.Fatalf("parse: %+v", files)
	}
	if _, err := parse(nil, []byte("R  new\x00"), ""); err == nil {
		t.Fatal("incomplete rename accepted")
	}
}
func TestNoRepositoryAndCancelledContext(t *testing.T) {
	snapshot, err := Read(context.Background(), t.TempDir())
	if err != nil || snapshot.Available || snapshot.Message == "" {
		t.Fatal("missing repo not reported")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Read(ctx, t.TempDir()); err == nil {
		t.Fatal("cancel ignored")
	}
}
