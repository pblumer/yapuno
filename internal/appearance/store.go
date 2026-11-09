package appearance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type record struct {
	Subject
	Skin   string `json:"skin"`
	Figure Figure `json:"figure"`
}

type state struct {
	Preferences []record `json:"preferences"`
}

// Store persists preferences in a private JSON file using atomic replacement.
// A Store must have a single writer per file; cross-process locking is not provided.
type Store struct {
	mu    sync.Mutex
	path  string
	prefs map[Subject]Preference
}

// Open loads an existing store or prepares a new one. It fails closed on
// corrupt content, unknown fields, invalid subjects or figures, duplicates and
// files readable by group or others. Unknown skin ids are tolerated on load so
// a retired skin cannot prevent startup; Get falls back for them.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, ErrInvalid
	}
	s := &Store{path: path, prefs: map[Subject]Preference{}}
	stat, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("%w: store file must be a private regular file", ErrInvalid)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st state
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&st); err != nil {
		return nil, fmt.Errorf("decode appearance store: %w", err)
	}
	for _, r := range st.Preferences {
		if err := validateSubject(r.Subject); err != nil {
			return nil, err
		}
		if err := validateFigure(r.Figure); err != nil {
			return nil, err
		}
		if _, dup := s.prefs[r.Subject]; dup {
			return nil, fmt.Errorf("%w: duplicate subject", ErrInvalid)
		}
		s.prefs[r.Subject] = Preference{Skin: r.Skin, Figure: r.Figure}
	}
	return s, nil
}

// Get returns the stored preference or Default. A stored skin that is no
// longer valid is replaced by the default skin; the figure is kept.
func (s *Store) Get(subject Subject) Preference {
	s.mu.Lock()
	p, ok := s.prefs[subject]
	s.mu.Unlock()
	if !ok {
		return Default()
	}
	if Validate(p) != nil {
		p.Skin = Default().Skin
	}
	return p
}

// Set validates and durably stores a preference before returning.
func (s *Store) Set(subject Subject, p Preference) error {
	if err := validateSubject(subject); err != nil {
		return err
	}
	if err := Validate(p); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[Subject]Preference, len(s.prefs)+1)
	for k, v := range s.prefs {
		next[k] = v
	}
	next[subject] = p
	if err := s.commit(next); err != nil {
		return err
	}
	s.prefs = next
	return nil
}

func (s *Store) commit(prefs map[Subject]Preference) error {
	st := state{Preferences: make([]record, 0, len(prefs))}
	for k, v := range prefs {
		st.Preferences = append(st.Preferences, record{Subject: k, Skin: v.Skin, Figure: v.Figure})
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	f, err := os.CreateTemp(dir, ".yapuno-appearance-*")
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
	if d, err := os.Open(dir); err == nil {
		defer d.Close()
		return d.Sync()
	}
	return nil
}
