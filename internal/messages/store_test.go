package messages

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
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

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var (
	leaParty  = contacts.Party{TenantID: "family-a", ChildID: "lea"}
	timParty  = contacts.Party{TenantID: "family-a", ChildID: "tim"}
	miaParty  = contacts.Party{TenantID: "family-b", ChildID: "mia"}
	noahParty = contacts.Party{TenantID: "family-c", ChildID: "lea"} // same child id as Lea
)

type world struct {
	dir      string
	path     string
	families *familystore.Store
	contacts *contacts.Store
	pairing  *devicepairing.Store
	store    *Store
	clock    *clock
	lea, tim Principal
	mia      Principal
	noah     Principal
	chat     string // conversation between Lea and Mia
}

func guardianOf(p contacts.Party) string {
	return map[string]string{"family-a": "anna", "family-b": "bruno", "family-c": "carla"}[p.TenantID]
}

func (w *world) connect(t *testing.T, a, b contacts.Party) string {
	t.Helper()
	code, err := w.contacts.Invite(guardianOf(a), a.TenantID, a.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := w.contacts.Redeem(guardianOf(b), b.TenantID, b.ChildID, code)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.contacts.Confirm(guardianOf(a), a.TenantID, a.ChildID, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (w *world) device(t *testing.T, p contacts.Party) Principal {
	t.Helper()
	id, _ := pairingtest.Pair(t, w.pairing, guardianOf(p), p.TenantID, p.ChildID)
	return Principal{Party: p, DeviceID: id}
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{dir: t.TempDir(), clock: &clock{t: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}}
	var err error
	if w.families, err = familystore.Open(filepath.Join(w.dir, "families.json")); err != nil {
		t.Fatal(err)
	}
	for p, name := range map[contacts.Party]string{leaParty: "Lea", timParty: "Tim", miaParty: "Mia", noahParty: "Noah"} {
		grant := authz.GuardianGrant{GuardianID: guardianOf(p), ChildID: p.ChildID, TenantID: p.TenantID, Active: true, CanManageDevices: true, CanApproveContacts: true}
		if err := w.families.Bootstrap(familydashboard.Child{ID: p.ChildID, TenantID: p.TenantID, DisplayName: name}, grant); err != nil {
			t.Fatal(err)
		}
	}
	if w.contacts, err = contacts.Open(filepath.Join(w.dir, "contacts.json"), w.families, w.clock.now); err != nil {
		t.Fatal(err)
	}
	if w.pairing, err = devicepairing.Open(filepath.Join(w.dir, "pairing.json"), w.families, w.clock.now); err != nil {
		t.Fatal(err)
	}
	w.path = filepath.Join(w.dir, "messages.log")
	w.reopen(t)
	w.lea, w.tim, w.mia, w.noah = w.device(t, leaParty), w.device(t, timParty), w.device(t, miaParty), w.device(t, noahParty)
	w.chat = w.connect(t, leaParty, miaParty)
	return w
}

func (w *world) reopen(t *testing.T) {
	t.Helper()
	if w.store != nil {
		if err := w.store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Open(w.path, w.contacts, w.pairing, w.families, w.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	w.store = s
	t.Cleanup(func() { _ = s.Close() })
}

var counter atomic.Uint64

// cid returns a fresh, well-formed client id.
func cid() string { return fmt.Sprintf("client-id-%012d", counter.Add(1)) }

func (w *world) send(t *testing.T, p Principal, conversation, body string) Message {
	t.Helper()
	m, dup, err := w.store.Send(p, conversation, cid(), body)
	if err != nil || dup {
		t.Fatalf("send %q: %v (duplicate=%v)", body, err, dup)
	}
	return m
}

func bodies(ms []Message) string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Body)
	}
	return strings.Join(out, "|")
}

func TestApprovedChildrenExchangeOrderedMessages(t *testing.T) {
	w := newWorld(t)
	first := w.send(t, w.lea, w.chat, "Hoi Mia!")
	w.clock.advance(time.Second)
	second := w.send(t, w.mia, w.chat, "Hoi Lea, wie gehts?")
	third := w.send(t, w.lea, w.chat, "Guet!")
	if first.Seq != 1 || second.Seq != 2 || third.Seq != 3 || first.From != leaParty || second.From != miaParty || !second.At.After(first.At) {
		t.Fatalf("unexpected messages: %+v %+v %+v", first, second, third)
	}
	for _, reader := range []Principal{w.lea, w.mia} {
		got, err := w.store.History(reader, w.chat, 0, 50)
		if err != nil {
			t.Fatal(err)
		}
		if bodies(got) != "Hoi Mia!|Hoi Lea, wie gehts?|Guet!" || got[0].Seq != 1 || got[2].Seq != 3 {
			t.Fatalf("%+v reads %q", reader.Party, bodies(got))
		}
	}
	after, err := w.store.History(w.mia, w.chat, 2, 50)
	if err != nil || bodies(after) != "Guet!" {
		t.Fatalf("cursor: %q %v", bodies(after), err)
	}
}

// Every send and every read is authorized again. Nothing below may store or
// reveal a message.
func TestSendAndReadAreDeniedWithoutCurrentAuthorization(t *testing.T) {
	w := newWorld(t)
	w.send(t, w.lea, w.chat, "geheim")
	pendingCode, err := w.contacts.Invite("anna", "family-a", "tim")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := w.contacts.Redeem("bruno", "family-b", "mia", pendingCode)
	if err != nil {
		t.Fatal(err)
	}
	other := w.connect(t, noahParty, timParty) // unrelated active conversation
	revoked := w.device(t, leaParty)
	if err := w.pairing.Revoke("anna", "family-a", "lea", revoked.DeviceID); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		who          Principal
		conversation string
	}{
		"outsider with own approved device":       {w.noah, w.chat},
		"sibling of a participant":                {w.tim, w.chat},
		"participant in someone else's chat":      {w.lea, other},
		"pending, unconfirmed contact (invitee)":  {w.mia, pending},
		"pending, unconfirmed contact (inviter)":  {w.tim, pending},
		"unknown conversation":                    {w.lea, strings.Repeat("0", 32)},
		"empty conversation":                      {w.lea, ""},
		"participant with another child's device": {Principal{Party: leaParty, DeviceID: w.mia.DeviceID}, w.chat},
		"participant with a sibling's device":     {Principal{Party: leaParty, DeviceID: w.tim.DeviceID}, w.chat},
		"participant with unknown device":         {Principal{Party: leaParty, DeviceID: "unknown"}, w.chat},
		"participant without device":              {Principal{Party: leaParty}, w.chat},
		"participant with revoked device":         {revoked, w.chat},
		"own device, forged tenant":               {Principal{Party: contacts.Party{TenantID: "family-b", ChildID: "lea"}, DeviceID: w.lea.DeviceID}, w.chat},
		"own device, forged child":                {Principal{Party: miaParty, DeviceID: w.lea.DeviceID}, w.chat},
		"zero principal":                          {Principal{}, w.chat},
	}
	for name, c := range cases {
		if _, _, err := w.store.Send(c.who, c.conversation, cid(), "darf nicht"); !errors.Is(err, ErrForbidden) {
			t.Errorf("send as %s: %v", name, err)
		}
		if got, err := w.store.History(c.who, c.conversation, 0, 50); !errors.Is(err, ErrForbidden) || len(got) != 0 {
			t.Errorf("read as %s: %v %q", name, err, bodies(got))
		}
		if got, err := w.store.Recent(c.who, c.conversation, 50); !errors.Is(err, ErrForbidden) || len(got) != 0 {
			t.Errorf("recent as %s: %v", name, err)
		}
	}
	got, err := w.store.History(w.mia, w.chat, 0, 50)
	if err != nil || bodies(got) != "geheim" {
		t.Fatalf("denied attempts changed the conversation: %q %v", bodies(got), err)
	}
	raw, _ := os.ReadFile(w.path)
	if strings.Contains(string(raw), "darf nicht") {
		t.Fatal("a denied message reached the disk")
	}
}

func TestAccessEndsWithContactDeviceOrProfile(t *testing.T) {
	t.Run("contact removed by the other family", func(t *testing.T) {
		w := newWorld(t)
		w.send(t, w.lea, w.chat, "vorher")
		if err := w.contacts.Remove("bruno", "family-b", "mia", w.chat); err != nil {
			t.Fatal(err)
		}
		for _, p := range []Principal{w.lea, w.mia} {
			if _, _, err := w.store.Send(p, w.chat, cid(), "nachher"); !errors.Is(err, ErrForbidden) {
				t.Fatalf("send after removal: %v", err)
			}
			if got, err := w.store.History(p, w.chat, 0, 50); !errors.Is(err, ErrForbidden) || len(got) != 0 {
				t.Fatalf("history after removal: %v", err)
			}
		}
		// Reconnecting creates a new conversation without the old messages.
		again := w.connect(t, leaParty, miaParty)
		if got, err := w.store.History(w.lea, again, 0, 50); err != nil || len(got) != 0 {
			t.Fatalf("old messages resurfaced: %q %v", bodies(got), err)
		}
	})
	t.Run("sender device revoked", func(t *testing.T) {
		w := newWorld(t)
		spare := w.device(t, leaParty)
		if err := w.pairing.Revoke("anna", "family-a", "lea", w.lea.DeviceID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := w.store.Send(w.lea, w.chat, cid(), "x"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("revoked device sent: %v", err)
		}
		w.send(t, spare, w.chat, "vom anderen Gerät")
		if got, err := w.store.History(w.lea, w.chat, 0, 50); !errors.Is(err, ErrForbidden) || len(got) != 0 {
			t.Fatalf("revoked device read: %v", err)
		}
	})
}

type vanishing struct {
	Children
	gone map[contacts.Party]bool
}

func (v vanishing) ChildName(tenantID, childID string) (string, bool) {
	if v.gone[contacts.Party{TenantID: tenantID, ChildID: childID}] {
		return "", false
	}
	return v.Children.ChildName(tenantID, childID)
}

func TestAccessEndsWhenEitherProfileIsRemoved(t *testing.T) {
	w := newWorld(t)
	w.send(t, w.lea, w.chat, "vorher")
	_ = w.store.Close()
	gone := map[contacts.Party]bool{}
	s, err := Open(w.path, w.contacts, w.pairing, vanishing{Children: w.families, gone: gone}, w.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, missing := range []contacts.Party{miaParty, leaParty} {
		gone[missing] = true
		if _, _, err := s.Send(w.lea, w.chat, cid(), "x"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("send with %+v removed: %v", missing, err)
		}
		if _, err := s.History(w.lea, w.chat, 0, 50); !errors.Is(err, ErrForbidden) {
			t.Fatalf("read with %+v removed: %v", missing, err)
		}
		delete(gone, missing)
	}
}

func TestRetriesAreIdempotent(t *testing.T) {
	w := newWorld(t)
	id := cid()
	first, dup, err := w.store.Send(w.lea, w.chat, id, "nur einmal")
	if err != nil || dup {
		t.Fatal(err)
	}
	w.send(t, w.mia, w.chat, "dazwischen")
	retry, dup, err := w.store.Send(w.lea, w.chat, id, "nur einmal")
	if err != nil || !dup || retry.Seq != first.Seq || !retry.At.Equal(first.At) {
		t.Fatalf("retry: %+v dup=%v err=%v", retry, dup, err)
	}
	// A retry from the child's second device is the same message too.
	spare := w.device(t, leaParty)
	if again, dup, err := w.store.Send(spare, w.chat, id, "nur einmal"); err != nil || !dup || again.Seq != first.Seq {
		t.Fatalf("retry from second device: %+v %v %v", again, dup, err)
	}
	if _, _, err := w.store.Send(w.lea, w.chat, id, "anderer Text"); !errors.Is(err, ErrConflict) {
		t.Fatalf("client id reused for another text: %v", err)
	}
	// The same client id from the other child or in another chat is unrelated.
	if m, dup, err := w.store.Send(w.mia, w.chat, id, "nur einmal"); err != nil || dup || m.Seq == first.Seq {
		t.Fatalf("other sender: %+v %v %v", m, dup, err)
	}
	second := w.connect(t, leaParty, noahParty)
	if m, dup, err := w.store.Send(w.lea, second, id, "nur einmal"); err != nil || dup || m.Seq != 1 {
		t.Fatalf("other conversation: %+v %v %v", m, dup, err)
	}
	w.reopen(t)
	if again, dup, err := w.store.Send(w.lea, w.chat, id, "nur einmal"); err != nil || !dup || again.Seq != first.Seq {
		t.Fatalf("retry after restart: %+v %v %v", again, dup, err)
	}
	got, _ := w.store.History(w.lea, w.chat, 0, 50)
	if bodies(got) != "nur einmal|dazwischen|nur einmal" {
		t.Fatalf("duplicates stored: %q", bodies(got))
	}
	// A retry is still authorized at the time of the retry.
	if err := w.contacts.Remove("anna", "family-a", "lea", w.chat); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.store.Send(w.lea, w.chat, id, "nur einmal"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("retry after contact removal: %v", err)
	}
}

func TestBodiesAndClientIDsAreValidated(t *testing.T) {
	w := newWorld(t)
	for name, body := range map[string]string{
		"empty":           "",
		"only whitespace": " \n\t ",
		"too long":        strings.Repeat("ä", MaxBodyRunes+1),
		"invalid utf-8":   "abc\xff",
		"NUL":             "a\x00b",
		"escape char":     "a\x1bb",
		"bidi override":   "abc‮def",
	} {
		if _, _, err := w.store.Send(w.lea, w.chat, cid(), body); !errors.Is(err, ErrInvalid) {
			t.Errorf("body %s: %v", name, err)
		}
	}
	for _, id := range []string{"", "short", strings.Repeat("a", 65), "has space 0123456789", "../../../etc/passwd", "client-id-<script>"} {
		if _, _, err := w.store.Send(w.lea, w.chat, id, "ok"); !errors.Is(err, ErrInvalid) {
			t.Errorf("client id %q: %v", id, err)
		}
	}
	longest := strings.Repeat("ä", MaxBodyRunes)
	if m := w.send(t, w.lea, w.chat, longest); m.Body != longest {
		t.Fatal("longest allowed body altered")
	}
	// Content is stored as typed; escaping is the renderer's job.
	markup := `<script>alert(1)</script> & "x" 😀`
	if m := w.send(t, w.lea, w.chat, markup); m.Body != markup {
		t.Fatalf("markup altered: %q", m.Body)
	}
	if m := w.send(t, w.lea, w.chat, "  Zeile 1\r\nZeile 2\n\n"); m.Body != "Zeile 1\nZeile 2" {
		t.Fatalf("not normalized: %q", m.Body)
	}
	got, _ := w.store.History(w.mia, w.chat, 0, 50)
	if len(got) != 3 {
		t.Fatalf("invalid messages were stored: %d", len(got))
	}
}

func TestMessagesSurviveRestartAndFileIsPrivate(t *testing.T) {
	w := newWorld(t)
	w.send(t, w.lea, w.chat, "eins")
	w.send(t, w.mia, w.chat, "zwei")
	info, err := os.Stat(w.path)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatalf("log not private: %v %v", info, err)
	}
	w.reopen(t)
	got, err := w.store.History(w.lea, w.chat, 0, 50)
	if err != nil || bodies(got) != "eins|zwei" || got[1].From != miaParty {
		t.Fatalf("after restart: %q %v", bodies(got), err)
	}
	if m := w.send(t, w.lea, w.chat, "drei"); m.Seq != 3 {
		t.Fatalf("sequence did not continue: %d", m.Seq)
	}
}

// A message is acknowledged only after it is durable. If the disk write
// fails, the caller gets an error, nothing is visible, and a retry succeeds
// exactly once.
func TestFailedDiskWriteIsNotAcknowledged(t *testing.T) {
	w := newWorld(t)
	w.send(t, w.lea, w.chat, "vorher")
	before, _ := os.ReadFile(w.path)
	id := cid()
	realSync := w.store.syncFile
	failures := map[string]func(){
		"write": func() { w.store.failWrite = true },
		"sync": func() {
			w.store.syncFile = func(f *os.File) error {
				w.store.syncFile = realSync
				return errors.New("disk says no")
			}
		},
	}
	for step, arm := range failures {
		arm()
		if _, _, err := w.store.Send(w.lea, w.chat, id, "unsicher"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s failure acknowledged: %v", step, err)
		}
		if got, _ := w.store.History(w.mia, w.chat, 0, 50); bodies(got) != "vorher" {
			t.Fatalf("unacknowledged message visible after %s failure: %q", step, bodies(got))
		}
		if after, _ := os.ReadFile(w.path); string(after) != string(before) {
			t.Fatalf("log changed after %s failure", step)
		}
	}
	m, dup, err := w.store.Send(w.lea, w.chat, id, "unsicher")
	if err != nil || dup || m.Seq != 2 {
		t.Fatalf("retry after failure: %+v %v %v", m, dup, err)
	}
	w.reopen(t)
	if got, _ := w.store.History(w.mia, w.chat, 0, 50); bodies(got) != "vorher|unsicher" {
		t.Fatalf("after restart: %q", bodies(got))
	}
}

func TestOpenRecoversTornTailAndRejectsCorruption(t *testing.T) {
	w := newWorld(t)
	w.send(t, w.lea, w.chat, "eins")
	w.send(t, w.mia, w.chat, "zwei")
	_ = w.store.Close()
	good, err := os.ReadFile(w.path)
	if err != nil {
		t.Fatal(err)
	}
	// A crash mid-append leaves a partial last line without newline. It was
	// never acknowledged, so it is dropped.
	if err := os.WriteFile(w.path, append(append([]byte{}, good...), []byte(`{"conversation":"`+w.chat+`","seq":3,"bo`)...), 0600); err != nil {
		t.Fatal(err)
	}
	w.store = nil
	w.reopen(t)
	if got, _ := w.store.History(w.lea, w.chat, 0, 50); bodies(got) != "eins|zwei" {
		t.Fatalf("after torn tail: %q", bodies(got))
	}
	if m := w.send(t, w.lea, w.chat, "drei"); m.Seq != 3 {
		t.Fatalf("append after recovery: %+v", m)
	}
	_ = w.store.Close()
	w.reopen(t)
	if got, _ := w.store.History(w.lea, w.chat, 0, 50); bodies(got) != "eins|zwei|drei" {
		t.Fatalf("log unreadable after recovery: %q", bodies(got))
	}
	_ = w.store.Close()
	lines := strings.SplitAfter(string(good), "\n")
	bad := map[string]string{
		"corrupt complete line": lines[0] + "{not json}\n",
		"unknown field":         lines[0] + strings.Replace(lines[1], `{"`, `{"admin":true,"`, 1),
		"sequence goes back":    lines[1] + lines[0],
		"duplicate sequence":    lines[0] + lines[0],
		"missing sender":        strings.Replace(lines[0], `"tenant_id":"family-a"`, `"tenant_id":""`, 1),
		"empty body":            strings.Replace(lines[0], `"body":"eins"`, `"body":""`, 1),
	}
	for name, content := range bad {
		p := filepath.Join(w.dir, "bad.log")
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if s, err := Open(p, w.contacts, w.pairing, w.families, w.clock.now); err == nil {
			_ = s.Close()
			t.Errorf("%s: store opened", name)
		}
	}
	shared := filepath.Join(w.dir, "shared.log")
	if err := os.WriteFile(shared, good, 0644); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(shared, w.contacts, w.pairing, w.families, w.clock.now); err == nil {
		_ = s.Close()
		t.Fatal("world-readable log opened")
	}
	for name, open := range map[string]func() (*Store, error){
		"no path":     func() (*Store, error) { return Open("", w.contacts, w.pairing, w.families, w.clock.now) },
		"no contacts": func() (*Store, error) { return Open(w.path, nil, w.pairing, w.families, w.clock.now) },
		"no devices":  func() (*Store, error) { return Open(w.path, w.contacts, nil, w.families, w.clock.now) },
		"no children": func() (*Store, error) { return Open(w.path, w.contacts, w.pairing, nil, w.clock.now) },
		"no clock":    func() (*Store, error) { return Open(w.path, w.contacts, w.pairing, w.families, nil) },
	} {
		if s, err := open(); err == nil {
			_ = s.Close()
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSendRateIsLimitedPerSenderAndConversation(t *testing.T) {
	w := newWorld(t)
	for i := 0; i < MaxPerMinute; i++ {
		w.send(t, w.lea, w.chat, fmt.Sprintf("n%d", i))
	}
	if _, _, err := w.store.Send(w.lea, w.chat, cid(), "zu viel"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("limit not enforced: %v", err)
	}
	w.send(t, w.mia, w.chat, "ich darf noch")
	second := w.connect(t, leaParty, noahParty)
	w.send(t, w.lea, second, "anderer Chat")
	w.clock.advance(time.Minute)
	w.send(t, w.lea, w.chat, "wieder erlaubt")
}

func TestHistoryPagingIsBounded(t *testing.T) {
	w := newWorld(t)
	for i := 1; i <= 12; i++ {
		w.send(t, w.lea, w.chat, fmt.Sprintf("m%d", i))
		w.clock.advance(10 * time.Second)
	}
	page, err := w.store.History(w.mia, w.chat, 3, 4)
	if err != nil || bodies(page) != "m4|m5|m6|m7" {
		t.Fatalf("page: %q %v", bodies(page), err)
	}
	recent, err := w.store.Recent(w.mia, w.chat, 3)
	if err != nil || bodies(recent) != "m10|m11|m12" {
		t.Fatalf("recent: %q %v", bodies(recent), err)
	}
	for _, limit := range []int{0, -5} {
		if got, err := w.store.History(w.mia, w.chat, 0, limit); err != nil || len(got) != 12 {
			t.Fatalf("limit %d: %d %v", limit, len(got), err)
		}
	}
	if got, _ := w.store.History(w.mia, w.chat, 0, MaxPage+500); len(got) > MaxPage {
		t.Fatalf("page larger than MaxPage: %d", len(got))
	}
	if got, err := w.store.History(w.mia, w.chat, 999, 10); err != nil || len(got) != 0 {
		t.Fatalf("cursor beyond end: %d %v", len(got), err)
	}
}

func TestRetentionAndPurgeOfEndedConversations(t *testing.T) {
	w := newWorld(t)
	w.send(t, w.lea, w.chat, "alt und geheim")
	w.clock.advance(Retention - time.Hour)
	w.send(t, w.mia, w.chat, "noch jung")
	w.clock.advance(2 * time.Hour)
	if err := w.store.Compact(); err != nil {
		t.Fatal(err)
	}
	got, err := w.store.History(w.lea, w.chat, 0, 50)
	if err != nil || bodies(got) != "noch jung" || got[0].Seq != 2 {
		t.Fatalf("after retention: %q %v", bodies(got), err)
	}
	raw, _ := os.ReadFile(w.path)
	if strings.Contains(string(raw), "alt und geheim") {
		t.Fatal("expired message still on disk")
	}
	// When everything has expired, numbering must not restart: clients hold
	// cursors, and a reused sequence number would hide new messages.
	w.clock.advance(Retention + time.Hour)
	if err := w.store.Compact(); err != nil {
		t.Fatal(err)
	}
	w.reopen(t)
	if m := w.send(t, w.lea, w.chat, "neu"); m.Seq != 3 {
		t.Fatalf("sequence restarted at %d", m.Seq)
	}
	if got, _ := w.store.History(w.mia, w.chat, 2, 50); bodies(got) != "neu" {
		t.Fatalf("cursor lost a message: %q", bodies(got))
	}
	// Ending a contact purges its messages from disk at the next compaction.
	w.send(t, w.lea, w.chat, "wird entfernt")
	if err := w.contacts.Remove("anna", "family-a", "lea", w.chat); err != nil {
		t.Fatal(err)
	}
	if err := w.store.Compact(); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(w.path)
	if strings.Contains(string(raw), "wird entfernt") || strings.Contains(string(raw), w.chat) {
		t.Fatal("messages of an ended contact remain on disk")
	}
	info, _ := os.Stat(w.path)
	if info.Mode().Perm()&0077 != 0 {
		t.Fatal("compaction made the log readable by others")
	}
}

func TestOldestMessagesAreDroppedBeyondTheConversationCap(t *testing.T) {
	w := newWorld(t)
	const limit = 40
	w.store.maxPerConversation = limit
	total := limit + 30
	for i := 1; i <= total; i++ {
		w.send(t, w.lea, w.chat, fmt.Sprintf("m%d", i))
		w.clock.advance(4 * time.Second) // stay under the rate limit
	}
	if err := w.store.Compact(); err != nil {
		t.Fatal(err)
	}
	recent, err := w.store.Recent(w.mia, w.chat, MaxPage)
	if err != nil || recent[len(recent)-1].Body != fmt.Sprintf("m%d", total) {
		t.Fatalf("newest message lost: %v", err)
	}
	all := 0
	for after := uint64(0); ; {
		page, err := w.store.History(w.mia, w.chat, after, MaxPage)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if after == 0 && page[0].Body != "m31" {
			t.Fatalf("oldest retained message is %q", page[0].Body)
		}
		all += len(page)
		after = page[len(page)-1].Seq
	}
	if all != limit {
		t.Fatalf("retained %d messages", all)
	}
}

func TestConcurrentSendsGetUniqueContiguousSequences(t *testing.T) {
	w := newWorld(t)
	const perSender = 15
	var wg sync.WaitGroup
	for _, p := range []Principal{w.lea, w.mia} {
		for i := 0; i < perSender; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, _, err := w.store.Send(p, w.chat, cid(), fmt.Sprintf("%s-%d", p.Party.ChildID, i)); err != nil {
					t.Error(err)
				}
			}()
		}
	}
	wg.Wait()
	w.reopen(t)
	got, err := w.store.History(w.lea, w.chat, 0, MaxPage)
	if err != nil || len(got) != 2*perSender {
		t.Fatalf("stored %d messages: %v", len(got), err)
	}
	for i, m := range got {
		if m.Seq != uint64(i+1) {
			t.Fatalf("sequence gap at %d: %d", i, m.Seq)
		}
	}
}

// Once a revocation or removal has returned, no send that starts afterwards
// may succeed, however many sends are in flight.
func TestNoSendSucceedsAfterRevocationOrRemovalReturned(t *testing.T) {
	for name, end := range map[string]func(w *world) error{
		"device revoked":  func(w *world) error { return w.pairing.Revoke("anna", "family-a", "lea", w.lea.DeviceID) },
		"contact removed": func(w *world) error { return w.contacts.Remove("bruno", "family-b", "mia", w.chat) },
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			var ended atomic.Bool
			var late atomic.Int64
			var wg sync.WaitGroup
			for g := 0; g < 4; g++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < 5; i++ {
						startedAfter := ended.Load()
						_, _, err := w.store.Send(w.lea, w.chat, cid(), "x")
						if startedAfter && err == nil {
							late.Add(1)
						}
					}
				}()
			}
			if err := end(w); err != nil {
				t.Error(err)
			}
			ended.Store(true)
			wg.Wait()
			if late.Load() != 0 {
				t.Fatalf("%d sends succeeded after the action returned", late.Load())
			}
			if _, _, err := w.store.Send(w.lea, w.chat, cid(), "x"); !errors.Is(err, ErrForbidden) {
				t.Fatalf("send afterwards: %v", err)
			}
		})
	}
}

