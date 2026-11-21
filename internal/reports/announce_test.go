package reports

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/familydashboard"
)

type fakeGuardians struct {
	of    map[string][]string // "tenant/child" -> guardian ids
	can   map[string]bool     // "tenant/guardian/child" -> may approve contacts
	names map[string]string   // "tenant/child" -> display name
}

func (f fakeGuardians) GuardiansOf(tenantID, childID string) []string {
	return f.of[tenantID+"/"+childID]
}

func (f fakeGuardians) Cards(guardianID, tenantID string) []familydashboard.ChildCard {
	var out []familydashboard.ChildCard
	for key, ok := range f.can {
		parts := strings.Split(key, "/")
		if parts[0] == tenantID && parts[1] == guardianID {
			out = append(out, familydashboard.ChildCard{ID: parts[2], Permissions: familydashboard.Permissions{ApproveContacts: ok}})
		}
	}
	return out
}

func (f fakeGuardians) ChildName(tenantID, childID string) (string, bool) {
	name, ok := f.names[tenantID+"/"+childID]
	return name, ok
}

type fakeAddresses map[string]string

func (f fakeAddresses) Email(_ context.Context, guardianID, tenantID string) (string, bool) {
	address, ok := f[tenantID+"/"+guardianID]
	return address, ok
}

type sentNotice struct{ to, child string }

type fakeNoticeMailer struct {
	mu   sync.Mutex
	sent []sentNotice
	// reminders are kept apart from first notices.
	reminders []sentNotice
	fail      map[string]bool
	block     bool
}

func (m *fakeNoticeMailer) SendReportNotice(ctx context.Context, email, childName string) error {
	if m.block {
		<-ctx.Done()
		return ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail[email] {
		return errors.New("smtp rejected")
	}
	m.sent = append(m.sent, sentNotice{email, childName})
	return nil
}

func (m *fakeNoticeMailer) SendReportReminder(ctx context.Context, email, childName string) error {
	if m.block {
		<-ctx.Done()
		return ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail[email] {
		return errors.New("smtp rejected")
	}
	m.reminders = append(m.reminders, sentNotice{email, childName})
	return nil
}

func (m *fakeNoticeMailer) reminded() []sentNotice {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]sentNotice(nil), m.reminders...)
	sort.Slice(out, func(i, j int) bool { return out[i].to < out[j].to })
	return out
}

func (m *fakeNoticeMailer) list() []sentNotice {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]sentNotice(nil), m.sent...)
	sort.Slice(out, func(i, j int) bool { return out[i].to < out[j].to })
	return out
}

