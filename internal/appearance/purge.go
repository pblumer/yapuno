package appearance

// PurgeChild removes the saved look of one child. It is a trusted call for
// the deletion of a child profile; the caller must have authorized it.
func (s *Store) PurgeChild(tenantID, childID string) error {
	if tenantID == "" || childID == "" {
		return ErrInvalid
	}
	target := Subject{Kind: KindChild, TenantID: tenantID, ID: childID}
	return s.purge(func(subject Subject) bool { return subject == target })
}

// PurgeTenant removes the saved looks of every child and guardian of one family.
func (s *Store) PurgeTenant(tenantID string) error {
	if tenantID == "" {
		return ErrInvalid
	}
	return s.purge(func(subject Subject) bool { return subject.TenantID == tenantID })
}

func (s *Store) purge(match func(Subject) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[Subject]Preference, len(s.prefs))
	for subject, pref := range s.prefs {
		if !match(subject) {
			next[subject] = pref
		}
	}
	if len(next) == len(s.prefs) {
		return nil
	}
	if err := s.commit(next); err != nil {
		return err
	}
	s.prefs = next
	return nil
}