// Every acknowledged message has been forced to stable storage first, and the
// bytes were already written when that happened.
func TestEachAcknowledgedMessageWasSyncedAfterBeingWritten(t *testing.T) {
	w := newWorld(t)
	realSync := w.store.syncFile
	syncs := 0
	var sizeAtSync int64
	w.store.syncFile = func(f *os.File) error {
		syncs++
		if info, err := f.Stat(); err == nil {
			sizeAtSync = info.Size()
		}
		return realSync(f)
	}
	for i := 1; i <= 3; i++ {
		w.send(t, w.lea, w.chat, fmt.Sprintf("m%d", i))
		info, err := os.Stat(w.path)
		if err != nil {
			t.Fatal(err)
		}
		if syncs != i || sizeAtSync != info.Size() {
			t.Fatalf("message %d: %d syncs, synced at %d bytes of %d", i, syncs, sizeAtSync, info.Size())
		}
	}
	// A duplicate writes nothing and therefore needs no sync.
	id := cid()
	if _, _, err := w.store.Send(w.lea, w.chat, id, "einmal"); err != nil {
		t.Fatal(err)
	}
	before := syncs
	if _, dup, err := w.store.Send(w.lea, w.chat, id, "einmal"); err != nil || !dup || syncs != before {
		t.Fatalf("duplicate: dup=%v err=%v syncs %d -> %d", dup, err, before, syncs)
	}
}

