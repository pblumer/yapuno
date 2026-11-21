package familystore

import (
	"strings"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
)

// MaxChildrenPerFamily bounds what guardians can create themselves.
const MaxChildrenPerFamily = 8

// CreateFamily starts a new tenant with one child and one guardian who holds
// the full baseline grant. It refuses a tenant that already has any record,
// so a new guardian can never be attached to an existing family this way.
//
// It is a trusted call for invited self-registration: the caller must have
// checked a valid invitation and control of the guardian's e-mail address.
// It does not verify guardianship; that remains self-declared.
func (s *Store) CreateFamily(tenantID, guardianID, childID, name string) error {
	if tenantID == "" || guardianID == "" || childID == "" || !validName(name) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.state.Children {
		if c.TenantID == tenantID {
			return ErrDuplicate
		}
	}
	// Every grant belongs to a child of its tenant, so the children are enough.
	next := clone(s.state)
	next.Children = append(next.Children, familydashboard.Child{ID: childID, TenantID: tenantID, DisplayName: strings.TrimSpace(name)})
	next.Grants = append(next.Grants, fullGrant(guardianID, tenantID, childID))
	return s.commit(next)
}

func fullGrant(guardianID, tenantID, childID string) authz.GuardianGrant {
	return authz.GuardianGrant{GuardianID: guardianID, ChildID: childID, TenantID: tenantID, Active: true, CanManageDevices: true, CanApproveContacts: true}
}

// AddChild creates another child in the guardian's own family. The guardian
// must already hold full authority for a child of that tenant, and receives
// the grant for the new child; other guardians of the family do not.
// Authentication of guardianID MUST happen before calling this method.
func (s *Store) AddChild(guardianID, tenantID, childID, name string) error {
	if guardianID == "" || tenantID == "" || childID == "" || !validName(name) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	authorized := false
	for _, card := range familydashboard.Build(guardianID, tenantID, s.state.Children, s.state.Grants) {
		if card.Permissions.ManageDevices && card.Permissions.ApproveContacts {
			authorized = true
		}
	}
	if !authorized {
		return ErrForbidden
	}
	inFamily := 0
	for _, c := range s.state.Children {
		if c.TenantID == tenantID {
			inFamily++
		}
	}
	if inFamily >= MaxChildrenPerFamily {
		return ErrLimit
	}
	// commit rejects a child id that already exists in this family.
	next := clone(s.state)
	next.Children = append(next.Children, familydashboard.Child{ID: childID, TenantID: tenantID, DisplayName: strings.TrimSpace(name)})
	next.Grants = append(next.Grants, fullGrant(guardianID, tenantID, childID))
	return s.commit(next)
}

// DiscardFamily undoes CreateFamily when the registration could not be
// completed. It only removes a tenant that still consists of exactly that
// one child and that one grant; anything else is real data and stays.
func (s *Store) DiscardFamily(tenantID, guardianID, childID string) error {
	if tenantID == "" || guardianID == "" || childID == "" {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := State{}
	children, grants := 0, 0
	for _, c := range s.state.Children {
		if c.TenantID != tenantID {
			next.Children = append(next.Children, c)
			continue
		}
		children++
	}
	for _, g := range s.state.Grants {
		if g.TenantID != tenantID {
			next.Grants = append(next.Grants, g)
			continue
		}
		if g.ChildID != childID || g.GuardianID != guardianID {
			return ErrForbidden
		}
		grants++
	}
	if children != 1 || grants != 1 {
		return ErrForbidden
	}
	return s.commit(next)
}
