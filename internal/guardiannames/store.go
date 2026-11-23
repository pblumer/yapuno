// Package guardiannames keeps the names a guardian chooses to be shown by.
//
// A guardian has two, both optional: one for their own children ("Mami") and
// one for the guardians of families whose children are connected to theirs
// ("Anna"). Each is shown only to the people it is meant for. The names are
// chosen by the guardian and checked by nobody: they say what somebody wants
// to be called, not who somebody is.
package guardiannames

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("invalid guardian name")
	ErrUnavailable = errors.New("guardian names unavailable")
)

const (
	// MaxLength is the longest name, in characters.
	MaxLength = 40

	maxEntries = 20000
	maxFile    = 4 << 20
)

// Names are the two names of one guardian. An empty name means none.
type Names struct {
	// ForChildren is what the guardian's own children see.
	ForChildren string
	// ForAdults is what guardians of connected families see.
	ForAdults string
}

type entry struct {
	TenantID    string `json:"tenant_id"`
	GuardianID  string `json:"guardian_id"`
	ForChildren string `json:"for_children"`
	ForAdults   string `json:"for_adults"`
}

type document struct {
	Guardians []entry `json:"guardians"`
}

type key struct{ tenant, guardian string }

// Store keeps the names in a private file and persists every change with an
// atomic rename before acknowledging it. It must have a single writer.
type Store struct {
	mu        sync.Mutex
	path      string
	names     map[key]Names
	failWrite bool // test hook
}

func validID(id string) bool {
	return id != "" && len(id) <= 128 && !strings.ContainsAny(id, "\x00\r\n")
}

// clean trims a name and reports whether it can be shown: at most MaxLength
// characters, no markup brackets, and no control or formatting characters,
// which could hide or reorder what is displayed.
func clean(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", true
	}
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxLength || strings.ContainsAny(name, "<>") {
		return "", false
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", false
		}
	}
	return name, true
}

// Valid reports whether a name would be accepted. The empty name is valid.
func Valid(name string) bool {
	_, ok := clean(name)
	return ok
}

// Open loads an existing private file or creates an empty one. A malformed,
// symlinked or group/world-accessible file is refused.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, ErrInvalid
	}
	s := &Store{path: path, names: map[key]Names{}}
	stat, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := s.write(s.names); err != nil {
			return nil, ErrUnavailable
		}
		return s, nil
	}
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 || stat.Size() > maxFile {
		return nil, ErrUnavailable
	}
	content, err := os.ReadFile(path)
	if err != nil {
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
	for _, e := range doc.Guardians {
		k := key{e.TenantID, e.GuardianID}
		forChildren, okChildren := clean(e.ForChildren)
		forAdults, okAdults := clean(e.ForAdults)
		_, duplicate := s.names[k]
		if !validID(e.TenantID) || !validID(e.GuardianID) || !okChildren || !okAdults || (forChildren == "" && forAdults == "") || duplicate {
			return nil, ErrUnavailable
		}
		s.names[k] = Names{ForChildren: forChildren, ForAdults: forAdults}
	}
	return s, nil
}

func (s *Store) write(names map[key]Names) error {
	if s.failWrite {
		return errors.New("write failed")
	}
	doc := document{Guardians: make([]entry, 0, len(names))}
	for k, n := range names {
		doc.Guardians = append(doc.Guardians, entry{TenantID: k.tenant, GuardianID: k.guardian, ForChildren: n.ForChildren, ForAdults: n.ForAdults})
	}
	sort.Slice(doc.Guardians, func(i, j int) bool {
		a, b := doc.Guardians[i], doc.Guardians[j]
		return a.TenantID+"\x00"+a.GuardianID < b.TenantID+"\x00"+b.GuardianID
	})
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	f, err := os.CreateTemp(dir, ".yapuno-guardian-names-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
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
	if handle, err := os.Open(dir); err == nil {
		defer handle.Close()
		return handle.Sync()
	}
	return nil
}

// Get returns the names of one guardian; both are empty if none was set.
// It is not an authorization check: the caller decides who may see which.
func (s *Store) Get(tenantID, guardianID string) Names {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.names[key{tenantID, guardianID}]
}

// Set replaces both names of one guardian. Empty names remove them. The
// caller MUST take tenant and guardian from a verified session of that same
// guardian: nobody names somebody else.
func (s *Store) Set(tenantID, guardianID string, names Names) error {
	forChildren, okChildren := clean(names.ForChildren)
	forAdults, okAdults := clean(names.ForAdults)
	if !validID(tenantID) || !validID(guardianID) || !okChildren || !okAdults {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[key]Names, len(s.names)+1)
	for k, n := range s.names {
		next[k] = n
	}
	k := key{tenantID, guardianID}
	if forChildren == "" && forAdults == "" {
		delete(next, k)
	} else {
		next[k] = Names{ForChildren: forChildren, ForAdults: forAdults}
	}
	if len(next) > maxEntries {
		return ErrUnavailable
	}
	if err := s.write(next); err != nil {
		return ErrUnavailable
	}
	s.names = next
	return nil
}

// PurgeTenant removes the names of every guardian of one family. It is a
// trusted call for the deletion of that family.
func (s *Store) PurgeTenant(tenantID string) error {
	if !validID(tenantID) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[key]Names, len(s.names))
	for k, n := range s.names {
		if k.tenant != tenantID {
			next[k] = n
		}
	}
	if len(next) == len(s.names) {
		return nil
	}
	if err := s.write(next); err != nil {
		return ErrUnavailable
	}
	s.names = next
	return nil
}