func announceWorld(t *testing.T) (*Announcer, *fakeNoticeMailer, *clock, fakeGuardians, fakeAddresses) {
	t.Helper()
	g := fakeGuardians{
		of: map[string][]string{"family-a/lea": {"albert", "anna", "nanny"}, "family-a/tim": {"anna"}, "family-b/mia": {"bruno"}},
		can: map[string]bool{
			"family-a/anna/lea": true, "family-a/albert/lea": true, "family-a/anna/tim": true, "family-b/bruno/mia": true,
			"family-a/nanny/lea": false, // may manage devices only
			"family-a/nanny/tim": true,  // contact authority for the sibling does not count for Lea
		},
		names: map[string]string{"family-a/lea": "Lea", "family-a/tim": "Tim", "family-b/mia": "Mia"},
	}
	addresses := fakeAddresses{"family-a/anna": "anna@example.org", "family-a/albert": "albert@example.org", "family-a/nanny": "nanny@example.org", "family-b/bruno": "bruno@example.org"}
	mailer := &fakeNoticeMailer{}
	c := &clock{t: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	a, err := NewAnnouncer(g, addresses, mailer, c.now)
	if err != nil {
		t.Fatal(err)
	}
	return a, mailer, c, g, addresses
}

func TestAnnouncementGoesToGuardiansWhoMaySeeTheReport(t *testing.T) {
	a, mailer, _, _, _ := announceWorld(t)
	a.Announce(lea)
	a.Wait()
	got := mailer.list()
	// Both equal guardians of Lea; not the helper without contact authority,
	// not the other family, and only Lea's name.
	if len(got) != 2 || got[0] != (sentNotice{"albert@example.org", "Lea"}) || got[1] != (sentNotice{"anna@example.org", "Lea"}) {
		t.Fatalf("recipients: %+v", got)
	}
}

func TestAtMostOneAnnouncementPerHourAndChild(t *testing.T) {
	a, mailer, c, _, _ := announceWorld(t)
	for i := 0; i < 5; i++ {
		a.Announce(lea)
		c.advance(10 * time.Minute)
	}
	a.Wait()
	if got := len(mailer.list()); got != 2 {
		t.Fatalf("several reports within an hour sent %d mails", got)
	}
	// Another child is not held back by Lea's limit.
	a.Announce(tim)
	a.Announce(mia)
	a.Wait()
	if got := len(mailer.list()); got != 4 {
		t.Fatalf("other children: %d mails", got)
	}
	// After the hour the next report is announced again.
	c.advance(11 * time.Minute)
	a.Announce(lea)
	a.Wait()
	if got := len(mailer.list()); got != 6 {
		t.Fatalf("after the hour: %d mails", got)
	}
	// Exactly at the boundary still counts as within the hour.
	c.advance(time.Hour - time.Second)
	a.Announce(lea)
	a.Wait()
	if got := len(mailer.list()); got != 6 {
		t.Fatalf("just before the hour is over: %d mails", got)
	}
}

func TestFailedAnnouncementDoesNotUseUpTheHour(t *testing.T) {
	a, mailer, _, _, _ := announceWorld(t)
	mailer.fail = map[string]bool{"anna@example.org": true, "albert@example.org": true}
	a.Announce(lea)
	a.Wait()
	if len(mailer.list()) != 0 {
		t.Fatal("precondition: nothing accepted")
	}
	// The mail server recovers: the next report must be announced.
	mailer.fail = nil
	a.Announce(lea)
	a.Wait()
	if got := len(mailer.list()); got != 2 {
		t.Fatalf("after a failed attempt: %d mails", got)
	}
	// One accepted mail is enough to count.
	mailer.fail = map[string]bool{"anna@example.org": true}
	a.Announce(tim)
	a.Wait()
	mailer.fail = nil
	a.Announce(tim)
	a.Wait()
	if got := len(mailer.list()); got != 3 {
		t.Fatalf("retry after total failure for Tim: %d mails", got)
	}
}

func TestAnnouncementSkipsGuardiansWithoutUsableAddress(t *testing.T) {
	a, mailer, _, _, addresses := announceWorld(t)
	// Albert is locked or inactive: the directory gives no address.
	delete(addresses, "family-a/albert")
	a.Announce(lea)
	a.Wait()
	if got := mailer.list(); len(got) != 1 || got[0].to != "anna@example.org" {
		t.Fatalf("recipients: %+v", got)
	}
	// Nobody reachable: nothing is sent and nothing breaks.
	delete(addresses, "family-b/bruno")
	a.Announce(mia)
	a.Announce(contacts.Party{TenantID: "family-x", ChildID: "ghost"})
	a.Announce(contacts.Party{})
	a.Wait()
	if len(mailer.list()) != 1 {
		t.Fatal("mail sent to nobody")
	}
}

func TestSlowMailServerDoesNotBlockTheChild(t *testing.T) {
	a, mailer, _, _, _ := announceWorld(t)
	mailer.block = true
	a.timeout = 50 * time.Millisecond
	start := time.Now()
	a.Announce(lea)
	if elapsed := time.Since(start); elapsed > 30*time.Millisecond {
		t.Fatalf("Announce waited for the mail server: %v", elapsed)
	}
	done := make(chan struct{})
	go func() { a.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("background send did not respect its time limit")
	}
}

func TestConcurrentReportsSendOneAnnouncement(t *testing.T) {
	a, mailer, _, _, _ := announceWorld(t)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); a.Announce(lea) }()
	}
	wg.Wait()
	a.Wait()
	if got := len(mailer.list()); got != 2 {
		t.Fatalf("concurrent reports sent %d mails", got)
	}
}

