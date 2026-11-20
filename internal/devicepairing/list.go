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
