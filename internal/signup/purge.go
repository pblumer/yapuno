package signup

// PurgeTenant is for the deletion of a family. It removes the family's
// unused invitations, which makes their links void, and the record that the
// family itself once joined through an invitation, which frees the place it
// took. Used invitations with which the family invited others stay: they are
// the record of how families that still exist came in. It is a trusted call;
// the caller must have authorized the deletion.
func (s *Store) PurgeTenant(tenantID string) error {
	if !validID(tenantID) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]Invitation, 0, len(s.items))
	for _, inv := range s.items {
		if inv.Joined == tenantID || (inv.Tenant == tenantID && inv.UsedAt == nil) {
			continue
		}
		next = append(next, inv)
	}
	if len(next) == len(s.items) {
		return nil
	}
	return s.commit(next)
}

// PurgeChild is for the deletion of one child. Invitations of its family
// that name it as the child to connect with keep working, but no longer name
// anybody: a link that is still open then only registers a family.
func (s *Store) PurgeChild(tenantID, childID string) error {
	if !validID(tenantID) || !validID(childID) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyItems()
	changed := false
	for i := range next {
		if next[i].Tenant == tenantID && next[i].Child == childID {
			next[i].Child = ""
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.commit(next)
}
