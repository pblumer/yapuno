package guardianlock

import (
	"context"
	"errors"
	"sync"
	"time"
)

// CoGuardianReader lists the other guardians sharing a child with a guardian.
type CoGuardianReader interface {
	CoGuardians(guardianID, tenantID string) []string
}

// EmailReader resolves the verified address of an active guardian.
type EmailReader interface {
	Email(ctx context.Context, guardianID, tenantID string) (string, bool)
}

// Notifier tells one address that another guardian locked their access.
type Notifier interface {
	SendAccessLockNotice(ctx context.Context, email string) error
}

// Report says who else is responsible for the guardian's children and how
// many of them the mail server accepted a notice for. Accepted is not read.
type Report struct {
	CoGuardians   int
	Notified      int
	AlreadyLocked bool
}

// Locker locks a guardian's access and then tells the other guardians of the
// same children. The lock never depends on mail: it is stored first, notices
// are best effort and bounded in time.
type Locker struct {
	store    *Store
	families CoGuardianReader
	emails   EmailReader
	notifier Notifier
	timeout  time.Duration
}

func NewLocker(store *Store, families CoGuardianReader, emails EmailReader, notifier Notifier) (*Locker, error) {
	if store == nil || families == nil || emails == nil || notifier == nil {
		return nil, errors.New("lock store, family store, directory and notifier are required")
	}
	return &Locker{store: store, families: families, emails: emails, notifier: notifier, timeout: 10 * time.Second}, nil
}

// Lock MUST only be called with the identity of a verified session of that
// same guardian. It is idempotent; only the call that creates the lock sends
// notices.
func (l *Locker) Lock(ctx context.Context, guardianID, tenantID string) (Report, error) {
	created, err := l.store.Lock(guardianID, tenantID)
	if err != nil {
		return Report{}, err
	}
	others := l.families.CoGuardians(guardianID, tenantID)
	report := Report{CoGuardians: len(others), AlreadyLocked: !created}
	if !created {
		return report, nil
	}
	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	for _, other := range others {
		// A locked guardian's mailbox may be in the wrong hands.
		if l.store.Locked(other, tenantID) {
			continue
		}
		address, ok := l.emails.Email(ctx, other, tenantID)
		if !ok || address == "" {
			continue
		}
		wg.Add(1)
		go func(address string) {
			defer wg.Done()
			if l.notifier.SendAccessLockNotice(ctx, address) == nil {
				mu.Lock()
				report.Notified++
				mu.Unlock()
			}
		}(address)
	}
	wg.Wait()
	return report, nil
}
