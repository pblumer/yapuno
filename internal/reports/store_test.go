package reports

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/messages"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var (
	lea = contacts.Party{TenantID: "family-a", ChildID: "lea"}
	tim = contacts.Party{TenantID: "family-a", ChildID: "tim"}
	mia = contacts.Party{TenantID: "family-b", ChildID: "mia"}
)

type world struct {
	t        *testing.T
	clock    *clock
	families *familystore.Store
	contacts *contacts.Store
	pairing  *devicepairing.Store
	messages *messages.Store
	reports  *Store
	path     string
	chat     string
	leaP     messages.Principal
	miaP     messages.Principal
	n        int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	w := &world{t: t, clock: &clock{t: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}, path: filepath.Join(dir, "reports.json")}
	var err error
	if w.families, err = familystore.Open(filepath.Join(dir, "families.json")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		party    contacts.Party
		name     string
		guardian string
	}{{lea, "Lea", "anna"}, {tim, "Tim", "anna"}, {mia, "Mia", "bruno"}} {
		grant := authz.GuardianGrant{GuardianID: c.guardian, ChildID: c.party.ChildID, TenantID: c.party.TenantID, Active: true, CanManageDevices: true, CanApproveContacts: true}
		if err := w.families.Bootstrap(familydashboard.Child{ID: c.party.ChildID, TenantID: c.party.TenantID, DisplayName: c.name}, grant); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.families.BootstrapGuardian(authz.GuardianGrant{GuardianID: "albert", ChildID: "lea", TenantID: "family-a", Active: true, CanManageDevices: true, CanApproveContacts: true}); err != nil {
		t.Fatal(err)
	}
	if w.contacts, err = contacts.Open(filepath.Join(dir, "contacts.json"), w.families, w.clock.now); err != nil {
		t.Fatal(err)
	}
	if w.pairing, err = devicepairing.Open(filepath.Join(dir, "pairing.json"), w.families, w.clock.now); err != nil {
		t.Fatal(err)
	}
	if w.messages, err = messages.Open(filepath.Join(dir, "messages.log"), w.contacts, w.pairing, w.families, w.clock.now); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.messages.Close() })
	w.chat = w.connect("anna", lea, "bruno", mia)
	w.leaP, w.miaP = w.device("anna", lea), w.device("bruno", mia)
	if w.reports, err = Open(w.path, w.messages, w.families, w.clock.now); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *world) connect(g1 string, a contacts.Party, g2 string, b contacts.Party) string {
	w.t.Helper()
	code, err := w.contacts.Invite(g1, a.TenantID, a.ChildID)
	if err != nil {
		w.t.Fatal(err)
	}
	id, err := w.contacts.Redeem(g2, b.TenantID, b.ChildID, code)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.contacts.Confirm(g1, a.TenantID, a.ChildID, id); err != nil {
		w.t.Fatal(err)
	}
	return id
}

func (w *world) device(guardian string, p contacts.Party) messages.Principal {
	w.t.Helper()
	id, _ := pairingtest.Pair(w.t, w.pairing, guardian, p.TenantID, p.ChildID)
	return messages.Principal{Party: p, DeviceID: id}
}

// say sends one message and returns its sequence number.
func (w *world) say(p messages.Principal, chat, body string) uint64 {
	w.t.Helper()
	w.n++
	m, _, err := w.messages.Send(p, chat, "report-test-client-"+strings.Repeat("0", 4)+string(rune('a'+w.n%26))+string(rune('a'+w.n/26)), body)
	if err != nil {
		w.t.Fatal(err)
	}
	return m.Seq
}

