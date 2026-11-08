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
