package signup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newStore(t *testing.T) (*Store, *clock, string) {
	t.Helper()
	c := &clock{t: time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "signup.json")
	s, err := Open(path, c.now)
	if err != nil {
		t.Fatal(err)
	}
	return s, c, path
}

func TestInviteReturnsAOneTimeCodeAndStoresOnlyItsDigest(t *testing.T) {
	s, c, path := newStore(t)
	code, inv, err := s.Invite("anna", "family-a", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 22 || inv.ID == "" || inv.ID == code || !inv.ExpiresAt.Equal(c.now().Add(InvitationTTL)) {
		t.Fatalf("invitation: %q %+v", code, inv)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), code) {
		t.Fatal("the code itself is on disk")
	}
	info, _ := os.Lstat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("file mode %v", info.Mode())
	}
	got, err := s.Lookup(code)
	if err != nil || got.ID != inv.ID || got.Guardian != "anna" || got.Tenant != "family-a" {
		t.Fatalf("lookup: %+v %v", got, err)
	}
	other, _, _ := s.Invite("anna", "family-a", "")
	if other == code {
		t.Fatal("codes repeat")
	}
	// Durable: a restart keeps the invitation usable.
	reopened, err := Open(path, c.now)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reopened.Lookup(code); err != nil || got.ID != inv.ID {
		t.Fatalf("after restart: %+v %v", got, err)
	}
}

