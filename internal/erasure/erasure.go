// Package erasure deletes a child profile or a whole family together with
// everything the other stores hold about it.
//
// There is no transaction across the stores, so the order carries the
// guarantees:
//
//  1. The family store is asked whether the guardian may delete at all.
//     A refusal touches nothing.
//  2. What the child acts with is removed first: devices, contacts, reports,
//     looks. If one of these fails the profile stays, so the guardian can
//     simply try again.
//  3. The profile, or the family, is removed. From here on nobody can reach
//     what may still be left.
//  4. For a family: the guardians' identities are removed, the guardians are
//     signed out, and then the records that restrict or describe guardians
//     (invitations, locks). A lock stays if the identities could not be
//     removed.
//  5. The message logs drop the conversations whose relationship is gone.
//
// A failure after step 3 is reported as ErrLeftovers; the remaining steps
// still run. See docs/adr/draft-self-service-deletion.md.
package erasure

import (
	"errors"
	"log"
	"sync"

	"github.com/pblumer/yapuno/internal/familystore"
)

var (
	// ErrIncomplete means the deletion stopped before the profile or family
	// was removed. Parts that depend on it may be gone already; trying again
	// is safe.
	ErrIncomplete = errors.New("deletion could not be completed; the profile still exists")
	// ErrLeftovers means the profile or family is deleted, but something
	// about it could not be removed and needs the operator.
	ErrLeftovers = errors.New("deleted, but some records could not be removed")
)

// Families is the authoritative store of profiles and grants. Its refusals
// (familystore.ErrForbidden, ErrLastChild, ErrLastFamily) are passed on.
type Families interface {
	MayRemoveChild(guardianID, tenantID, childID string) error
	RemoveChild(guardianID, tenantID, childID string) error
	MayRemoveFamily(guardianID, tenantID string) error
	// RemoveFamily returns every guardian who held a grant in the family.
	RemoveFamily(guardianID, tenantID string) ([]string, error)
}

// Store holds records about children. Purging what is not there must not be
// an error.
type Store interface {
	PurgeChild(tenantID, childID string) error
	PurgeTenant(tenantID string) error
}

// TenantStore holds records about the guardians of a family.
type TenantStore interface {
	PurgeTenant(tenantID string) error
}

// Identities is the guardian directory.
type Identities interface {
	RemoveTenant(tenantID string) (int, error)
}

// Compactor is a message log that drops conversations whose relationship no
// longer exists.
type Compactor interface {
	Compact() error
}

// The names are for the log only; a failure is logged with the name of the
// part, never with an identifier, a name or an address.
type (
	Dependent struct {
		Name  string
		Store Store
	}
	TenantDependent struct {
		Name  string
		Store TenantStore
	}
	Log struct {
		Name string
		Log  Compactor
	}
)

type Config struct {
	Families   Families
	Identities Identities
	// SignOut ends what a guardian is signed in with: sessions and unused
	// sign-in links. Each is called for every guardian of a deleted family.
	SignOut []func(guardianID, tenantID string)
	// Before are purged before the profile is removed.
	Before []Dependent
	// After are purged once the family and its identities are gone.
	After []TenantDependent
	// Logs are compacted last.
	Logs []Log
}

// Service runs one deletion at a time.
type Service struct {
	cfg Config
	mu  sync.Mutex
}

func New(cfg Config) (*Service, error) {
	if cfg.Families == nil || cfg.Identities == nil {
		return nil, errors.New("deletion requires the family store and the guardian directory")
	}
	for _, d := range cfg.Before {
		if d.Store == nil {
			return nil, errors.New("deletion: missing store")
		}
	}
	for _, d := range cfg.After {
		if d.Store == nil {
			return nil, errors.New("deletion: missing store")
		}
	}
	for _, l := range cfg.Logs {
		if l.Log == nil {
			return nil, errors.New("deletion: missing log")
		}
	}
	for _, f := range cfg.SignOut {
		if f == nil {
			return nil, errors.New("deletion: missing sign-out")
		}
	}
	return &Service{cfg: cfg}, nil
}

// compact runs every log and reports whether all of them succeeded.
func (s *Service) compact() bool {
	clean := true
	for _, l := range s.cfg.Logs {
		if err := l.Log.Compact(); err != nil {
			// The log retries with its next compaction.
			log.Printf("deletion: %s could not be compacted; it retries with the next compaction", l.Name)
			clean = false
		}
	}
	return clean
}

// DeleteChild deletes one child profile and everything about that child.
// guardianID and tenantID MUST come from a verified session.
func (s *Service) DeleteChild(guardianID, tenantID, childID string) error {
	if guardianID == "" || tenantID == "" || childID == "" {
		return familystore.ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.cfg.Families.MayRemoveChild(guardianID, tenantID, childID); err != nil {
		return err
	}
	for _, d := range s.cfg.Before {
		if err := d.Store.PurgeChild(tenantID, childID); err != nil {
			log.Printf("deletion of a child stopped: %s could not be purged", d.Name)
			return ErrIncomplete
		}
	}
	if err := s.cfg.Families.RemoveChild(guardianID, tenantID, childID); err != nil {
		if errors.Is(err, familystore.ErrForbidden) || errors.Is(err, familystore.ErrLastChild) {
			return err
		}
		log.Printf("deletion of a child stopped: the profile could not be removed")
		return ErrIncomplete
	}
	if !s.compact() {
		return ErrLeftovers
	}
	return nil
}

// DeleteFamily deletes a whole family: every child, every guardian, and
// everything about them. guardianID and tenantID MUST come from a verified
// session. Afterwards no guardian of the family is signed in.
func (s *Service) DeleteFamily(guardianID, tenantID string) error {
	if guardianID == "" || tenantID == "" {
		return familystore.ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.cfg.Families.MayRemoveFamily(guardianID, tenantID); err != nil {
		return err
	}
	for _, d := range s.cfg.Before {
		if err := d.Store.PurgeTenant(tenantID); err != nil {
			log.Printf("deletion of a family stopped: %s could not be purged", d.Name)
			return ErrIncomplete
		}
	}
	guardians, err := s.cfg.Families.RemoveFamily(guardianID, tenantID)
	if err != nil {
		if errors.Is(err, familystore.ErrForbidden) || errors.Is(err, familystore.ErrLastFamily) {
			return err
		}
		log.Printf("deletion of a family stopped: the family could not be removed")
		return ErrIncomplete
	}
	clean := true
	identitiesGone := true
	if _, err := s.cfg.Identities.RemoveTenant(tenantID); err != nil {
		log.Printf("deletion of a family: the guardians' identities could not be removed; their addresses cannot register again until the operator removes them")
		clean, identitiesGone = false, false
	}
	for _, guardian := range guardians {
		for _, signOut := range s.cfg.SignOut {
			signOut(guardian, tenantID)
		}
	}
	// A lock protects against a guardian whose identity still exists.
	if identitiesGone {
		for _, d := range s.cfg.After {
			if err := d.Store.PurgeTenant(tenantID); err != nil {
				log.Printf("deletion of a family: %s could not be purged", d.Name)
				clean = false
			}
		}
	}
	if !s.compact() {
		clean = false
	}
	if !clean {
		return ErrLeftovers
	}
	return nil
}
