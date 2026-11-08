package familystore

import "github.com/pblumer/yapuno/internal/authz"

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
