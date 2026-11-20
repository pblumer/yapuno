package devicepairing

// ListDevices returns the current devices for one child to a guardian with
// active device-management rights. It includes revoked devices for audit/UI.
// Callers MUST derive guardian and tenant from a verified server session.
func (s *Store) ListDevices(guardian, tenant, child string) ([]Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(guardian, tenant, child) {
		return nil, ErrForbidden
	}
	out := make([]Device, 0)
	for _, d := range s.state.Devices {
		if d.TenantID == tenant && d.ChildID == child {
			out = append(out, d)
		}
	}
	return out, nil
}

// DeviceByID returns an active (not revoked) device by its random id. It is
// only a key lookup for proof-of-possession login: the caller must verify a
// fresh signature against the returned public key before trusting anything.
func (s *Store) DeviceByID(deviceID string) (Device, bool) {
	if deviceID == "" {
		return Device{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.state.Devices {
		if d.ID == deviceID && !d.Revoked {
			return d, true
		}
	}
	return Device{}, false
}

// RevokeAll locks every active device of one child in a single durable step
// and cancels that child's pairings in progress, so a pairing started before
// the lock cannot produce a fresh device afterwards. It is meant for a lost
// or stolen device when the guardian is not sure which entry it is. It
// returns the number of devices that were active. A new pairing can be
// started afterwards.
func (s *Store) RevokeAll(guardian, tenant, child string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(guardian, tenant, child) {
		return 0, ErrForbidden
	}
	next := s.clone()
	revoked, cancelled := 0, 0
	for i, d := range next.Devices {
		if d.TenantID == tenant && d.ChildID == child && !d.Revoked {
			next.Devices[i].Revoked = true
			revoked++
		}
	}
	for i, c := range next.Challenges {
		if c.TenantID == tenant && c.ChildID == child && !c.Consumed {
			next.Challenges[i].Consumed = true
			cancelled++
		}
	}
	if revoked == 0 && cancelled == 0 {
		return 0, nil
	}
	if err := s.commit(next); err != nil {
		return 0, err
	}
	return revoked, nil
}
