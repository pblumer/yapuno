package reports

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/messages"
)

// AnnounceInterval is the shortest time between two notices about the same
// child. Further reports within it are visible in Yapuno but send no mail.
const AnnounceInterval = time.Hour

// Guardians names the guardians of a child and their current permissions.
type Guardians interface {
	GuardiansOf(tenantID, childID string) []string
	Cards(guardianID, tenantID string) []familydashboard.ChildCard
	ChildName(tenantID, childID string) (string, bool)
}

// Addresses resolves the verified address of an active, unlocked guardian.
type Addresses interface {
	Email(ctx context.Context, guardianID, tenantID string) (string, bool)
}

// Mailer tells one guardian address that this child reported a message. It
// must not be given anything but the address and the child's display name.
type Mailer interface {
	SendReportNotice(ctx context.Context, email, childName string) error
	// SendReportReminder says that a report has not been looked at yet.
	SendReportReminder(ctx context.Context, email, childName string) error
}

// Announcer e-mails the guardians of a reporting child that a report is
// waiting. The mail names only that child. It is sent in the background, at
// most once per AnnounceInterval and child, and never delays the child.
type Announcer struct {
	guardians Guardians
	addresses Addresses
	mailer    Mailer
	now       func() time.Time
	timeout   time.Duration

	mu   sync.Mutex
	last map[contacts.Party]time.Time
	wg   sync.WaitGroup
}

func NewAnnouncer(guardians Guardians, addresses Addresses, mailer Mailer, now func() time.Time) (*Announcer, error) {
	if guardians == nil || addresses == nil || mailer == nil || now == nil {
		return nil, errors.New("guardians, addresses, mailer and clock are required")
	}
	return &Announcer{guardians: guardians, addresses: addresses, mailer: mailer, now: now, timeout: 15 * time.Second, last: make(map[contacts.Party]time.Time)}, nil
}

// reserve takes the slot of this child if none is taken within the interval.
func (a *Announcer) reserve(child contacts.Party) (time.Time, bool) {
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if last, ok := a.last[child]; ok && now.Sub(last) < AnnounceInterval {
		return time.Time{}, false
	}
	// Forget children whose interval is over, so the map stays small.
	for other, last := range a.last {
		if now.Sub(last) >= AnnounceInterval {
			delete(a.last, other)
		}
	}
	a.last[child] = now
	return now, true
}

// release gives the slot back when nobody was told, so the next report tries
// again instead of waiting an hour.
func (a *Announcer) release(child contacts.Party, taken time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last[child].Equal(taken) {
		delete(a.last, child)
	}
}

// Announce returns at once. The child must come from a verified session and
// must just have filed a new report.
func (a *Announcer) Announce(child contacts.Party) {
	if !validParty(child) {
		return
	}
	taken, ok := a.reserve(child)
	if !ok {
		return
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), a.timeout)
		defer cancel()
		if a.send(ctx, child, a.mailer.SendReportNotice) == 0 {
			a.release(child, taken)
		}
	}()
}

// remind sends a reminder now, unless a mail about this child went out within
// the interval: reminders and first notices share one limit, so a guardian
// never gets more than one mail per hour and child. It reports whether at
// least one guardian's mail server accepted the reminder.
func (a *Announcer) remind(ctx context.Context, child contacts.Party) bool {
	if !validParty(child) || ctx.Err() != nil {
		return false
	}
	taken, ok := a.reserve(child)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	if a.send(ctx, child, a.mailer.SendReportReminder) == 0 {
		a.release(child, taken)
		return false
	}
	return true
}

// send mails every guardian who may currently see this child's reports and
// returns how many mails the mail server accepted.
func (a *Announcer) send(ctx context.Context, child contacts.Party, mail func(ctx context.Context, email, childName string) error) int {
	name, _ := a.guardians.ChildName(child.TenantID, child.ChildID)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
	)
	for _, guardian := range a.guardians.GuardiansOf(child.TenantID, child.ChildID) {
		if !a.maySee(guardian, child) {
			continue
		}
		address, ok := a.addresses.Email(ctx, guardian, child.TenantID)
		if !ok || address == "" {
			continue
		}
		wg.Add(1)
		go func(address string) {
			defer wg.Done()
			if mail(ctx, address, name) == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}(address)
	}
	wg.Wait()
	return accepted
}

// maySee applies the same rule as the report store: current contact
// authority for exactly this child.
func (a *Announcer) maySee(guardian string, child contacts.Party) bool {
	for _, c := range a.guardians.Cards(guardian, child.TenantID) {
		if c.ID == child.ChildID && c.Permissions.ApproveContacts {
			return true
		}
	}
	return false
}

// Wait blocks until background sends have finished. It is for tests and for
// an orderly shutdown.
func (a *Announcer) Wait() { a.wg.Wait() }

// Notifying is a report store that announces every newly filed report.
type Notifying struct {
	*Store
	announcer *Announcer
}

// WithNotices wraps a store so that guardians are told about new reports.
func WithNotices(store *Store, announcer *Announcer) (*Notifying, error) {
	if store == nil || announcer == nil {
		return nil, errors.New("report store and announcer are required")
	}
	return &Notifying{Store: store, announcer: announcer}, nil
}

// File stores the report and, only if it is new, announces it. A failed or
// repeated report sends nothing.
func (n *Notifying) File(p messages.Principal, conversationID string, seq uint64) (bool, error) {
	created, err := n.Store.File(p, conversationID, seq)
	if err == nil && created {
		n.announcer.Announce(p.Party)
	}
	return created, err
}

// Reminder reminds guardians of reports that none of them has looked at.
// Run is meant to be called by a timer every few minutes.
type Reminder struct {
	store     *Store
	announcer *Announcer
	mu        sync.Mutex
}

func NewReminder(store *Store, announcer *Announcer) (*Reminder, error) {
	if store == nil || announcer == nil {
		return nil, errors.New("report store and announcer are required")
	}
	return &Reminder{store: store, announcer: announcer}, nil
}

// Run makes one pass and returns the number of children whose guardians were
// reminded. A reminder is recorded only after a mail server accepted it, so a
// failed attempt is tried again at the next run. Runs never overlap.
func (r *Reminder) Run(ctx context.Context) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	reminded := 0
	for _, child := range r.store.RemindersDue() {
		if ctx.Err() != nil {
			break
		}
		if !r.announcer.remind(ctx, child) {
			continue
		}
		reminded++
		// If this cannot be stored, the shared hourly limit still prevents a
		// mail at every run.
		_ = r.store.MarkReminded(child)
	}
	return reminded
}
