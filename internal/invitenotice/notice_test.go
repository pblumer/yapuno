package invitenotice

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/familydashboard"
)

type fakeGuardians struct {
	of    map[string][]string                    // "tenant/child" -> guardians
	cards map[string][]familydashboard.ChildCard // "tenant/guardian" -> cards
}

func (f fakeGuardians) GuardiansOf(tenantID, childID string) []string {
	return f.of[tenantID+"/"+childID]
}

func (f fakeGuardians) Cards(guardianID, tenantID string) []familydashboard.ChildCard {
	return f.cards[tenantID+"/"+guardianID]
}

type fakeAddresses map[string]string // "tenant/guardian" -> address

func (f fakeAddresses) Email(_ context.Context, guardianID, tenantID string) (string, bool) {
	address, ok := f[tenantID+"/"+guardianID]
	return address, ok
}

type fakeMailer struct {
	mu    sync.Mutex
	sent  []string // "address about child name"
	fail  map[string]bool
	hang  map[string]bool // addresses whose mail server never answers
	block chan struct{}
}

func (m *fakeMailer) SendInvitationContactNotice(ctx context.Context, email, childName string) error {
	if m.block != nil {
		select {
		case <-m.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if m.hang[email] {
		<-ctx.Done()
		return ctx.Err()
	}
	// Like a real mail server connection, nothing is sent after the deadline.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail[email] {
		return errors.New("smtp down")
	}
	m.sent = append(m.sent, email+" about "+childName)
	return nil
}

func (m *fakeMailer) list() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]string(nil), m.sent...)
	sort.Strings(out)
	return out
}

func full(id, name string) familydashboard.ChildCard {
	return familydashboard.ChildCard{ID: id, DisplayName: name, Permissions: familydashboard.Permissions{ManageDevices: true, ApproveContacts: true}}
}

func world(t *testing.T) (*Notifier, *fakeMailer, fakeGuardians, fakeAddresses) {
	t.Helper()
	guardians := fakeGuardians{
		of: map[string][]string{"family-a/lea": {"anna", "albert", "dora", "erik"}, "family-a/tim": {"anna"}, "family-b/lea": {"bruno"}},
		cards: map[string][]familydashboard.ChildCard{
			"family-a/anna":   {full("lea", "Lea"), full("tim", "Tim")},
			"family-a/albert": {full("lea", "Lea")},
			// Dora manages Lea's devices only and does not see her contacts.
			"family-a/dora": {{ID: "lea", DisplayName: "Lea", Permissions: familydashboard.Permissions{ManageDevices: true}}},
			// Erik may approve contacts, but has no usable address (locked).
			"family-a/erik":  {full("lea", "Lea")},
			"family-b/bruno": {full("lea", "Noah")},
		},
	}
	addresses := fakeAddresses{"family-a/anna": "anna@example.org", "family-a/albert": "albert@example.org", "family-a/dora": "dora@example.org", "family-b/bruno": "bruno@example.org"}
	mailer := &fakeMailer{fail: map[string]bool{}}
	n, err := New(guardians, addresses, mailer)
	if err != nil {
		t.Fatal(err)
	}
	return n, mailer, guardians, addresses
}

func TestEveryGuardianWhoSeesTheContactsOfTheChildIsTold(t *testing.T) {
	n, mailer, _, _ := world(t)
	n.Connected("family-a", "lea")
	n.Wait()
	got := mailer.list()
	want := []string{"albert@example.org about Lea", "anna@example.org about Lea"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("notices: %v", got)
	}
}

func TestOnlyGuardiansOfExactlyThatChildAreTold(t *testing.T) {
	n, mailer, _, _ := world(t)
	n.Connected("family-a", "tim")
	n.Wait()
	if got := mailer.list(); len(got) != 1 || got[0] != "anna@example.org about Tim" {
		t.Fatalf("sibling: %v", got)
	}
	mailer.sent = nil
	// The same child id in another family concerns that family only.
	n.Connected("family-b", "lea")
	n.Wait()
	if got := mailer.list(); len(got) != 1 || got[0] != "bruno@example.org about Noah" {
		t.Fatalf("other family: %v", got)
	}
	mailer.sent = nil
	for _, c := range [][2]string{{"family-a", "ghost"}, {"family-z", "lea"}, {"", "lea"}, {"family-a", ""}} {
		n.Connected(c[0], c[1])
	}
	n.Wait()
	if got := mailer.list(); len(got) != 0 {
		t.Fatalf("unknown child: %v", got)
	}
}

func TestAFailedMailDoesNotStopTheOthers(t *testing.T) {
	n, mailer, _, _ := world(t)
	mailer.fail["anna@example.org"] = true
	n.Connected("family-a", "lea")
	n.Wait()
	if got := mailer.list(); len(got) != 1 || got[0] != "albert@example.org about Lea" {
		t.Fatalf("notices: %v", got)
	}
}

// The registration that caused the notice must not wait for a mail server.
func TestConnectedReturnsBeforeTheMailServerAnswers(t *testing.T) {
	n, mailer, _, _ := world(t)
	mailer.block = make(chan struct{})
	done := make(chan struct{})
	go func() { n.Connected("family-a", "lea"); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Connected waits for the mail server")
	}
	if got := mailer.list(); len(got) != 0 {
		t.Fatalf("sent while blocked: %v", got)
	}
	close(mailer.block)
	n.Wait()
	if got := mailer.list(); len(got) != 2 {
		t.Fatalf("notices after release: %v", got)
	}
}

func TestASlowMailServerIsGivenUp(t *testing.T) {
	n, mailer, _, _ := world(t)
	n.timeout = 20 * time.Millisecond
	mailer.block = make(chan struct{})
	n.Connected("family-a", "lea")
	finished := make(chan struct{})
	go func() { n.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("a hanging mail server keeps the notifier busy for ever")
	}
	if got := mailer.list(); len(got) != 0 {
		t.Fatalf("notices: %v", got)
	}
}

func TestNewNeedsItsDependencies(t *testing.T) {
	_, mailer, guardians, addresses := world(t)
	if _, err := New(nil, addresses, mailer); err == nil {
		t.Error("nil guardians accepted")
	}
	if _, err := New(guardians, nil, mailer); err == nil {
		t.Error("nil addresses accepted")
	}
	if _, err := New(guardians, addresses, nil); err == nil {
		t.Error("nil mailer accepted")
	}
}

// One recipient whose mail server hangs must not cost the others their mail.
func TestAHangingRecipientDoesNotDelayTheOthers(t *testing.T) {
	n, mailer, _, _ := world(t)
	n.timeout = 50 * time.Millisecond
	mailer.hang = map[string]bool{"anna@example.org": true}
	n.Connected("family-a", "lea")
	n.Wait()
	if got := mailer.list(); len(got) != 1 || got[0] != "albert@example.org about Lea" {
		t.Fatalf("notices: %v", got)
	}
}
