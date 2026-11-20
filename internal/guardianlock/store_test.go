package guardianlock

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixedClock() func() time.Time {
	now := time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC)
	return func() time.Time { return now }
}

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "locks.json")
	s, err := Open(path, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestLockIsScopedDurableAndIdempotent(t *testing.T) {
	s, path := open(t)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("store file must exist privately after Open: %v %v", info, err)
	}
	if s.Locked("anna", "family-1") {
		t.Fatal("locked before any lock")
	}
	created, err := s.Lock("anna", "family-1")
	if err != nil || !created {
		t.Fatalf("lock: %v %v", created, err)
	}
	if !s.Locked("anna", "family-1") {
		t.Fatal("not locked after Lock")
	}
	// Same guardian id in another family, and the partner, stay free.
	if s.Locked("anna", "family-2") || s.Locked("ben", "family-1") {
		t.Fatal("lock leaked to another guardian")
	}
	again, err := s.Lock("anna", "family-1")
	if err != nil || again {
		t.Fatalf("second lock must be a no-op: %v %v", again, err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Count(string(raw), `"anna"`) != 1 || !strings.Contains(string(raw), "2026-11-20T18:00:00Z") {
		t.Fatalf("stored document: %s", raw)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatalf("permissions after write: %v", info.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".yapuno-*")); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
	// A restart keeps the lock.
	reopened, err := Open(path, fixedClock())
	if err != nil || !reopened.Locked("anna", "family-1") || reopened.Locked("ben", "family-1") {
		t.Fatalf("lock lost on restart: %v", err)
	}
}

func TestOperatorLiftsALockByRemovingTheEntry(t *testing.T) {
	s, path := open(t)
	for _, g := range []string{"anna", "ben"} {
		if _, err := s.Lock(g, "family-1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte(`{"locks":[{"guardian_id":"ben","tenant_id":"family-1","locked_at":"2026-11-20T18:00:00Z"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s.Locked("anna", "family-1") || !s.Locked("ben", "family-1") {
		t.Fatal("operator edit not honoured without restart")
	}
	// Locking again after an operator release works and keeps other entries.
	if created, err := s.Lock("anna", "family-1"); err != nil || !created || !s.Locked("ben", "family-1") {
		t.Fatalf("relock: %v %v", created, err)
	}
}

func TestUnreadableStoreLocksEveryoneOut(t *testing.T) {
	cases := map[string]func(path string) error{
		"missing":       func(p string) error { return os.Remove(p) },
		"malformed":     func(p string) error { return os.WriteFile(p, []byte(`{"locks":[`), 0600) },
		"unknown field": func(p string) error { return os.WriteFile(p, []byte(`{"locks":[],"unlock_all":true}`), 0600) },
		"trailing data": func(p string) error { return os.WriteFile(p, []byte(`{"locks":[]}{"locks":[]}`), 0600) },
		"entry without id": func(p string) error {
			return os.WriteFile(p, []byte(`{"locks":[{"guardian_id":"","tenant_id":"family-1","locked_at":"2026-11-20T18:00:00Z"}]}`), 0600)
		},
		"entry without time": func(p string) error {
			return os.WriteFile(p, []byte(`{"locks":[{"guardian_id":"ben","tenant_id":"family-1"}]}`), 0600)
		},
		"group readable": func(p string) error { return os.Chmod(p, 0640) },
		"symlink": func(p string) error {
			target := p + ".real"
			if err := os.Rename(p, target); err != nil {
				return err
			}
			return os.Symlink(target, p)
		},
		"directory": func(p string) error {
			if err := os.Remove(p); err != nil {
				return err
			}
			return os.Mkdir(p, 0700)
		},
		"too large": func(p string) error {
			return os.WriteFile(p, []byte(`{"locks":[]}`+strings.Repeat(" ", 1<<20)), 0600)
		},
	}
	for name, breakIt := range cases {
		s, path := open(t)
		if err := breakIt(path); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !s.Locked("anna", "family-1") {
			t.Errorf("%s: unreadable store must deny, not allow", name)
		}
		if _, err := s.Lock("anna", "family-1"); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: lock on a broken store: %v", name, err)
		}
		if name != "missing" {
			if _, err := Open(path, fixedClock()); err == nil {
				t.Errorf("%s: Open accepted a broken store", name)
			}
		}
	}
}

func TestInvalidIdentitiesAreDeniedAndNeverStored(t *testing.T) {
	s, path := open(t)
	before, _ := os.ReadFile(path)
	for _, id := range [][2]string{{"", "family-1"}, {"anna", ""}, {"an\nna", "family-1"}, {"anna", "fam\x00ily"}, {strings.Repeat("a", 129), "family-1"}} {
		if !s.Locked(id[0], id[1]) {
			t.Errorf("%q: invalid identity must count as locked", id)
		}
		if _, err := s.Lock(id[0], id[1]); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", id, err)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("an invalid request changed the store")
	}
	for _, bad := range []func() (*Store, error){
		func() (*Store, error) { return Open("", fixedClock()) },
		func() (*Store, error) { return Open(path, nil) },
	} {
		if _, err := bad(); err == nil {
			t.Fatal("Open accepted invalid arguments")
		}
	}
}

func TestFailedWriteDoesNotClaimALock(t *testing.T) {
	s, path := open(t)
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)
	if os.Geteuid() == 0 {
		// root ignores directory permissions; use the hook instead.
		s.failWrite = true
	}
	if created, err := s.Lock("anna", "family-1"); err == nil || created {
		t.Fatalf("failed write reported as locked: %v %v", created, err)
	}
	_ = os.Chmod(dir, 0700)
	s.failWrite = false
	if s.Locked("anna", "family-1") {
		t.Fatal("lock visible although the write failed")
	}
}

func TestConcurrentLocksKeepEveryEntry(t *testing.T) {
	s, _ := open(t)
	var wg sync.WaitGroup
	created := make(chan bool, 40)
	for i := 0; i < 20; i++ {
		for _, g := range []string{"anna", "ben"} {
			wg.Add(1)
			go func(g string) {
				defer wg.Done()
				ok, err := s.Lock(g, "family-1")
				if err != nil {
					t.Error(err)
				}
				created <- ok
			}(g)
		}
	}
	wg.Wait()
	close(created)
	n := 0
	for ok := range created {
		if ok {
			n++
		}
	}
	if n != 2 || !s.Locked("anna", "family-1") || !s.Locked("ben", "family-1") {
		t.Fatalf("exactly one caller per guardian must create the lock, got %d", n)
	}
}
