package familystore

import (
	"sort"

	"github.com/pblumer/yapuno/internal/authz"
)

// BootstrapGuardian adds a separately verified guardian for an existing child.
// This is a trusted provisioning API, NOT a user action. The caller must have
// independently verified guardian identity, legal relationship, step-up
// authorization, notifications and anti-lockout conditions. Never expose this
// method directly as an HTTP endpoint or accept client-supplied grants.
// All newly provisioned guardians receive equal baseline capabilities.
func (s *Store) BootstrapGuardian(grant authz.GuardianGrant) error {
	if grant.GuardianID == "" || grant.ChildID == "" || grant.TenantID == "" ||
		!grant.Active || !grant.CanManageDevices || !grant.CanApproveContacts {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for _, child := range s.state.Children {
		if child.ID == grant.ChildID && child.TenantID == grant.TenantID {
			found = true
			break
		}
	}
	if !found {
		return ErrForbidden
	}
	for _, existing := range s.state.Grants {
		if existing.GuardianID == grant.GuardianID && existing.ChildID == grant.ChildID && existing.TenantID == grant.TenantID {
			return ErrDuplicate
		}
	}
	next := clone(s.state)
	next.Grants = append(next.Grants, grant)
	return s.commit(next)
}

// CoGuardians returns the ids of the other guardians who hold an active
// grant for at least one child this guardian also holds an active grant for,
// in the same tenant, sorted. It is used to notify them of security events;
// it is not an authorization check and reveals no child data.
func (s *Store) CoGuardians(guardianID, tenantID string) []string {
	if guardianID == "" || tenantID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	children := make(map[string]bool)
	for _, g := range s.state.Grants {
		if g.Active && g.GuardianID == guardianID && g.TenantID == tenantID {
			children[g.ChildID] = true
		}
	}
	seen := make(map[string]bool)
	var out []string
	for _, g := range s.state.Grants {
		if g.Active && g.TenantID == tenantID && g.GuardianID != guardianID && g.GuardianID != "" && children[g.ChildID] && !seen[g.GuardianID] {
			seen[g.GuardianID] = true
			out = append(out, g.GuardianID)
		}
	}
	sort.Strings(out)
	return out
}