func TestOnlyANewReportIsAnnounced(t *testing.T) {
	w := newWorld(t)
	g := fakeGuardians{of: map[string][]string{"family-a/lea": {"anna"}}, can: map[string]bool{"family-a/anna/lea": true}, names: map[string]string{"family-a/lea": "Lea"}}
	mailer := &fakeNoticeMailer{}
	a, err := NewAnnouncer(g, fakeAddresses{"family-a/anna": "anna@example.org"}, mailer, w.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	notifying, err := WithNotices(w.reports, a)
	if err != nil {
		t.Fatal(err)
	}
	own := w.say(w.leaP, w.chat, "eigene")
	theirs := w.say(w.miaP, w.chat, "gemein")
	// Refused reports announce nothing.
	if _, err := notifying.File(w.leaP, w.chat, own); err == nil {
		t.Fatal("own message reported")
	}
	if _, err := notifying.File(w.leaP, w.chat, 99); err == nil {
		t.Fatal("missing message reported")
	}
	a.Wait()
	if len(mailer.list()) != 0 {
		t.Fatal("a refused report was announced")
	}
	if created, err := notifying.File(w.leaP, w.chat, theirs); err != nil || !created {
		t.Fatalf("report: %v %v", created, err)
	}
	a.Wait()
	if got := mailer.list(); len(got) != 1 || got[0] != (sentNotice{"anna@example.org", "Lea"}) {
		t.Fatalf("announcement: %+v", got)
	}
	// The wrapper still exposes the store for reading.
	if len(notifying.Reported(lea, w.chat)) != 1 || w.reports.Count("anna", lea.TenantID, lea.ChildID) != 1 {
		t.Fatal("report not stored")
	}
	// The same message again is not a new report: no further mail even after the hour.
	w.clock.advance(2 * time.Hour)
	if created, err := notifying.File(w.leaP, w.chat, theirs); err != nil || created {
		t.Fatalf("repeat: %v %v", created, err)
	}
	a.Wait()
	if len(mailer.list()) != 1 {
		t.Fatal("a repeated report was announced")
	}
}

func TestAnnouncerNeedsItsDependencies(t *testing.T) {
	g, addresses, mailer := fakeGuardians{}, fakeAddresses{}, &fakeNoticeMailer{}
	for _, build := range []func() (*Announcer, error){
		func() (*Announcer, error) { return NewAnnouncer(nil, addresses, mailer, time.Now) },
		func() (*Announcer, error) { return NewAnnouncer(g, nil, mailer, time.Now) },
		func() (*Announcer, error) { return NewAnnouncer(g, addresses, nil, time.Now) },
		func() (*Announcer, error) { return NewAnnouncer(g, addresses, mailer, nil) },
	} {
		if _, err := build(); err == nil {
			t.Fatal("announcer built without a dependency")
		}
	}
	a, _ := NewAnnouncer(g, addresses, mailer, time.Now)
	if _, err := WithNotices(nil, a); err == nil {
		t.Fatal("wrapper built without a store")
	}
	w := newWorld(t)
	if _, err := WithNotices(w.reports, nil); err == nil {
		t.Fatal("wrapper built without an announcer")
	}
}

func TestAnnouncementIsAllowedAgainExactlyAfterTheInterval(t *testing.T) {
	a, mailer, c, _, _ := announceWorld(t)
	a.Announce(tim)
	a.Wait()
	c.advance(AnnounceInterval)
	a.Announce(tim)
	a.Wait()
	if got := len(mailer.list()); got != 2 {
		t.Fatalf("one hour later: %d mails", got)
	}
}
