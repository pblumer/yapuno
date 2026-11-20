// Package guardianlock stores emergency locks of guardian access. A guardian
// locks their own access when a device or mailbox may be in someone else's
// hands; only the operator lifts a lock, by removing its entry from the file.
package guardianlock

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid guardian lock request")
	ErrUnavailable = errors.New("guardian lock store unavailable")
)

// Entry is one locked guardian. It holds identifiers only.
type Entry struct {
	GuardianID string    `json:"guardian_id"`
	TenantID   string    `json:"tenant_id"`
	LockedAt   time.Time `json:"locked_at"`
}

type document struct {
	Locks []Entry `json:"locks"`
}

// Store re-reads its private file on every check, so an operator can lift a
// lock without a restart. A missing, malformed, symlinked or incorrectly
// permissioned file fails closed: every guardian counts as locked. Writes are
// serialized in this process; there is no cross-process locking.
type Store struct {
	mu        sync.Mutex
	path      string
	now       func() time.Time
	failWrite bool // test hook
}

// Open validates an existing file or creates an empty private one, so that a
// file that disappears later is noticed instead of unlocking everyone.
func Open(path string, now func() time.Time) (*Store, error) {
	if path == "" || now == nil {
		return nil, ErrInvalid
	}
	s := &Store{path: path, now: now}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if err := s.write(document{Locks: []Entry{}}); err != nil {
			return nil, ErrUnavailable
		}
	}
	if _, err := s.read(); err != nil {
		return nil, err
	}
	return s, nil
}

func validID(id string) bool {
	return id != "" && len(id) <= 128 && !strings.ContainsAny(id, "\x00\r\n")
}

func (s *Store) read() ([]Entry, error) {
	stat, err := os.Lstat(s.path)
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 {
		return nil, ErrUnavailable
	}
	f, err := os.Open(s.path)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Mode().Perm()&0077 != 0 || !os.SameFile(stat, opened) {
		return nil, ErrUnavailable
	}
	content, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(content) > 1<<20 {
		return nil, ErrUnavailable
	}
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.DisallowUnknownFields()
	var doc document
	if err := dec.Decode(&doc); err != nil {
		return nil, ErrUnavailable
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, ErrUnavailable
	}
	for _, e := range doc.Locks {
		if !validID(e.GuardianID) || !validID(e.TenantID) || e.LockedAt.IsZero() {
			return nil, ErrUnavailable
		}
	}
	return doc.Locks, nil
}

func (s *Store) write(doc document) error {
	if s.failWrite {
		return errors.New("write failed")
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	f, err := os.CreateTemp(dir, ".yapuno-guardian-locks-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	if dirHandle, err := os.Open(dir); err == nil {
		defer dirHandle.Close()
		if err := dirHandle.Sync(); err != nil {
			return err
		}
	}
	return nil
}

// Locked reports whether this guardian's access is locked. Any doubt (invalid
// identity, unreadable store) counts as locked.
func (s *Store) Locked(guardianID, tenantID string) bool {
	if !validID(guardianID) || !validID(tenantID) {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.read()
	if err != nil {
		return true
	}
	for _, e := range entries {
		if e.GuardianID == guardianID && e.TenantID == tenantID {
			return true
		}
	}
	return false
}

// Lock durably locks one guardian. It returns true only for the call that
// created the lock; an existing lock is left unchanged. Callers MUST take
// guardian and tenant from a verified session of that same guardian.
func (s *Store) Lock(guardianID, tenantID string) (bool, error) {
	if !validID(guardianID) || !validID(tenantID) {
		return false, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.read()
	if err != nil {
		return false, ErrUnavailable
	}
	for _, e := range entries {
		if e.GuardianID == guardianID && e.TenantID == tenantID {
			return false, nil
		}
	}
	next := append(append([]Entry{}, entries...), Entry{GuardianID: guardianID, TenantID: tenantID, LockedAt: s.now().UTC()})
	if err := s.write(document{Locks: next}); err != nil {
		return false, ErrUnavailable
	}
	return true, nil
}
