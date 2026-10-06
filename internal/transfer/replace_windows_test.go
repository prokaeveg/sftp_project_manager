//go:build windows

package transfer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceLocalDoesNotReplaceNewDestination(t *testing.T) {
	dir := t.TempDir()
	temp := filepath.Join(dir, "temporary")
	destination := filepath.Join(dir, "destination")
	if err := os.WriteFile(temp, []byte("temporary contents"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("original contents"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := replaceLocal(temp, destination, false); err == nil {
		t.Fatal("replaceLocal() replaced a destination that appeared after the initial check")
	}
	actual, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != "original contents" {
		t.Fatalf("destination contents = %q, want original contents", actual)
	}
	if _, err := os.Stat(temp); err != nil {
		t.Fatalf("temporary file was unexpectedly consumed: %v", err)
	}
}
