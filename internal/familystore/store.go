// Package familystore provides durable, tenant-scoped family profile storage.
// It is a persistence component, not an authentication or authorization boundary.
package familystore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
)

var (
	ErrInvalid = errors.New("invalid family profile")
	ErrDuplicate = errors.New("duplicate family profile")
	ErrForbidden = errors.New("guardian is not authorized for this child")
)

type State struct {
	Children []familydashboard.Child `json:"children"`
	Grants []authz.GuardianGrant `json:"grants"`
}

// Store serializes updates and persists each successful update via atomic rename.
// A Store must have a single writer per file; cross-process locking is not provided.
type Store struct {
	mu sync.Mutex
	path string
	state State
}

func Open(path string) (*Store, error) {
	if path == "" { return nil, ErrInvalid }
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) { return s, nil }
	if err != nil { return nil, err }
	if err := json.Unmarshal(data, &s.state); err != nil { return nil, fmt.Errorf("decode family store: %w", err) }
	if err := validate(s.state); err != nil { return nil, err }
	return s, nil
}

func validate(s State) error {
	seen := make(map[string]bool)
	for _, c := range s.Children {
		if c.ID == "" || c.TenantID == "" || !validName(c.DisplayName) { return ErrInvalid }
		key := c.TenantID + "\x00" + c.ID
		if seen[key] { return ErrDuplicate }
		seen[key] = true
	}
	for _, g := range s.Grants {
		if g.GuardianID == "" || g.ChildID == "" || g.TenantID == "" || !seen[g.TenantID+"\x00"+g.ChildID] { return ErrInvalid }
	}
	return nil
}

func validName(name string) bool {
	name = strings.TrimSpace(name)
	return utf8.ValidString(name) && utf8.RuneCountInString(name) >= 1 && utf8.RuneCountInString(name) <= 40 && !strings.ContainsAny(name, "<>\r\n\x00")
}

// Cards returns a least-privilege projection, not raw tenant records.
func (s *Store) Cards(guardianID, tenantID string) []familydashboard.ChildCard {
	s.mu.Lock()
	defer s.mu.Unlock()
	return familydashboard.Build(guardianID, tenantID, s.state.Children, s.state.Grants)
}

// RenameChild requires an active guardian grant scoped to this child and tenant.
// Authentication of guardianID MUST happen before calling this method.
func (s *Store) RenameChild(guardianID, tenantID, childID, name string) error {
	if guardianID == "" || tenantID == "" || childID == "" || !validName(name) { return ErrInvalid }
	s.mu.Lock()
	defer s.mu.Unlock()
	authorized := false
	for _, card := range familydashboard.Build(guardianID, tenantID, s.state.Children, s.state.Grants) {
		if card.ID == childID && card.Permissions.ManageDevices && card.Permissions.ApproveContacts { authorized = true }
	}
	if !authorized { return ErrForbidden }
	next := clone(s.state)
	for i := range next.Children {
		if next.Children[i].ID == childID && next.Children[i].TenantID == tenantID { next.Children[i].DisplayName = strings.TrimSpace(name) }
	}
	return s.commit(next)
}

// Bootstrap is an explicit provisioning operation, not a user-facing endpoint.
// Callers must authorize tenant creation externally; never expose it to an untrusted client.
func (s *Store) Bootstrap(child familydashboard.Child, grant authz.GuardianGrant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if child.ID == "" || child.TenantID == "" || !validName(child.DisplayName) ||
		grant.ChildID != child.ID || grant.TenantID != child.TenantID || grant.GuardianID == "" || !grant.Active { return ErrInvalid }
	next := clone(s.state)
	next.Children = append(next.Children, child)
	next.Grants = append(next.Grants, grant)
	if err := validate(next); err != nil { return err }
	return s.commit(next)
}

func clone(s State) State {
	return State{Children: append([]familydashboard.Child(nil), s.Children...), Grants: append([]authz.GuardianGrant(nil), s.Grants...)}
}

func (s *Store) commit(next State) error {
	if err := validate(next); err != nil { return err }
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil { return err }
	dir := filepath.Dir(s.path)
	f, err := os.CreateTemp(dir, ".yapuno-family-*")
	if err != nil { return err }
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil { f.Close(); return err }
	if _, err := f.Write(data); err != nil { f.Close(); return err }
	if err := f.Sync(); err != nil { f.Close(); return err }
	if err := f.Close(); err != nil { return err }
	if err := os.Rename(f.Name(), s.path); err != nil { return err }
	d, err := os.Open(dir)
	if err == nil { defer d.Close(); if err := d.Sync(); err != nil { return err } }
	s.state = next
	return nil
}
