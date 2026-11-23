package contacts

// PurgeChild removes every relationship of one child from the store, active
// or pending, and its invitation codes. It is a trusted call for the deletion
// of a child profile and performs no guardian check; the caller must have
// authorized the deletion. The other child of each relationship simply has
// one contact less. Purging what is not there is not an error.
func (s *Store) PurgeChild(tenant, child string) error {
	target := Party{TenantID: tenant, ChildID: child}
	if !target.valid() {
		return ErrInvalid
	}
	return s.purge(func(p Party) bool { return p == target })
}

// PurgeTenant is PurgeChild for every child of one family.
func (s *Store) PurgeTenant(tenant string) error {
	if tenant == "" {
		return ErrInvalid
	}
	return s.purge(func(p Party) bool { return p.TenantID == tenant })
}

func (s *Store) purge(match func(Party) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := state{}
	changed := false
	for _, inv := range s.state.Invitations {
		if match(inv.Inviter) {
			changed = true
			continue
		}
		next.Invitations = append(next.Invitations, inv)
	}
	for _, l := range s.state.Links {
		if match(l.Inviter) || match(l.Invitee) {
			changed = true
			continue
		}
		next.Links = append(next.Links, l)
	}
	if !changed {
		return nil
	}
	return s.commit(next)
}
