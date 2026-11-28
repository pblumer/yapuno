package play

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/pblumer/yapuno/internal/familydashboard"
)

// GameChess is the only game so far.
const GameChess = "chess"

var knownGames = map[string]bool{GameChess: true}

var (
	ErrForbidden   = errors.New("not allowed to change games for this child")
	ErrUnknownGame = errors.New("unknown game")
)

// Families resolves the children a guardian currently answers for.
type Families interface {
	Cards(guardianID, tenantID string) []familydashboard.ChildCard
}

type enabled struct {
	TenantID string `json:"tenant_id"`
	ChildID  string `json:"child_id"`
	Game     string `json:"game"`
}

type settingsFile struct {
	Enabled []enabled `json:"enabled"`
}

// Settings records which games the guardians of a child allow for that
// child. Every game is off until a guardian switches it on. It is
// single-writer per file.
type Settings struct {
	mu       sync.Mutex
	path     string
	families Families
	on       map[enabled]bool
}

const maxSettingsFile = 4 << 20

// OpenSettings loads the file or prepares a new one. It fails closed on
// insecure permissions, unknown fields, unknown games and duplicates.
func OpenSettings(path string, families Families) (*Settings, error) {
	if path == "" || families == nil {
		return nil, ErrInvalidPolicy
	}
	s := &Settings{path: path, families: families, on: map[enabled]bool{}}
	stat, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 || stat.Size() > maxSettingsFile {
		return nil, fmt.Errorf("%w: settings must be a private regular file", ErrInvalidPolicy)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f settingsFile
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%w: decode play settings", ErrInvalidPolicy)
	}
	for _, e := range f.Enabled {
		if e.TenantID == "" || e.ChildID == "" || !knownGames[e.Game] || s.on[e] {
			return nil, fmt.Errorf("%w: play settings record", ErrInvalidPolicy)
		}
		s.on[e] = true
	}
	return s, nil
}

// Enabled reports whether the game is currently allowed for the child.
func (s *Settings) Enabled(tenantID, childID, game string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.on[enabled{tenantID, childID, game}]
}

// SetEnabled switches a game on or off for one child. The guardian must
// currently be allowed to approve contacts for that child, because a game
// is a form of contact.
func (s *Settings) SetEnabled(guardianID, tenantID, childID, game string, on bool) error {
	if !knownGames[game] {
		return ErrUnknownGame
	}
	if !s.mayChange(guardianID, tenantID, childID) {
		return ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := enabled{tenantID, childID, game}
	if s.on[key] == on {
		return nil
	}
	next := make(map[enabled]bool, len(s.on)+1)
	for k := range s.on {
		next[k] = true
	}
	if on {
		next[key] = true
	} else {
		delete(next, key)
	}
	return s.commit(next)
}

func (s *Settings) mayChange(guardianID, tenantID, childID string) bool {
	if guardianID == "" || tenantID == "" || childID == "" {
		return false
	}
	for _, c := range s.families.Cards(guardianID, tenantID) {
		if c.ID == childID && c.Permissions.ApproveContacts {
			return true
		}
	}
	return false
}

// PurgeChild removes every setting of one child. It is a trusted call for
// the deletion of a child profile.
func (s *Settings) PurgeChild(tenantID, childID string) error {
	if tenantID == "" || childID == "" {
		return ErrInvalidPolicy
	}
	return s.purge(func(e enabled) bool { return e.TenantID == tenantID && e.ChildID == childID })
}

// PurgeTenant removes the settings of every child of one family.
func (s *Settings) PurgeTenant(tenantID string) error {
	if tenantID == "" {
		return ErrInvalidPolicy
	}
	return s.purge(func(e enabled) bool { return e.TenantID == tenantID })
}

func (s *Settings) purge(match func(enabled) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := map[enabled]bool{}
	for k := range s.on {
		if !match(k) {
			next[k] = true
		}
	}
	if len(next) == len(s.on) {
		return nil
	}
	return s.commit(next)
}

// commit replaces the file atomically and only then the in-memory state.
func (s *Settings) commit(next map[enabled]bool) error {
	f := settingsFile{Enabled: make([]enabled, 0, len(next))}
	for k := range next {
		f.Enabled = append(f.Enabled, k)
	}
	sort.Slice(f.Enabled, func(i, j int) bool {
		a, b := f.Enabled[i], f.Enabled[j]
		return a.TenantID+"\x00"+a.ChildID+"\x00"+a.Game < b.TenantID+"\x00"+b.ChildID+"\x00"+b.Game
	})
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".yapuno-play-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	s.on = next
	return nil
}
