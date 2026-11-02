package gitstatus

import (
	"context"
	"testing"
)

func TestBranchesAndScopedBlob(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	git(t, root, "config", "user.email", "test@example.invalid")
	git(t, root, "config", "user.name", "Test")
	put(t, root, "sub/a space.txt", "committed\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "fixture")
	git(t, root, "branch", "target")
	git(t, root, "update-ref", "refs/remotes/origin/main", "HEAD")
	git(t, root, "update-ref", "refs/remotes/upstream/main", "HEAD")
	branches, err := Branches(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, b := range branches {
		found[b.Ref] = true
	}
	for _, ref := range []string{"refs/heads/target", "refs/remotes/origin/main", "refs/remotes/upstream/main"} {
		if !found[ref] {
			t.Fatalf("missing %s: %+v", ref, branches)
		}
	}
	put(t, root, "sub/a space.txt", "worktree\n")
	data, err := ReadBlob(context.Background(), root+"/sub", "refs/heads/target", "a space.txt")
	if err != nil || string(data) != "committed\n" {
		t.Fatalf("%q %v", data, err)
	}
	for _, ref := range []string{"HEAD", "--help", "refs/heads/absent"} {
		if _, err := ReadBlob(context.Background(), root, ref, "sub/a space.txt"); err == nil {
			t.Fatalf("accepted %s", ref)
		}
	}
	for _, name := range []string{"../sub/a space.txt", "/sub/a space.txt", "missing"} {
		if _, err := ReadBlob(context.Background(), root, "refs/heads/target", name); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
}
