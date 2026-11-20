package guardianlock

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"
)

type fakeFamilies map[string][]string

func (f fakeFamilies) CoGuardians(guardianID, tenantID string) []string {
	return f[tenantID+"/"+guardianID]
}

type fakeEmails map[string]string

func (f fakeEmails) Email(_ context.Context, guardianID, tenantID string) (string, bool) {
	address, ok := f[tenantID+"/"+guardianID]
	return address, ok
}

type fakeNotifier struct {
	mu     sync.Mutex
	sent   []string
	fail   map[string]bool
	block  bool
	locked func() bool
	seen   []bool
}

func (n *fakeNotifier) SendAccessLockNotice(ctx context.Context, email string) error {
	if n.block {
		<-ctx.Done()
		return ctx.Err()
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.locked != nil {
		n.seen = append(n.seen, n.locked())
	}
	if n.fail[email] {
		return errors.New("smtp rejected")
	}
	n.sent = append(n.sent, email)
	return nil
}

func (n *fakeNotifier) recipients() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := append([]string(nil), n.sent...)
	sort.Strings(out)
	return out
}

func newLocker(t *testing.T, families fakeFamilies, emails fakeEmails, n *fakeNotifier) (*Locker, *Store) {
	t.Helper()
	store, _ := open(t)
	l, err := NewLocker(store, families, emails, n)
	if err != nil {
		t.Fatal(err)
	}
	return l, store
}

func TestLockTellsExactlyTheOtherGuardiansOfSharedChildren(t *testing.T) {
	families := fakeFamilies{"family-1/anna": {"ben", "carla"}, "family-2/anna": {"zoe"}}
	emails := fakeEmails{"family-1/ben": "ben@example.org", "family-1/carla": "carla@example.org", "family-1/anna": "anna@example.org", "family-2/zoe": "zoe@example.org", "family-1/nora": "nora@example.org"}
	n := &fakeNotifier{}
	l, store := newLocker(t, families, emails, n)
	n.locked = func() bool { return store.Locked("anna", "family-1") }
	report, err := l.Lock(context.Background(), "anna", "family-1")
	if err != nil {
		t.Fatal(err)
	}
	if !store.Locked("anna", "family-1") {
		t.Fatal("not locked")
	}
	if report != (Report{CoGuardians: 2, Notified: 2}) {
		t.Fatalf("report: %+v", report)
	}
	if got := n.recipients(); len(got) != 2 || got[0] != "ben@example.org" || got[1] != "carla@example.org" {
		t.Fatalf("recipients: %v", got)
	}
	// The lock is durable before anybody is told.
	for _, lockedWhenSent := range n.seen {
		if !lockedWhenSent {
			t.Fatal("a notice was sent before the lock was stored")
		}
	}
	// A repeated request neither fails nor sends anything again.
	again, err := l.Lock(context.Background(), "anna", "family-1")
	if err != nil || again != (Report{CoGuardians: 2, Notified: 0, AlreadyLocked: true}) || len(n.recipients()) != 2 {
		t.Fatalf("repeat: %+v %v %v", again, err, n.recipients())
	}
}

func TestLockOfASoleGuardianTellsNobody(t *testing.T) {
	n := &fakeNotifier{}
	l, store := newLocker(t, fakeFamilies{}, fakeEmails{"family-3/sole": "sole@example.org"}, n)
	report, err := l.Lock(context.Background(), "sole", "family-3")
	if err != nil || report != (Report{}) || !store.Locked("sole", "family-3") || len(n.recipients()) != 0 {
		t.Fatalf("sole guardian: %+v %v", report, err)
	}
}

func TestLockSucceedsWhenNoticesFail(t *testing.T) {
	families := fakeFamilies{"family-1/anna": {"ben", "carla", "dana", "eli"}}
	emails := fakeEmails{"family-1/ben": "ben@example.org", "family-1/carla": "carla@example.org", "family-1/eli": "eli@example.org"}
	n := &fakeNotifier{fail: map[string]bool{"carla@example.org": true}}
	l, store := newLocker(t, families, emails, n)
	// Eli locked earlier: that mailbox may be in the wrong hands and is skipped.
	if _, err := store.Lock("eli", "family-1"); err != nil {
		t.Fatal(err)
	}
	report, err := l.Lock(context.Background(), "anna", "family-1")
	if err != nil || !store.Locked("anna", "family-1") {
		t.Fatalf("lock must not depend on mail: %v", err)
	}
	// ben told; carla rejected by the mail server; dana has no usable address; eli locked.
	if report != (Report{CoGuardians: 4, Notified: 1}) {
		t.Fatalf("report: %+v", report)
	}
	if got := n.recipients(); len(got) != 1 || got[0] != "ben@example.org" {
		t.Fatalf("recipients: %v", got)
	}
}

func TestSlowMailServerCannotHoldTheRequest(t *testing.T) {
	families := fakeFamilies{"family-1/anna": {"ben", "carla"}}
	emails := fakeEmails{"family-1/ben": "ben@example.org", "family-1/carla": "carla@example.org"}
	n := &fakeNotifier{block: true}
	l, store := newLocker(t, families, emails, n)
	l.timeout = 50 * time.Millisecond
	start := time.Now()
	report, err := l.Lock(context.Background(), "anna", "family-1")
	if err != nil || !store.Locked("anna", "family-1") || report != (Report{CoGuardians: 2, Notified: 0}) {
		t.Fatalf("slow mail: %+v %v", report, err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("request held for %v", elapsed)
	}
}

func TestNothingIsSentWhenTheLockCannotBeStored(t *testing.T) {
	families := fakeFamilies{"family-1/anna": {"ben"}}
	n := &fakeNotifier{}
	l, store := newLocker(t, families, fakeEmails{"family-1/ben": "ben@example.org"}, n)
	store.failWrite = true
	if _, err := l.Lock(context.Background(), "anna", "family-1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error: %v", err)
	}
	if _, err := l.Lock(context.Background(), "", "family-1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid identity: %v", err)
	}
	if len(n.recipients()) != 0 {
		t.Fatal("a notice went out although nothing was locked")
	}
}

func TestLockerNeedsAllDependencies(t *testing.T) {
	store, _ := open(t)
	n := &fakeNotifier{}
	for _, build := range []func() (*Locker, error){
		func() (*Locker, error) { return NewLocker(nil, fakeFamilies{}, fakeEmails{}, n) },
		func() (*Locker, error) { return NewLocker(store, nil, fakeEmails{}, n) },
		func() (*Locker, error) { return NewLocker(store, fakeFamilies{}, nil, n) },
		func() (*Locker, error) { return NewLocker(store, fakeFamilies{}, fakeEmails{}, nil) },
	} {
		if _, err := build(); err == nil {
			t.Fatal("locker built without a dependency")
		}
	}
}
