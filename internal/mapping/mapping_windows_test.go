//go:build windows

package mapping

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRejectsJunctionComponents(t *testing.T) {
	m, root := newTestMapper(t)
	outside := t.TempDir()
	junction := filepath.Join(root, "junction")
	if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, outside).CombinedOutput(); err != nil {
		t.Skipf("junction creation unavailable: %v (%s)", err, output)
	}
	if _, err := m.LocalToRemote(filepath.Join("junction", "file.txt")); err == nil {
		t.Fatal("LocalToRemote followed an intermediate junction")
	}
	if _, err := m.RemoteToLocal("/srv/projects/demo/junction/file.txt"); err == nil {
		t.Fatal("RemoteToLocal accepted an existing junction component")
	}
}
