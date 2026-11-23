package devicepairing

// PurgeChild removes every device and every pairing of one child from the
// store: active and revoked devices, and requests a guardian approved for
// that child. It is a trusted call for the deletion of a child profile and
// performs no guardian check; the caller must have authorized the deletion.
// Purging what is not there is not an error.
func (s *Store) PurgeChild(tenant, child string) error {
	if tenant == "" || child == "" {
		return ErrInvalid
	}
	return s.purge(func(t, c string) bool { return t == tenant && c == child })
}

// PurgeTenant is PurgeChild for every child of one family.
func (s *Store) PurgeTenant(tenant string) error {
	if tenant == "" {
		return ErrInvalid
	}
	return s.purge(func(t, _ string) bool { return t == tenant })
}

func (s *Store) purge(match func(tenant, child string) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := state{}
	changed := false
	for _, c := range s.state.Challenges {
		// A request nobody approved yet carries no tenant and stays.
		if c.TenantID != "" && match(c.TenantID, c.ChildID) {
			changed = true
			continue
		}
		next.Challenges = append(next.Challenges, c)
	}
	for _, d := range s.state.Devices {
		if match(d.TenantID, d.ChildID) {
			changed = true
			continue
		}
		next.Devices = append(next.Devices, d)
	}
	if !changed {
		return nil
	}
	return s.commit(next)
}