func TestPagesNeverExceedMaxPage(t *testing.T) {
	w := newWorld(t)
	for i := 0; i < MaxPage+5; i++ {
		w.send(t, w.lea, w.chat, "m")
		w.clock.advance(4 * time.Second)
	}
	for _, limit := range []int{0, -1, MaxPage, MaxPage + 500} {
		if got, err := w.store.History(w.mia, w.chat, 0, limit); err != nil || len(got) != MaxPage {
			t.Fatalf("history limit %d: %d %v", limit, len(got), err)
		}
		if got, err := w.store.Recent(w.mia, w.chat, limit); err != nil || len(got) != MaxPage || got[len(got)-1].Seq != MaxPage+5 {
			t.Fatalf("recent limit %d: %d %v", limit, len(got), err)
		}
	}
}

func TestCanReadFollowsTheSameCurrentStateAsReading(t *testing.T) {
	w := newWorld(t)
	// Lea and Mia may read their conversation; Tim and Noah may not.
	for p, want := range map[*Principal]bool{&w.lea: true, &w.mia: true, &w.tim: false, &w.noah: false} {
		if got := w.store.CanRead(*p, w.chat); got != want {
			t.Errorf("%s: CanRead=%v", p.Party.ChildID, got)
		}
	}
	for _, p := range []Principal{{}, {Party: leaParty}, {Party: leaParty, DeviceID: "unknown-device"}} {
		if w.store.CanRead(p, w.chat) {
			t.Errorf("principal %+v may read", p)
		}
	}
	if w.store.CanRead(w.lea, "") || w.store.CanRead(w.lea, "0123456789abcdef0123456789abcdef") {
		t.Error("an unknown conversation is readable")
	}
	// A blocked contact may not be read by the blocked side either, exactly
	// as History says.
	if err := w.contacts.Block(miaParty, w.chat); err != nil {
		t.Fatal(err)
	}
	for _, p := range []Principal{w.lea, w.mia} {
		_, err := w.store.History(p, w.chat, 0, 1)
		if w.store.CanRead(p, w.chat) != (err == nil) {
			t.Errorf("%s: CanRead disagrees with History (%v)", p.Party.ChildID, err)
		}
	}
	if err := w.pairing.Revoke("anna", "family-a", "lea", w.lea.DeviceID); err != nil {
		t.Fatal(err)
	}
	if w.store.CanRead(w.lea, w.chat) {
		t.Error("a revoked device may read")
	}
}
