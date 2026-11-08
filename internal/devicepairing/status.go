package devicepairing

import "time"

// Pending is the public-to-guardian projection of a challenge. It never
// includes the public key, nonce or raw token.
type Pending struct {
	Name        string
	Fingerprint string
	Expires     time.Time
	Approved    bool
}

// Status shows the claimed device fingerprint only to a currently authorized
// guardian of the exact child and tenant. The raw token is a caller-supplied
// ephemeral capability and is never returned or persisted.
func (s *Store) Status(guardian, tenant, child, token string) (Pending, error) {
	d, ok := digest(token)
	if !ok {
		return Pending{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(guardian, tenant, child) {
		return Pending{}, ErrForbidden
	}
	i := s.index(d)
	if err := s.validChallenge(i); err != nil {
		return Pending{}, err
	}
	c := s.state.Challenges[i]
	if c.TenantID != tenant || c.ChildID != child {
		return Pending{}, ErrForbidden
	}
	return Pending{Name: c.Name, Fingerprint: c.Fingerprint, Expires: c.Expires, Approved: c.Approver != ""}, nil
}
