package guardianlock

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// A lock of a guardian whose family is deleted has nobody left to lock. It
// must not wait for a later guardian or family with the same identifiers.
func TestPurgeTenantRemovesTheLocksOfThatFamilyOnly(t *testing.T) {
	s, path := open(t)
	for _, who := range [][2]string{{"anna", "family-a"}, {"albert", "family-a"}, {"bruno", "family-b"}, {"anna", "family-z"}} {
		if _, err := s.Lock(who[0], who[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PurgeTenant("family-a"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	for name, store := range map[string]*Store{"running": s, "reopened": reopened} {
		if store.Locked("anna", "family-a") || store.Locked("albert", "family-a") {
			t.Errorf("%s: a lock of the purged family is left", name)
		}
		if !store.Locked("bruno", "family-b") || !store.Locked("anna", "family-z") {
			t.Errorf("%s: a lock of another family was lifted", name)
		}
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "family-a") {
		t.Fatalf("file: %s", raw)
	}
	if err := s.PurgeTenant("family-a"); err != nil {
		t.Fatalf("second purge: %v", err)
	}
	if err := s.PurgeTenant("unknown"); err != nil {
		t.Fatalf("purging nothing: %v", err)
	}
}

// A lock is a protection: it only goes when the removal is on disk.
func TestAFailedPurgeLiftsNoLock(t *testing.T) {
	s, _ := open(t)
	if _, err := s.Lock("anna", "family-a"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "a\nb"} {
		if err := s.PurgeTenant(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	s.failWrite = true
	if err := s.PurgeTenant("family-a"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed write: %v", err)
	}
	s.failWrite = false
	if !s.Locked("anna", "family-a") {
		t.Fatal("a lock was lifted without a durable write")
	}
}