func TestLookupRefusesUnknownMalformedExpiredAndUsedCodes(t *testing.T) {
	s, c, _ := newStore(t)
	code, inv, _ := s.Invite("anna", "family-a", "")
	for _, bad := range []string{"", "short", code + "x", strings.Repeat("A", 22), code[:21] + "!", " " + code[1:]} {
		if _, err := s.Lookup(bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	c.add(InvitationTTL - time.Second)
	if _, err := s.Lookup(code); err != nil {
		t.Fatalf("just before expiry: %v", err)
	}
	c.add(time.Second)
	if _, err := s.Lookup(code); !errors.Is(err, ErrNotFound) {
		t.Fatalf("at expiry: %v", err)
	}
	if _, err := s.Usable(inv.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired by id: %v", err)
	}
	if err := s.Consume(inv.ID, "family-b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired invitation consumed: %v", err)
	}
}

func TestConsumeWorksExactlyOnce(t *testing.T) {
	s, c, path := newStore(t)
	code, inv, _ := s.Invite("anna", "family-a", "")
	var wg sync.WaitGroup
	wins := make(chan bool, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wins <- s.Consume(inv.ID, "family-b") == nil
		}()
	}
	wg.Wait()
	close(wins)
	won := 0
	for ok := range wins {
		if ok {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("invitation consumed %d times", won)
	}
	if _, err := s.Lookup(code); !errors.Is(err, ErrNotFound) {
		t.Fatalf("used code still valid: %v", err)
	}
	if s.Registered() != 1 || len(s.Pending("anna", "family-a")) != 0 {
		t.Fatal("use not recorded")
	}
	reopened, _ := Open(path, c.now)
	if _, err := reopened.Lookup(code); !errors.Is(err, ErrNotFound) || reopened.Registered() != 1 {
		t.Fatal("use not durable")
	}
	for _, bad := range [][2]string{{"", "t"}, {inv.ID, ""}, {"unknown", "t"}} {
		if err := s.Consume(bad[0], bad[1]); err == nil {
			t.Errorf("%v: accepted", bad)
		}
	}
}

func TestRestoreReopensOnlyTheMatchingUse(t *testing.T) {
	s, _, _ := newStore(t)
	code, inv, _ := s.Invite("anna", "family-a", "")
	if err := s.Restore(inv.ID, "family-b"); err == nil {
		t.Fatal("restored an invitation that was not used")
	}
	if err := s.Consume(inv.ID, "family-b"); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(inv.ID, "family-x"); err == nil {
		t.Fatal("restored for another family")
	}
	if err := s.Restore(inv.ID, "family-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(code); err != nil || s.Registered() != 0 {
		t.Fatalf("not usable again: %v", err)
	}
}

func TestOpenInvitationsAreLimitedPerGuardianAndListedOnlyToTheirOwner(t *testing.T) {
	s, c, _ := newStore(t)
	var first Invitation
	for i := 0; i < MaxOpenPerGuardian; i++ {
		_, inv, err := s.Invite("anna", "family-a", "")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = inv
		}
		c.add(time.Minute)
	}
	if _, _, err := s.Invite("anna", "family-a", ""); !errors.Is(err, ErrLimit) {
		t.Fatalf("over the limit: %v", err)
	}
	// Another guardian, and the same id in another family, have their own limit.
	if _, _, err := s.Invite("bruno", "family-a", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Invite("anna", "family-z", ""); err != nil {
		t.Fatal(err)
	}
	mine := s.Pending("anna", "family-a")
	if len(mine) != MaxOpenPerGuardian || mine[0].ID != first.ID {
		t.Fatalf("pending: %+v", mine)
	}
	for _, inv := range mine {
		if inv.Guardian != "anna" || inv.Tenant != "family-a" {
			t.Fatalf("foreign invitation listed: %+v", inv)
		}
	}
	// Only the owner withdraws; afterwards there is room again.
	if err := s.Withdraw("bruno", "family-a", first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign withdrawal: %v", err)
	}
	if err := s.Withdraw("anna", "family-z", first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("withdrawal across tenants: %v", err)
	}
	if err := s.Withdraw("anna", "family-a", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Usable(first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("withdrawn invitation still usable")
	}
	if _, _, err := s.Invite("anna", "family-a", ""); err != nil {
		t.Fatalf("after withdrawal: %v", err)
	}
	// Expired invitations free their place as well.
	c.add(InvitationTTL)
	if len(s.Pending("anna", "family-a")) != 0 {
		t.Fatal("expired invitations listed")
	}
	if _, _, err := s.Invite("anna", "family-a", ""); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	for _, bad := range [][2]string{{"", "t"}, {"g", ""}, {"g\n", "t"}} {
		if _, _, err := s.Invite(bad[0], bad[1], ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestMailsPerInvitationAreCountedDurably(t *testing.T) {
	s, c, path := newStore(t)
	_, inv, _ := s.Invite("anna", "family-a", "")
	for i := 0; i < MaxMailsPerInvitation-1; i++ {
		if err := s.CountMail(inv.ID); err != nil {
			t.Fatalf("mail %d: %v", i, err)
		}
	}
	// A restart does not reset the budget.
	reopened, err := Open(path, c.now)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.CountMail(inv.ID); err != nil {
		t.Fatal(err)
	}
	if err := reopened.CountMail(inv.ID); !errors.Is(err, ErrLimit) {
		t.Fatalf("over the mail budget: %v", err)
	}
	if err := reopened.CountMail("unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown invitation: %v", err)
	}
}

func TestTheNumberOfRegisteredFamiliesIsCapped(t *testing.T) {
	s, _, _ := newStore(t)
	for i := 0; i < MaxFamilies; i++ {
		_, inv, err := s.Invite("anna", "family-a", "")
		if err != nil {
			t.Fatalf("invitation %d: %v", i, err)
		}
		if err := s.Consume(inv.ID, "joined"+string(rune('A'+i%26))+string(rune('a'+i/26))); err != nil {
			t.Fatalf("use %d: %v", i, err)
		}
	}
	if _, _, err := s.Invite("anna", "family-a", ""); !errors.Is(err, ErrFull) {
		t.Fatalf("invitation beyond the cap: %v", err)
	}
}

func TestAnInvitationCreatedBeforeTheCapCannotExceedIt(t *testing.T) {
	s, _, _ := newStore(t)
	_, spare, _ := s.Invite("bruno", "family-b", "")
	for i := 0; i < MaxFamilies; i++ {
		_, inv, _ := s.Invite("anna", "family-a", "")
		if err := s.Consume(inv.ID, "joined"+string(rune('A'+i%26))+string(rune('a'+i/26))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Consume(spare.ID, "late"); !errors.Is(err, ErrFull) {
		t.Fatalf("use beyond the cap: %v", err)
	}
}

func TestOpenFailsClosed(t *testing.T) {
	c := &clock{t: time.Now()}
	if _, err := Open("", c.now); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, err := Open(filepath.Join(t.TempDir(), "x.json"), nil); err == nil {
		t.Fatal("missing clock accepted")
	}
	for name, content := range map[string]string{
		"corrupt":         `{"invitations":[`,
		"unknown field":   `{"invitations":[],"extra":1}`,
		"trailing data":   `{"invitations":[]} {}`,
		"empty digest":    `{"invitations":[{"id":"a","digest":"","guardian":"g","tenant":"t","created_at":"2026-01-01T00:00:00Z","expires_at":"2026-01-02T00:00:00Z"}]}`,
		"duplicate id":    `{"invitations":[{"id":"a","digest":"` + strings.Repeat("a", 64) + `","guardian":"g","tenant":"t","created_at":"2026-01-01T00:00:00Z","expires_at":"2026-01-02T00:00:00Z"},{"id":"a","digest":"` + strings.Repeat("b", 64) + `","guardian":"g","tenant":"t","created_at":"2026-01-01T00:00:00Z","expires_at":"2026-01-02T00:00:00Z"}]}`,
		"invalid child":   `{"invitations":[{"id":"a","digest":"` + strings.Repeat("a", 64) + `","guardian":"g","tenant":"t","child":"c\nd","created_at":"2026-01-01T00:00:00Z","expires_at":"2026-01-02T00:00:00Z"}]}`,
		"used, no tenant": `{"invitations":[{"id":"a","digest":"` + strings.Repeat("a", 64) + `","guardian":"g","tenant":"t","created_at":"2026-01-01T00:00:00Z","expires_at":"2026-01-02T00:00:00Z","used_at":"2026-01-01T01:00:00Z"}]}`,
	} {
		path := filepath.Join(t.TempDir(), "signup.json")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(path, c.now); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	path := filepath.Join(t.TempDir(), "signup.json")
	_ = os.WriteFile(path, []byte(`{"invitations":[]}`), 0644)
	if _, err := Open(path, c.now); err == nil {
		t.Fatal("world-readable file opened")
	}
	real := filepath.Join(t.TempDir(), "real.json")
	_ = os.WriteFile(real, []byte(`{"invitations":[]}`), 0600)
	link := filepath.Join(t.TempDir(), "link.json")
	_ = os.Symlink(real, link)
	if _, err := Open(link, c.now); err == nil {
		t.Fatal("symlink opened")
	}
}

func TestAFailedWriteChangesNothing(t *testing.T) {
	s, _, _ := newStore(t)
	code, inv, _ := s.Invite("anna", "family-a", "")
	s.failWrite = true
	if _, _, err := s.Invite("anna", "family-a", ""); err == nil {
		t.Fatal("invitation acknowledged without a durable write")
	}
	if err := s.Consume(inv.ID, "family-b"); err == nil {
		t.Fatal("use acknowledged without a durable write")
	}
	if err := s.CountMail(inv.ID); err == nil {
		t.Fatal("mail counted without a durable write")
	}
	s.failWrite = false
	if _, err := s.Lookup(code); err != nil || len(s.Pending("anna", "family-a")) != 1 || s.Registered() != 0 {
		t.Fatal("a failed write changed the state")
	}
	for i := 0; i < MaxMailsPerInvitation; i++ {
		if err := s.CountMail(inv.ID); err != nil {
			t.Fatalf("the failed count used up budget: %v", err)
		}
	}
}

// An invitation can name one child of the inviting family. That is the
// inviting guardian's approval for a contact with the invited family's child.
func TestInvitationCanNameTheInvitingChild(t *testing.T) {
	s, c, path := newStore(t)
	code, inv, err := s.Invite("anna", "family-a", "lea")
	if err != nil || inv.Child != "lea" {
		t.Fatalf("invite: %+v %v", inv, err)
	}
	plainCode, plain, err := s.Invite("anna", "family-a", "")
	if err != nil || plain.Child != "" {
		t.Fatalf("invite without child: %+v %v", plain, err)
	}
	reopened, err := Open(path, c.now)
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []*Store{s, reopened} {
		if got, err := store.Lookup(code); err != nil || got.Child != "lea" {
			t.Fatalf("lookup: %+v %v", got, err)
		}
		if got, err := store.Usable(inv.ID); err != nil || got.Child != "lea" {
			t.Fatalf("usable: %+v %v", got, err)
		}
		if got, err := store.Lookup(plainCode); err != nil || got.Child != "" {
			t.Fatalf("lookup without child: %+v %v", got, err)
		}
		pending := store.Pending("anna", "family-a")
		if len(pending) != 2 || pending[0].Child != "lea" || pending[1].Child != "" {
			t.Fatalf("pending: %+v", pending)
		}
	}
	// A file written before invitations could name a child has no such key.
	raw, _ := os.ReadFile(path)
	if strings.Count(string(raw), `"child"`) != 1 {
		t.Fatalf("child key written for an invitation without one: %s", raw)
	}
	for _, bad := range []string{"lea\n", "le\x00a", strings.Repeat("x", 129)} {
		if _, _, err := s.Invite("anna", "family-a", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	// The child stays on record once the invitation is used.
	if err := s.Consume(inv.ID, "family-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, c.now); err != nil {
		t.Fatalf("used invitation with a child does not reopen: %v", err)
	}
}
