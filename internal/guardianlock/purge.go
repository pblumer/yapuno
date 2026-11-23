package guardianlock

// PurgeTenant removes the locks of every guardian of one family. It is a
// trusted call for the deletion of that family, made after the guardians'
// identities are gone: a lock must not outlive the guardian it protects
// against and then apply to somebody else with the same identifiers.
// A lock is only lifted once the change is on disk.
func (s *Store) PurgeTenant(tenantID string) error {
	if !validID(tenantID) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.read()
	if err != nil {
		return ErrUnavailable
	}
	next := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.TenantID != tenantID {
			next = append(next, e)
		}
	}
	if len(next) == len(entries) {
		return nil
	}
	if err := s.write(document{Locks: next}); err != nil {
		return ErrUnavailable
	}
	return nil
}
