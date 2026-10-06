package mapping

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestMapper(t *testing.T) (*Mapper, string) {
	t.Helper()
	root := t.TempDir()
	m, err := New(root, "/srv/projects/demo")
	if err != nil {
		t.Fatal(err)
	}
	return m, root
}

func TestNewRequiresAbsoluteLocalRootAndValidPOSIXRemoteRoot(t *testing.T) {
	if _, err := New("relative-project", "/srv/project"); err == nil {
		t.Fatal("New accepted a relative local root")
	}
	root := t.TempDir()
	for _, remote := range []string{`/srv\project`, "/srv/proj\u0001ect", "/srv/proj\u007fect"} {
		t.Run(remote, func(t *testing.T) {
			if _, err := New(root, remote); err == nil {
				t.Fatalf("New accepted invalid remote root %q", remote)
			}
		})
	}
}

func TestRoundTripUnicodePaths(t *testing.T) {
	m, root := newTestMapper(t)
	local := filepath.Join("данные", "résumé.txt")
	wantRemote := "/srv/projects/demo/данные/résumé.txt"
	gotRemote, err := m.LocalToRemote(local)
	if err != nil {
		t.Fatal(err)
	}
	if gotRemote != wantRemote {
		t.Fatalf("LocalToRemote() = %q, want %q", gotRemote, wantRemote)
	}
	gotLocal, err := m.RemoteToLocal(wantRemote)
	if err != nil {
		t.Fatal(err)
	}
	wantLocal := filepath.Join(root, local)
	if gotLocal != wantLocal {
		t.Fatalf("RemoteToLocal() = %q, want %q", gotLocal, wantLocal)
	}
	if _, err := os.Stat(gotLocal); !os.IsNotExist(err) {
		t.Fatalf("expected download target to be allowed not to exist; stat error = %v", err)
	}
}

func TestRemoteRelativeToAbsoluteAllowsRemoteOnlyNames(t *testing.T) {
	m, _ := newTestMapper(t)
	got, err := m.RemoteRelativeToAbsolute("CON/na:me.txt")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/srv/projects/demo/CON/na:me.txt"; got != want {
		t.Fatalf("RemoteRelativeToAbsolute() = %q, want %q", got, want)
	}
	if got, err := m.RemoteRelativeToAbsolute("."); err != nil || got != "/srv/projects/demo" {
		t.Fatalf("RemoteRelativeToAbsolute(.) = %q, %v; want remote root", got, err)
	}
}

func TestRemoteRelativeToAbsoluteIgnoresSameNamedLocalSymlink(t *testing.T) {
	m, root := newTestMapper(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "remote-dir")); err != nil {
		t.Skipf("symlink creation unavailable on this Windows account: %v", err)
	}
	got, err := m.RemoteRelativeToAbsolute("remote-dir/child")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/srv/projects/demo/remote-dir/child"; got != want {
		t.Fatalf("RemoteRelativeToAbsolute() = %q, want %q", got, want)
	}
}

func TestRemoteRelativeToAbsoluteRejectsUnsafePaths(t *testing.T) {
	m, _ := newTestMapper(t)
	for _, input := range []string{"", "/absolute", "../outside", "nested/../../outside", `nested\child`, "control\x01name"} {
		t.Run(input, func(t *testing.T) {
			if _, err := m.RemoteRelativeToAbsolute(input); err == nil {
				t.Fatalf("RemoteRelativeToAbsolute(%q) unexpectedly succeeded", input)
			}
		})
	}
}

func TestLocalToRemoteRejectsTraversalAndAbsolutePaths(t *testing.T) {
	m, _ := newTestMapper(t)
	for _, input := range []string{"../secret", "nested/../../secret", filepath.VolumeName(t.TempDir()) + string(filepath.Separator) + "outside"} {
		t.Run(input, func(t *testing.T) {
			if _, err := m.LocalToRemote(input); err == nil {
				t.Fatalf("LocalToRemote(%q) unexpectedly succeeded", input)
			}
		})
	}
}

func TestRemoteToLocalRejectsTraversalAndRootPrefixCollision(t *testing.T) {
	m, _ := newTestMapper(t)
	for _, input := range []string{"/srv/projects/../secret", "/srv/projects/demo-evil/file", "/srv/projects/demo2/file"} {
		t.Run(input, func(t *testing.T) {
			if _, err := m.RemoteToLocal(input); err == nil {
				t.Fatalf("RemoteToLocal(%q) unexpectedly succeeded", input)
			}
		})
	}
}

func TestRemoteToLocalRejectsWindowsInvalidNames(t *testing.T) {
	m, _ := newTestMapper(t)
	for _, name := range []string{"bad:name", "trailing.", "trailing ", "CON", "con.txt", "NUL.log", "COM1", "LPT9.txt", "a\\b", "bad?name", "bad*name", "<x>", "pipe|name", "quote\"name"} {
		t.Run(strings.ReplaceAll(name, "\\", "_"), func(t *testing.T) {
			if _, err := m.RemoteToLocal("/srv/projects/demo/" + name); err == nil {
				t.Fatalf("RemoteToLocal accepted invalid Windows name %q", name)
			}
		})
	}
}

func TestRemoteToLocalAcceptsOrdinaryNamesWithExtensions(t *testing.T) {
	m, root := newTestMapper(t)
	got, err := m.RemoteToLocal("/srv/projects/demo/CONSOLE.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "CONSOLE.txt")
	if got != want {
		t.Fatalf("RemoteToLocal() = %q, want %q", got, want)
	}
}

func TestRemoteToLocalRejectsExistingCaseCollision(t *testing.T) {
	m, root := newTestMapper(t)
	if err := os.Mkdir(filepath.Join(root, "Foo"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RemoteToLocal("/srv/projects/demo/foo/file.txt"); err == nil {
		t.Fatal("RemoteToLocal accepted a target colliding with an existing name by case")
	}
}

func TestRejectsExistingSymlinkComponents(t *testing.T) {
	m, root := newTestMapper(t)
	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation unavailable on this Windows account: %v", err)
	}
	if _, err := m.LocalToRemote(filepath.Join("link", "file.txt")); err == nil {
		t.Fatal("LocalToRemote followed an intermediate symlink")
	}
	if _, err := m.RemoteToLocal("/srv/projects/demo/link/file.txt"); err == nil {
		t.Fatal("RemoteToLocal accepted an existing symlink component")
	}
}

func TestRejectsSymlinkProjectRoot(t *testing.T) {
	actual := t.TempDir()
	parent := t.TempDir()
	link := filepath.Join(parent, "project-link")
	if err := os.Symlink(actual, link); err != nil {
		t.Skipf("symlink creation unavailable on this Windows account: %v", err)
	}
	if _, err := New(link, "/srv/projects/demo"); err == nil {
		t.Fatal("New accepted a symlink project root")
	}
}

func TestRemoteToLocalRejectsColonEvenOnNonWindowsHost(t *testing.T) {
	m, _ := newTestMapper(t)
	if _, err := m.RemoteToLocal("/srv/projects/demo/name:stream"); err == nil {
		t.Fatal("RemoteToLocal accepted a colon in a Windows target name")
	}
}