func TestChildReportsExactlyOneMessageToOwnGuardians(t *testing.T) {
	w := newWorld(t)
	w.say(w.leaP, w.chat, "Hoi Mia")
	bad := w.say(w.miaP, w.chat, "Du bisch e Löli")
	w.say(w.miaP, w.chat, "Geheim, nicht gemeldet")
	w.clock.advance(time.Minute)
	created, err := w.reports.File(w.leaP, w.chat, bad)
	if err != nil || !created {
		t.Fatalf("report: %v %v", created, err)
	}
	for _, guardian := range []string{"anna", "albert"} {
		list, err := w.reports.ForChild(guardian, lea.TenantID, lea.ChildID)
		if err != nil || len(list) != 1 {
			t.Fatalf("%s: %v %v", guardian, list, err)
		}
		r := list[0]
		if r.From != "Mia" || r.Body != "Du bisch e Löli" || len(r.ID) != 32 || !r.ReportedAt.Equal(w.clock.now()) || !r.SentAt.Equal(w.clock.now().Add(-time.Minute)) {
			t.Fatalf("report content: %+v", r)
		}
		if w.reports.Count(guardian, lea.TenantID, lea.ChildID) != 1 {
			t.Fatal("count")
		}
	}
	// Nobody else sees it: not the reported child's family, not the sibling's page.
	for name, c := range map[string][3]string{
		"other family":          {"bruno", mia.TenantID, mia.ChildID},
		"other as reporter":     {"bruno", lea.TenantID, lea.ChildID},
		"own guardian, sibling": {"anna", tim.TenantID, tim.ChildID},
		"unknown guardian":      {"nobody", lea.TenantID, lea.ChildID},
		"empty guardian":        {"", lea.TenantID, lea.ChildID},
		"wrong tenant":          {"anna", "family-b", lea.ChildID},
	} {
		list, err := w.reports.ForChild(c[0], c[1], c[2])
		if len(list) != 0 || w.reports.Count(c[0], c[1], c[2]) != 0 {
			t.Errorf("%s sees reports: %v", name, list)
		}
		// Guardians asking about their own child get an empty list, everybody else a refusal.
		if name != "own guardian, sibling" && name != "other family" && !errors.Is(err, ErrForbidden) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The file holds the reported text and nothing else of the conversation.
	raw, _ := os.ReadFile(w.path)
	if !strings.Contains(string(raw), "Löli") || strings.Contains(string(raw), "Geheim") || strings.Contains(string(raw), "Hoi Mia") {
		t.Fatalf("stored document: %s", raw)
	}
	if info, _ := os.Stat(w.path); info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v", info.Mode().Perm())
	}
}

