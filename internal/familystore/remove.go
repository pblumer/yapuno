package familystore

import (
	"errors"
	"sort"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
)

var (
	// ErrLastChild means the child is the only one of its family. Without a
	// child no guardian of the family can sign in, so it goes only together
	// with the family.
	ErrLastChild = errors.New("the last child of a family cannot be removed on its own")
	// ErrLastFamily means no other family exists. Somebody has to be left
	// who can sign in and invite.
	ErrLastFamily = errors.New("the last family cannot be removed")
)

// full reports whether the guardian holds full authority for this child.
func full(state State, guardianID, tenantID, childID string) bool {
	for _, card := range familydashboard.Build(guardianID, tenantID, state.Children, state.Grants) {
		if card.ID == childID && card.Permissions.ManageDevices && card.Permissions.ApproveContacts {
			return true
		}
	}
	return false
}

func (s *Store) mayRemoveChild(guardianID, tenantID, childID string) error {
	if guardianID == "" || tenantID == "" || childID == "" || !full(s.state, guardianID, tenantID, childID) {
		return ErrForbidden
	}
	inFamily := 0
	for _, c := range s.state.Children {
		if c.TenantID == tenantID {
			inFamily++
		}
	}
	if inFamily < 2 {
		return ErrLastChild
	}
	return nil
}

// MayRemoveChild reports whether RemoveChild would be allowed right now. It
// lets a caller refuse before it deletes anything that depends on the child.
func (s *Store) MayRemoveChild(guardianID, tenantID, childID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mayRemoveChild(guardianID, tenantID, childID)
}

// RemoveChild deletes one child profile and every grant for it. The guardian
// must hold full authority for exactly this child; other guardians of the
// child are not asked. Authentication of guardianID MUST happen before
// calling this method. It deletes nothing outside this store: the caller
// removes what other stores hold about the child.
func (s *Store) RemoveChild(guardianID, tenantID, childID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.mayRemoveChild(guardianID, tenantID, childID); err != nil {
		return err
	}
	next := State{}
	for _, c := range s.state.Children {
		if c.TenantID != tenantID || c.ID != childID {
			next.Children = append(next.Children, c)
		}
	}
	for _, g := range s.state.Grants {
		if g.TenantID != tenantID || g.ChildID != childID {
			next.Grants = append(next.Grants, g)
		}
	}
	return s.commit(next)
}

func (s *Store) mayRemoveFamily(guardianID, tenantID string) error {
	if guardianID == "" || tenantID == "" {
		return ErrForbidden
	}
	own, others := 0, false
	for _, c := range s.state.Children {
		if c.TenantID != tenantID {
			others = true
			continue
		}
		own++
		// One child the guardian does not fully answer for is enough to refuse.
		if !full(s.state, guardianID, tenantID, c.ID) {
			return ErrForbidden
		}
	}
	if own == 0 {
		return ErrForbidden
	}
	if !others {
		return ErrLastFamily
	}
	return nil
}

// MayRemoveFamily reports whether RemoveFamily would be allowed right now.
func (s *Store) MayRemoveFamily(guardianID, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mayRemoveFamily(guardianID, tenantID)
}

// RemoveFamily deletes every child profile and every grant of one family.
// The guardian must hold full authority for every child of that family, and
// another family must remain. It returns the ids of all guardians who held a
// grant in the family, active or not, sorted, so that the caller can remove
// their identities and sessions. Authentication of guardianID MUST happen
// before calling this method.
func (s *Store) RemoveFamily(guardianID, tenantID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.mayRemoveFamily(guardianID, tenantID); err != nil {
		return nil, err
	}
	next := State{}
	for _, c := range s.state.Children {
		if c.TenantID != tenantID {
			next.Children = append(next.Children, c)
		}
	}
	seen := map[string]bool{}
	var guardians []string
	keep := []authz.GuardianGrant{}
	for _, g := range s.state.Grants {
		if g.TenantID != tenantID {
			keep = append(keep, g)
			continue
		}
		if !seen[g.GuardianID] {
			seen[g.GuardianID] = true
			guardians = append(guardians, g.GuardianID)
		}
	}
	next.Grants = keep
	if err := s.commit(next); err != nil {
		return nil, err
	}
	sort.Strings(guardians)
	return guardians, nil
}
