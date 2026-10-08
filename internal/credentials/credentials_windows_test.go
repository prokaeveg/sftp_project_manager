//go:build windows

package credentials

import (
	"crypto/rand"
	"encoding/hex"
	"testing"
)

func uniqueHex(t *testing.T) string {
	t.Helper()
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal("generate isolated credential test namespace")
	}
	return hex.EncodeToString(random[:])
}

func TestWindowsCredentialStore(t *testing.T) {
	store := New()
	prefix := "a" + uniqueHex(t)
	otherPrefix := "b" + uniqueHex(t)
	t.Cleanup(func() {
		if err := store.DeletePrefix(prefix); err != nil {
			t.Errorf("clean up test credentials: %v", err)
		}
		if err := store.DeletePrefix(otherPrefix); err != nil {
			t.Errorf("clean up isolated test credential: %v", err)
		}
	})

	if value, found, err := store.Read(prefix + "/1"); err != nil || found || value != "" {
		t.Fatalf("Read(missing) = (%q, %v, %v), want empty, false, nil", value, found, err)
	}

	want := "пароль🔐"
	if err := store.Write(prefix+"/1", want); err != nil {
		t.Fatalf("Write(Unicode): %v", err)
	}
	if got, found, err := store.Read(prefix + "/1"); err != nil || !found || got != want {
		t.Fatalf("Read(Unicode) = (%q, %v, %v), want (%q, true, nil)", got, found, err, want)
	}

	if err := store.Write(prefix+"/1", "updated"); err != nil {
		t.Fatalf("overwrite credential: %v", err)
	}
	if got, found, err := store.Read(prefix + "/1"); err != nil || !found || got != "updated" {
		t.Fatalf("Read(overwritten) = (%q, %v, %v), want updated, true, nil", got, found, err)
	}
	if err := store.Write(prefix+"/2", "second"); err != nil {
		t.Fatalf("Write(second): %v", err)
	}
	if err := store.Write(otherPrefix+"/1", "keep"); err != nil {
		t.Fatalf("Write(isolated): %v", err)
	}
	if err := store.DeletePrefix(prefix); err != nil {
		t.Fatalf("DeletePrefix: %v", err)
	}
	for _, target := range []string{prefix + "/1", prefix + "/2"} {
		if got, found, err := store.Read(target); err != nil || found {
			t.Fatalf("Read(%q after delete) = (%q, %v, %v), want empty, false, nil", target, got, found, err)
		}
	}
	if got, found, err := store.Read(otherPrefix + "/1"); err != nil || !found || got != "keep" {
		t.Fatalf("Read(isolated after delete) = (%q, %v, %v), want keep, true, nil", got, found, err)
	}
}

func TestWindowsCredentialStoreAllowsEmptyValueWhenSupported(t *testing.T) {
	store := New()
	prefix := "c" + uniqueHex(t)
	t.Cleanup(func() {
		if err := store.DeletePrefix(prefix); err != nil {
			t.Errorf("clean up empty-value test credential: %v", err)
		}
	})
	if err := store.Write(prefix+"/1", ""); err != nil {
		t.Skipf("Credential Manager does not accept an empty generic credential blob: %v", err)
	}
	if got, found, err := store.Read(prefix + "/1"); err != nil || !found || got != "" {
		t.Fatalf("Read(empty) = (%q, %v, %v), want empty, true, nil", got, found, err)
	}
}