func TestReportIsOnlyPossibleForAReadableMessageOfTheOtherChild(t *testing.T) {
	w := newWorld(t)
	own := w.say(w.leaP, w.chat, "Meine eigene")
	theirs := w.say(w.miaP, w.chat, "Von Mia")
	timP := w.device("anna", tim)
	otherChat := w.connect("anna", tim, "bruno", mia)
	inOther := w.say(w.miaP, otherChat, "An Tim")
	cases := map[string]struct {
		p    messages.Principal
		chat string
		seq  uint64
		want error
	}{
		"own message":             {w.leaP, w.chat, own, ErrInvalid},
		"unknown sequence":        {w.leaP, w.chat, 99, ErrNotFound},
		"sequence zero":           {w.leaP, w.chat, 0, ErrNotFound},
		"conversation of sibling": {w.leaP, otherChat, inOther, ErrForbidden},
		"outsider":                {timP, w.chat, theirs, ErrForbidden},
		"unknown conversation":    {w.leaP, strings.Repeat("0", 32), 1, ErrForbidden},
		"unknown device":          {messages.Principal{Party: lea, DeviceID: "AAAAAAAAAAAAAAAAAAAAAA"}, w.chat, theirs, ErrForbidden},
		"no device":               {messages.Principal{Party: lea}, w.chat, theirs, ErrForbidden},
	}
	for name, c := range cases {
		if created, err := w.reports.File(c.p, c.chat, c.seq); created || !errors.Is(err, c.want) {
			t.Errorf("%s: %v %v", name, created, err)
		}
	}
	if w.reports.Count("anna", lea.TenantID, lea.ChildID) != 0 || w.reports.Count("anna", tim.TenantID, tim.ChildID) != 0 {
		t.Fatal("a refused report was stored")
	}
	if _, err := os.Stat(w.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused report touched the file")
	}
	// A locked device cannot report.
	if err := w.pairing.Revoke("anna", lea.TenantID, lea.ChildID, w.leaP.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.reports.File(w.leaP, w.chat, theirs); !errors.Is(err, ErrForbidden) {
		t.Fatalf("locked device reported: %v", err)
	}
	// After a block nothing in the conversation can be read, so nothing can be reported.
	if err := w.contacts.Block(tim, otherChat); err != nil {
		t.Fatal(err)
	}
	if _, err := w.reports.File(timP, otherChat, inOther); !errors.Is(err, ErrForbidden) {
		t.Fatalf("report in a blocked conversation: %v", err)
	}
}

func TestReportingTwiceKeepsOneReportAndListsWhatWasReported(t *testing.T) {
	w := newWorld(t)
	first := w.say(w.miaP, w.chat, "eins")
	second := w.say(w.miaP, w.chat, "zwei")
	for i, want := range []bool{true, false, false} {
		if created, err := w.reports.File(w.leaP, w.chat, first); err != nil || created != want {
			t.Fatalf("attempt %d: %v %v", i, created, err)
		}
	}
	if _, err := w.reports.File(w.leaP, w.chat, second); err != nil {
		t.Fatal(err)
	}
	if got := w.reports.Count("anna", lea.TenantID, lea.ChildID); got != 2 {
		t.Fatalf("count: %d", got)
	}
	marked := w.reports.Reported(lea, w.chat)
	if len(marked) != 2 || !marked[first] || !marked[second] {
		t.Fatalf("reported marks: %v", marked)
	}
	if len(w.reports.Reported(mia, w.chat)) != 0 || len(w.reports.Reported(lea, strings.Repeat("0", 32))) != 0 {
		t.Fatal("marks leak to the other child or another conversation")
	}
	list, _ := w.reports.ForChild("anna", lea.TenantID, lea.ChildID)
	if len(list) != 2 || list[0].Body != "zwei" || list[1].Body != "eins" {
		t.Fatalf("newest first expected: %+v", list)
	}
}

func TestGuardianDismissesOnlyReportsOfTheirOwnChild(t *testing.T) {
	w := newWorld(t)
	seq := w.say(w.miaP, w.chat, "weg damit")
	if _, err := w.reports.File(w.leaP, w.chat, seq); err != nil {
		t.Fatal(err)
	}
	list, _ := w.reports.ForChild("anna", lea.TenantID, lea.ChildID)
	id := list[0].ID
	for name, c := range map[string][3]string{
		"other family":      {"bruno", mia.TenantID, mia.ChildID},
		"other as reporter": {"bruno", lea.TenantID, lea.ChildID},
		"sibling's page":    {"anna", tim.TenantID, tim.ChildID},
		"unknown guardian":  {"nobody", lea.TenantID, lea.ChildID},
	} {
		if err := w.reports.Dismiss(c[0], c[1], c[2], id); err == nil {
			t.Errorf("%s dismissed the report", name)
		}
	}
	if err := w.reports.Dismiss("anna", lea.TenantID, lea.ChildID, strings.Repeat("0", 32)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown report: %v", err)
	}
	if w.reports.Count("albert", lea.TenantID, lea.ChildID) != 1 {
		t.Fatal("a refused dismissal removed the report")
	}
	if err := w.reports.Dismiss("albert", lea.TenantID, lea.ChildID, id); err != nil {
		t.Fatal(err)
	}
	if w.reports.Count("anna", lea.TenantID, lea.ChildID) != 0 {
		t.Fatal("report still listed after dismissal")
	}
	// The copy of the message and the sender's name are gone from disk.
	if raw, _ := os.ReadFile(w.path); strings.Contains(string(raw), "weg damit") || strings.Contains(string(raw), "Mia") {
		t.Fatalf("dismissed report still holds text or name: %s", raw)
	}
}

func TestReportOutlivesTheContactButNotItsRetention(t *testing.T) {
	w := newWorld(t)
	seq := w.say(w.miaP, w.chat, "Beleg")
	if _, err := w.reports.File(w.leaP, w.chat, seq); err != nil {
		t.Fatal(err)
	}
	// The other family ends the contact and Mia is renamed: the parents still see what was reported, as it was.
	if err := w.contacts.Remove("bruno", mia.TenantID, mia.ChildID, w.chat); err != nil {
		t.Fatal(err)
	}
	if err := w.families.RenameChild("bruno", mia.TenantID, mia.ChildID, "Anders"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(w.path, w.messages, w.families, w.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	list, err := reopened.ForChild("anna", lea.TenantID, lea.ChildID)
	if err != nil || len(list) != 1 || list[0].Body != "Beleg" || list[0].From != "Mia" {
		t.Fatalf("after contact removal and restart: %+v %v", list, err)
	}
	w.clock.advance(Retention - time.Minute)
	if reopened.Count("anna", lea.TenantID, lea.ChildID) != 1 {
		t.Fatal("report expired early")
	}
	w.clock.advance(2 * time.Minute)
	if reopened.Count("anna", lea.TenantID, lea.ChildID) != 0 {
		t.Fatal("report shown after its retention")
	}
	// The next write removes the expired copy from disk.
	other := w.connect("anna", tim, "bruno", mia)
	timP := w.device("anna", tim)
	s := w.say(w.miaP, other, "neu")
	if _, err := reopened.File(timP, other, s); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(w.path); strings.Contains(string(raw), "Beleg") {
		t.Fatal("expired report still on disk")
	}
}

func TestOpenReportsPerChildAreLimited(t *testing.T) {
	w := newWorld(t)
	var last uint64
	for i := 0; i < MaxOpenPerChild+1; i++ {
		w.clock.advance(time.Minute)
		last = w.say(w.miaP, w.chat, "Nachricht "+strings.Repeat("x", i+1))
		created, err := w.reports.File(w.leaP, w.chat, last)
		if i < MaxOpenPerChild && (err != nil || !created) {
			t.Fatalf("report %d: %v %v", i, created, err)
		}
		if i == MaxOpenPerChild && !errors.Is(err, ErrLimit) {
			t.Fatalf("report beyond the limit: %v", err)
		}
	}
	// Another child is not affected by Lea's limit.
	other := w.connect("anna", tim, "bruno", mia)
	timP := w.device("anna", tim)
	s := w.say(w.miaP, other, "an Tim")
	if created, err := w.reports.File(timP, other, s); err != nil || !created {
		t.Fatalf("other child blocked by the limit: %v %v", created, err)
	}
	// Dismissing one makes room again.
	list, _ := w.reports.ForChild("anna", lea.TenantID, lea.ChildID)
	if err := w.reports.Dismiss("anna", lea.TenantID, lea.ChildID, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if created, err := w.reports.File(w.leaP, w.chat, last); err != nil || !created {
		t.Fatalf("report after making room: %v %v", created, err)
	}
}

func TestFailedWriteStoresNothingAndSaysSo(t *testing.T) {
	w := newWorld(t)
	seq := w.say(w.miaP, w.chat, "nicht gespeichert")
	w.reports.failWrite = true
	if created, err := w.reports.File(w.leaP, w.chat, seq); created || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed write: %v %v", created, err)
	}
	w.reports.failWrite = false
	if w.reports.Count("anna", lea.TenantID, lea.ChildID) != 0 || len(w.reports.Reported(lea, w.chat)) != 0 {
		t.Fatal("a report is shown although it was not stored")
	}
	if created, err := w.reports.File(w.leaP, w.chat, seq); err != nil || !created {
		t.Fatalf("retry: %v %v", created, err)
	}
}

func TestOpenFailsClosed(t *testing.T) {
	w := newWorld(t)
	seq := w.say(w.miaP, w.chat, "x")
	if _, err := w.reports.File(w.leaP, w.chat, seq); err != nil {
		t.Fatal(err)
	}
	good, _ := os.ReadFile(w.path)
	open := func() error { _, err := Open(w.path, w.messages, w.families, w.clock.now); return err }
	for name, breakIt := range map[string]func() error{
		"malformed":     func() error { return os.WriteFile(w.path, []byte(`{"reports":[`), 0600) },
		"unknown field": func() error { return os.WriteFile(w.path, []byte(`{"reports":[],"extra":1}`), 0600) },
		"trailing data": func() error { return os.WriteFile(w.path, append(append([]byte{}, good...), good...), 0600) },
		"record no id": func() error {
			return os.WriteFile(w.path, []byte(strings.Replace(string(good), `"id":"`, `"id":"","x":"`, 1)), 0600)
		},
		"duplicate": func() error {
			return os.WriteFile(w.path, []byte(strings.Replace(string(good), `"reports":[`, `"reports":[`+strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(string(good)), `{"reports":[`), `]}`)+`,`, 1)), 0600)
		},
		"group readable": func() error { _ = os.WriteFile(w.path, good, 0600); return os.Chmod(w.path, 0640) },
	} {
		if err := breakIt(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := open(); err == nil {
			t.Errorf("%s: Open accepted a broken store", name)
		}
		_ = os.Chmod(w.path, 0600)
	}
	if err := os.WriteFile(w.path, good, 0600); err != nil {
		t.Fatal(err)
	}
	if err := open(); err != nil {
		t.Fatalf("good file rejected: %v", err)
	}
	for _, bad := range []func() (*Store, error){
		func() (*Store, error) { return Open("", w.messages, w.families, w.clock.now) },
		func() (*Store, error) { return Open(w.path, nil, w.families, w.clock.now) },
		func() (*Store, error) { return Open(w.path, w.messages, nil, w.clock.now) },
		func() (*Store, error) { return Open(w.path, w.messages, w.families, nil) },
	} {
		if _, err := bad(); err == nil {
			t.Fatal("Open accepted missing dependencies")
		}
	}
}

// gappy answers like a log after compaction: the next stored message can have
// a higher sequence number than the one asked for.
type gappy struct{ next messages.Message }

func (g gappy) History(_ messages.Principal, _ string, after uint64, _ int) ([]messages.Message, error) {
	if g.next.Seq > after {
		return []messages.Message{g.next}, nil
	}
	return nil, nil
}

func TestReportNeverPicksANeighbouringMessage(t *testing.T) {
	w := newWorld(t)
	path := filepath.Join(t.TempDir(), "reports.json")
	s, err := Open(path, gappy{next: messages.Message{Seq: 7, From: mia, Body: "eine andere Nachricht", At: w.clock.now()}}, w.families, w.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	// Message 5 expired; asking for it must not report message 7 instead.
	if created, err := s.File(w.leaP, w.chat, 5); created || !errors.Is(err, ErrNotFound) {
		t.Fatalf("report of a missing message: %v %v", created, err)
	}
	if s.Count("anna", lea.TenantID, lea.ChildID) != 0 {
		t.Fatal("a neighbouring message was reported")
	}
	if created, err := s.File(w.leaP, w.chat, 7); err != nil || !created {
		t.Fatalf("report of the existing message: %v %v", created, err)
	}
}

func TestGuardianOfASiblingOnlyDoesNotSeeTheReports(t *testing.T) {
	w := newWorld(t)
	// Dora is a guardian of Tim only, in the same family as Lea.
	if err := w.families.BootstrapGuardian(authz.GuardianGrant{GuardianID: "dora", ChildID: "tim", TenantID: "family-a", Active: true, CanManageDevices: true, CanApproveContacts: true}); err != nil {
		t.Fatal(err)
	}
	seq := w.say(w.miaP, w.chat, "nur für Leas Eltern")
	if _, err := w.reports.File(w.leaP, w.chat, seq); err != nil {
		t.Fatal(err)
	}
	if list, err := w.reports.ForChild("dora", lea.TenantID, lea.ChildID); !errors.Is(err, ErrForbidden) || len(list) != 0 {
		t.Fatalf("guardian of the sibling sees Lea's reports: %v %v", list, err)
	}
	id := ""
	if list, _ := w.reports.ForChild("anna", lea.TenantID, lea.ChildID); len(list) == 1 {
		id = list[0].ID
	}
	if err := w.reports.Dismiss("dora", lea.TenantID, lea.ChildID, id); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guardian of the sibling dismissed Lea's report: %v", err)
	}
	if list, err := w.reports.ForChild("dora", tim.TenantID, tim.ChildID); err != nil || len(list) != 0 {
		t.Fatalf("own child: %v %v", list, err)
	}
}
