package reports

import "github.com/pblumer/yapuno/internal/contacts"

// PurgeChild removes every report that one child filed, with the copies of
// the reported messages. It is a trusted call for the deletion of a child
// profile and performs no guardian check; the caller must have authorized
// the deletion.
//
// A report that another child filed about a message of this child is not
// removed: it belongs to the family that filed it, who close it themselves
// or let it expire.
func (s *Store) PurgeChild(tenant, child string) error {
	target := contacts.Party{TenantID: tenant, ChildID: child}
	if tenant == "" || child == "" {
		return ErrInvalid
	}
	return s.purge(func(p contacts.Party) bool { return p == target })
}

// PurgeTenant is PurgeChild for every child of one family.
func (s *Store) PurgeTenant(tenant string) error {
	if tenant == "" {
		return ErrInvalid
	}
	return s.purge(func(p contacts.Party) bool { return p.TenantID == tenant })
}

func (s *Store) purge(match func(contacts.Party) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]record, 0, len(s.records))
	for _, r := range s.records {
		if !match(r.Reporter) {
			next = append(next, r)
		}
	}
	if len(next) == len(s.records) {
		return nil
	}
	return s.commit(next)
}
