package guardianchat

import (
	"context"
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

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// guardians is the identity check: everyone is active unless listed.
type guardians struct {
	mu       sync.Mutex
	inactive map[string]bool
}

func (g *guardians) ActiveGuardian(_ context.Context, guardianID, tenantID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.inactive[tenantID+"/"+guardianID]
}

func (g *guardians) set(tenant, id string, inactive bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inactive[tenant+"/"+id] = inactive
}

// devices approves exactly the listed devices.
type devices struct {
	mu sync.Mutex
	ok map[string]bool
}

func (d *devices) ActiveDevice(tenantID, childID, deviceID string) (devicepairing.Device, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return devicepairing.Device{}, d.ok[tenantID+"/"+childID+"/"+deviceID]
}

type world struct {
	t         *testing.T
	store     *Store
	path      string
	families  *familystore.Store
	contacts  *contacts.Store
	guardians *guardians
	devices   *devices
	clock     *clock
	link      string
}

var (
	anna   = Guardian("family-a", "anna")
	albert = Guardian("family-a", "albert")
	bruno  = Guardian("family-b", "bruno")
	carla  = Guardian("family-c", "carla")
	alex   = Child("family-a", "alex", "phone-alex")
	ada    = Child("family-a", "ada", "phone-ada")
	bea    = Child("family-b", "bea", "phone-bea")
	cem    = Child("family-c", "cem", "phone-cem")
)

func full(guardian, tenant, child string) authz.GuardianGrant {
	return authz.GuardianGrant{GuardianID: guardian, ChildID: child, TenantID: tenant, Active: true, CanManageDevices: true, CanApproveContacts: true}
}

// newWorld has three families. Anna and Albert are guardians of Alex, Anna
// alone of Ada; Bruno of Bea; Carla of Cem. Alex and Bea are connected.
func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	w := &world{t: t, path: filepath.Join(dir, "guardian-chat.log"), clock: &clock{t: time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)},
		guardians: &guardians{inactive: map[string]bool{}}, devices: &devices{ok: map[string]bool{}}}
	var err error
	if w.families, err = familystore.Open(filepath.Join(dir, "families.json")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ tenant, child, name, guardian string }{
		{"family-a", "alex", "Alex", "anna"}, {"family-a", "ada", "Ada", "anna"}, {"family-b", "bea", "Bea", "bruno"}, {"family-c", "cem", "Cem", "carla"},
	} {
		if err := w.families.Bootstrap(familydashboard.Child{ID: c.child, TenantID: c.tenant, DisplayName: c.name}, full(c.guardian, c.tenant, c.child)); err != nil {
			t.Fatal(err)
		}
		w.devices.ok[c.tenant+"/"+c.child+"/phone-"+c.child] = true
	}
	if err := w.families.BootstrapGuardian(full("albert", "family-a", "alex")); err != nil {
		t.Fatal(err)
	}
	if w.contacts, err = contacts.Open(filepath.Join(dir, "contacts.json"), w.families, w.clock.now); err != nil {
		t.Fatal(err)
	}
	w.link = w.connect("anna", "family-a", "alex", "bruno", "family-b", "bea")
	w.open()
	return w
}

func (w *world) connect(g1, t1, c1, g2, t2, c2 string) string {
	w.t.Helper()
	code, err := w.contacts.Invite(g1, t1, c1)
	if err != nil {
		w.t.Fatal(err)
	}
	id, err := w.contacts.Redeem(g2, t2, c2, code)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.contacts.Confirm(g1, t1, c1, id); err != nil {
		w.t.Fatal(err)
	}
	return id
}

func (w *world) open() {
	w.t.Helper()
	if w.store != nil {
		_ = w.store.Close()
	}
	s, err := Open(w.path, w.families, w.contacts, w.guardians, w.devices, w.clock.now)
	if err != nil {
		w.t.Fatal(err)
	}
	w.store = s
	w.t.Cleanup(func() { _ = s.Close() })
}

// thread returns the id of the actor's thread with the given peer.
func (w *world) thread(a Actor, peer Actor) string {
	w.t.Helper()
	for _, th := range w.store.Threads(a) {
		if th.Peer == peer.Participant {
			return th.ID
		}
	}
	w.t.Fatalf("%v has no thread with %v", a.Participant, peer.Participant)
	return ""
}

func (w *world) has(a Actor, peer Actor) bool {
	for _, th := range w.store.Threads(a) {
		if th.Peer == peer.Participant {
			return true
		}
	}
	return false
}

var nextID int

func cid() string {
	nextID++
	return "client-id-" + strings.Repeat("0", 6) + string(rune('a'+nextID%26)) + string(rune('a'+(nextID/26)%26)) + string(rune('a'+(nextID/676)%26))
}

func (w *world) send(a Actor, thread, body string) Message {
	w.t.Helper()
	m, _, err := w.store.Send(a, thread, cid(), body)
	if err != nil {
		w.t.Fatalf("send as %v: %v", a.Participant, err)
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

func TestAGuardianAndTheirOwnChildTalk(t *testing.T) {
	w := newWorld(t)
	id := w.thread(anna, alex)
	if id != w.thread(alex, anna) {
		t.Fatal("the two sides see different threads")
	}
	if !regexpID.MatchString(id) {
		t.Fatalf("thread id %q", id)
	}
	first := w.send(anna, id, "  Znacht isch parat  ")
	second := w.send(alex, id, "Chume grad")
	if first.Seq != 1 || second.Seq != 2 || first.Body != "Znacht isch parat" || first.From != anna.Participant || second.From != alex.Participant {
		t.Fatalf("messages: %+v %+v", first, second)
	}
	for _, a := range []Actor{anna, alex} {
		got, err := w.store.Recent(a, id, 50)
		if err != nil || bodies(got) != "Znacht isch parat|Chume grad" {
			t.Fatalf("%v reads %q %v", a.Participant, bodies(got), err)
		}
	}
	after, err := w.store.History(alex, id, 1, 50)
	if err != nil || bodies(after) != "Chume grad" {
		t.Fatalf("history: %q %v", bodies(after), err)
	}
}

func TestThreadsListExactlyWhoOneMayTalkTo(t *testing.T) {
	w := newWorld(t)
	kinds := func(a Actor) string {
		var out []string
		for _, th := range w.store.Threads(a) {
			out = append(out, th.Kind.String()+":"+th.Peer.TenantID+"/"+th.Peer.ID)
		}
		return strings.Join(out, " ")
	}
	for actor, want := range map[Actor]string{
		// Own children first, then the guardians of connected children.
		anna:   "family:family-a/ada family:family-a/alex parents:family-b/bruno",
		albert: "family:family-a/alex parents:family-b/bruno",
		bruno:  "family:family-b/bea parents:family-a/albert parents:family-a/anna",
		carla:  "family:family-c/cem",
		alex:   "family:family-a/albert family:family-a/anna",
		ada:    "family:family-a/anna",
		bea:    "family:family-b/bruno",
	} {
		if got := kinds(actor); got != want {
			t.Errorf("%v: %q, want %q", actor.Participant, got, want)
		}
	}
	// A parents thread says through which of the other family's children it exists.
	for _, th := range w.store.Threads(anna) {
		if th.Kind == Parents && (len(th.Via) != 1 || th.Via[0] != (contacts.Party{TenantID: "family-b", ChildID: "bea"})) {
			t.Fatalf("via: %+v", th.Via)
		}
	}
	for _, unknown := range []Actor{Guardian("family-a", "mallory"), Guardian("family-b", "anna"), Child("family-b", "alex", "phone-alex"), {}, Child("family-a", "alex", "")} {
		if got := w.store.Threads(unknown); len(got) != 0 {
			t.Errorf("%+v sees %+v", unknown, got)
		}
	}
}

func TestGuardiansOfConnectedChildrenTalk(t *testing.T) {
	w := newWorld(t)
	id := w.thread(anna, bruno)
	if id != w.thread(bruno, anna) || id == w.thread(albert, bruno) || id == w.thread(anna, alex) {
		t.Fatal("thread ids are not one per pair")
	}
	w.send(anna, id, "Hoi Bruno")
	w.send(bruno, id, "Hoi Anna")
	got, err := w.store.Recent(bruno, id, 50)
	if err != nil || bodies(got) != "Hoi Bruno|Hoi Anna" {
		t.Fatalf("bruno reads %q %v", bodies(got), err)
	}
	// A block between the children does not silence the parents.
	if err := w.contacts.Block(contacts.Party{TenantID: "family-b", ChildID: "bea"}, w.link); err != nil {
		t.Fatal(err)
	}
	w.send(bruno, id, "Mir sötte rede")
	if got, _ := w.store.Recent(anna, id, 50); bodies(got) != "Hoi Bruno|Hoi Anna|Mir sötte rede" {
		t.Fatalf("after a block: %q", bodies(got))
	}
}

func TestNobodyElseReadsOrWritesAThread(t *testing.T) {
	w := newWorld(t)
	family := w.thread(anna, alex)
	parents := w.thread(anna, bruno)
	w.send(anna, family, "privat")
	w.send(anna, parents, "unter Erwachsenen")
	outsiders := map[string][]Actor{
		// The second guardian of the same child has a thread of their own
		// and no access to this one; neither has a sibling.
		family: {albert, ada, bruno, bea, carla, cem, Guardian("family-b", "anna"), Child("family-a", "alex", "other-phone"), Child("family-a", "alex", ""), {}},
		// No child reads what the adults write, not even their own.
		parents: {albert, alex, ada, bea, carla, cem, Guardian("family-a", "bruno"), {}},
	}
	for id, actors := range outsiders {
		for _, a := range actors {
			if _, _, err := w.store.Send(a, id, cid(), "hallo"); !errors.Is(err, ErrForbidden) {
				t.Errorf("%+v sends: %v", a, err)
			}
			if got, err := w.store.Recent(a, id, 50); !errors.Is(err, ErrForbidden) || got != nil {
				t.Errorf("%+v reads: %v %v", a, got, err)
			}
			if got, err := w.store.History(a, id, 0, 50); !errors.Is(err, ErrForbidden) || got != nil {
				t.Errorf("%+v reads history: %v %v", a, got, err)
			}
		}
	}
	// Unknown and malformed ids answer exactly like foreign ones.
	for _, id := range []string{"", "x", strings.Repeat("0", 32), family + "0"} {
		if _, _, err := w.store.Send(anna, id, cid(), "hallo"); !errors.Is(err, ErrForbidden) {
			t.Errorf("id %q: %v", id, err)
		}
	}
	if got, _ := w.store.Recent(anna, family, 50); bodies(got) != "privat" {
		t.Fatalf("a refused attempt changed the thread: %q", bodies(got))
	}
}

func TestAccessFollowsTheCurrentState(t *testing.T) {
	w := newWorld(t)
	family, parents := w.thread(anna, alex), w.thread(anna, bruno)
	w.send(anna, family, "eins")
	w.send(anna, parents, "eins")
	denied := func(a Actor, id string) bool {
		_, _, sendErr := w.store.Send(a, id, cid(), "x")
		_, readErr := w.store.Recent(a, id, 50)
		return errors.Is(sendErr, ErrForbidden) && errors.Is(readErr, ErrForbidden)
	}
	// A revoked device loses the child's access at once; the guardian keeps theirs.
	w.devices.ok["family-a/alex/phone-alex"] = false
	if !denied(alex, family) || denied(anna, family) {
		t.Fatal("device revocation not applied")
	}
	w.devices.ok["family-a/alex/phone-alex"] = true
	// A locked or deactivated guardian neither writes nor is written to.
	w.guardians.set("family-a", "anna", true)
	if !denied(anna, family) || !denied(alex, family) || !denied(anna, parents) || !denied(bruno, parents) {
		t.Fatal("a locked guardian still takes part")
	}
	if w.has(alex, anna) || w.has(bruno, anna) || !w.has(alex, albert) || !w.has(bruno, albert) {
		t.Fatal("thread lists do not follow the lock")
	}
	w.guardians.set("family-a", "anna", false)
	if denied(anna, family) || denied(bruno, parents) {
		t.Fatal("access did not return with the guardian")
	}
	// Ending the children's contact ends the parents' conversation.
	if err := w.contacts.Remove("bruno", "family-b", "bea", w.link); err != nil {
		t.Fatal(err)
	}
	if !denied(anna, parents) || !denied(bruno, parents) || w.has(anna, bruno) || denied(anna, family) {
		t.Fatal("the parents' thread outlived the contact")
	}
}

func TestSendingIsIdempotentValidatedAndLimited(t *testing.T) {
	w := newWorld(t)
	id := w.thread(anna, alex)
	m, dup, err := w.store.Send(anna, id, "client-id-0000001", "hallo")
	if err != nil || dup {
		t.Fatal(err)
	}
	again, dup, err := w.store.Send(anna, id, "client-id-0000001", "hallo")
	if err != nil || !dup || again.Seq != m.Seq {
		t.Fatalf("retry: %+v %v %v", again, dup, err)
	}
	if _, _, err := w.store.Send(anna, id, "client-id-0000001", "anders"); !errors.Is(err, ErrConflict) {
		t.Fatalf("same id, other text: %v", err)
	}
	// The same client id from the other participant is another message.
	if _, dup, err := w.store.Send(alex, id, "client-id-0000001", "hallo"); err != nil || dup {
		t.Fatalf("other sender, same id: %v %v", dup, err)
	}
	for name, c := range map[string][2]string{
		"empty": {"client-id-0000002", "   "}, "too long": {"client-id-0000003", strings.Repeat("x", 1001)},
		"control": {"client-id-0000004", "a\x00b"}, "bidi": {"client-id-0000005", "a‮b"}, "short id": {"short", "hallo"}, "odd id": {"client id 0000006!", "hallo"},
	} {
		if _, _, err := w.store.Send(anna, id, c[0], c[1]); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for i := 1; i < MaxPerMinute; i++ {
		w.send(anna, id, "viel")
	}
	if _, _, err := w.store.Send(anna, id, cid(), "zu viel"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("over the limit: %v", err)
	}
	// The limit is per sender and per minute.
	w.send(alex, id, "ich darf noch")
	w.clock.add(time.Minute)
	w.send(anna, id, "wieder")
}

func TestMessagesAreDurableBeforeTheyAreAcknowledged(t *testing.T) {
	w := newWorld(t)
	id := w.thread(anna, bruno)
	w.send(anna, id, "bleibt")
	info, err := os.Lstat(w.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("log file: %v %v", info, err)
	}
	// A failed write is not acknowledged and leaves no trace.
	w.store.failWrite = true
	if _, _, err := w.store.Send(anna, id, "client-id-failing1", "verloren"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed write: %v", err)
	}
	if got, _ := w.store.Recent(anna, id, 50); bodies(got) != "bleibt" {
		t.Fatalf("after a failed write: %q", bodies(got))
	}
	// The same client id can be used again, because nothing was stored.
	if _, dup, err := w.store.Send(anna, id, "client-id-failing1", "zweiter Versuch"); err != nil || dup {
		t.Fatalf("retry after failure: %v %v", dup, err)
	}
	w.open()
	got, err := w.store.Recent(bruno, id, 50)
	if err != nil || bodies(got) != "bleibt|zweiter Versuch" {
		t.Fatalf("after restart: %q %v", bodies(got), err)
	}
	// Sequence numbers continue, and retries are still recognized.
	if _, dup, _ := w.store.Send(anna, id, "client-id-failing1", "zweiter Versuch"); !dup {
		t.Fatal("retry not recognized after restart")
	}
	if m := w.send(bruno, id, "weiter"); m.Seq != 3 {
		t.Fatalf("sequence after restart: %d", m.Seq)
	}
}

func TestOpenFailsClosedAndRepairsOnlyATornLastLine(t *testing.T) {
	w := newWorld(t)
	id := w.thread(anna, alex)
	w.send(anna, id, "ganz")
	_ = w.store.Close()
	good, _ := os.ReadFile(w.path)
	open := func() error {
		s, err := Open(w.path, w.families, w.contacts, w.guardians, w.devices, w.clock.now)
		if err == nil {
			_ = s.Close()
		}
		return err
	}
	// A crash in the middle of an append leaves half a line: it was never
	// acknowledged and is removed.
	_ = os.WriteFile(w.path, append(append([]byte{}, good...), []byte(`{"thread":"`)...), 0600)
	if err := open(); err != nil {
		t.Fatalf("torn last line: %v", err)
	}
	if after, _ := os.ReadFile(w.path); string(after) != string(good) {
		t.Fatal("the torn line was not removed exactly")
	}
	second := strings.Replace(strings.Replace(string(good), `"client-id-`, `"client-xx-`, 1), `"ganz"`, `"zwei"`, 1)
	for name, content := range map[string]string{
		// A line filed under another conversation than that of its two participants.
		"foreign thread id": strings.Replace(string(good), id, strings.Repeat("0", 32), 1),
		// A second message that reuses a sequence number.
		"sequence repeats": string(good) + second,
		"corrupt line":     "{not json}\n",
		"unknown field":    strings.Replace(string(good), `"seq"`, `"extra":1,"seq"`, 1),
		"sequence reset":   string(good) + string(good),
		"no sender":        strings.Replace(string(good), `"from":{`, `"from":{"x":1,`, 1),
	} {
		_ = os.WriteFile(w.path, []byte(content), 0600)
		if err := open(); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	_ = os.WriteFile(w.path, good, 0600)
	if err := open(); err != nil {
		t.Fatalf("restored log: %v", err)
	}
	_ = os.Chmod(w.path, 0644)
	if err := open(); err == nil {
		t.Error("world-readable log opened")
	}
	_ = os.Remove(w.path)
	_ = os.WriteFile(w.path+".real", good, 0600)
	_ = os.Symlink(w.path+".real", w.path)
	if err := open(); err == nil {
		t.Error("symlinked log opened")
	}
	for _, bad := range []func() error{
		func() error {
			_, err := Open("", w.families, w.contacts, w.guardians, w.devices, w.clock.now)
			return err
		},
		func() error {
			_, err := Open(w.path+".x", nil, w.contacts, w.guardians, w.devices, w.clock.now)
			return err
		},
		func() error {
			_, err := Open(w.path+".x", w.families, nil, w.guardians, w.devices, w.clock.now)
			return err
		},
		func() error {
			_, err := Open(w.path+".x", w.families, w.contacts, nil, w.devices, w.clock.now)
			return err
		},
		func() error {
			_, err := Open(w.path+".x", w.families, w.contacts, w.guardians, nil, w.clock.now)
			return err
		},
		func() error {
			_, err := Open(w.path+".x", w.families, w.contacts, w.guardians, w.devices, nil)
			return err
		},
	} {
		if bad() == nil {
			t.Error("incomplete dependencies accepted")
		}
	}
}

func TestCompactionDropsOldMessagesAndEndedRelationships(t *testing.T) {
	w := newWorld(t)
	family, parents := w.thread(anna, alex), w.thread(anna, bruno)
	w.send(anna, family, "alt")
	w.send(anna, parents, "unter Eltern")
	w.clock.add(Retention - time.Hour)
	w.send(alex, family, "neu")
	w.clock.add(2 * time.Hour)
	if err := w.store.Compact(); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.store.Recent(anna, family, 50); bodies(got) != "neu" {
		t.Fatalf("after retention: %q", bodies(got))
	}
	raw, _ := os.ReadFile(w.path)
	if strings.Contains(string(raw), "alt") || strings.Contains(string(raw), "unter Eltern") {
		t.Fatal("expired messages still on disk")
	}
	// Sequence numbers never restart, even when everything expired and the
	// server was restarted in between.
	w.open()
	if m := w.send(bruno, parents, "wieder da"); m.Seq != 2 {
		t.Fatalf("sequence after expiry: %d", m.Seq)
	}
	// A locked guardian's threads are kept: a lock can be lifted.
	w.guardians.set("family-a", "anna", true)
	if err := w.store.Compact(); err != nil {
		t.Fatal(err)
	}
	w.guardians.set("family-a", "anna", false)
	if got, _ := w.store.Recent(bruno, parents, 50); bodies(got) != "wieder da" {
		t.Fatalf("a lock deleted messages: %q", bodies(got))
	}
	// An ended contact takes the parents' messages with it, also from disk.
	if err := w.contacts.Remove("anna", "family-a", "alex", w.link); err != nil {
		t.Fatal(err)
	}
	if err := w.store.Compact(); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(w.path)
	if strings.Contains(string(raw), "wieder da") || !strings.Contains(string(raw), "neu") {
		t.Fatalf("after the contact ended: %s", raw)
	}
	// Reconnecting starts an empty conversation under the same id.
	w.link = w.connect("anna", "family-a", "alex", "bruno", "family-b", "bea")
	if got, err := w.store.Recent(anna, w.thread(anna, bruno), 50); err != nil || len(got) != 0 {
		t.Fatalf("old messages came back: %q %v", bodies(got), err)
	}
}

func TestTheNumberOfMessagesPerThreadIsCapped(t *testing.T) {
	w := newWorld(t)
	w.store.maxPerThread = 8
	id := w.thread(anna, alex)
	for i := 0; i < 12; i++ {
		w.send(anna, id, "n"+string(rune('a'+i)))
		w.clock.add(10 * time.Second)
	}
	got, _ := w.store.Recent(anna, id, 50)
	if len(got) > 10 || got[len(got)-1].Body != "nl" || got[len(got)-1].Seq != 12 {
		t.Fatalf("cap: %d messages, last %+v", len(got), got[len(got)-1])
	}
}

func TestAMessageIsAcknowledgedOnlyAfterItWasSynced(t *testing.T) {
	w := newWorld(t)
	id := w.thread(anna, alex)
	synced := 0
	w.store.syncFile = func(f *os.File) error { synced++; return f.Sync() }
	w.send(anna, id, "eins")
	if synced != 1 {
		t.Fatalf("one message was synced %d times", synced)
	}
	// A sync that fails means the message may not be on disk: not acknowledged.
	w.store.syncFile = func(*os.File) error { return errors.New("disk gone") }
	if _, _, err := w.store.Send(anna, id, cid(), "zwei"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unsynced message acknowledged: %v", err)
	}
	w.store.syncFile = (*os.File).Sync
	if got, _ := w.store.Recent(anna, id, 50); bodies(got) != "eins" {
		t.Fatalf("after a failed sync: %q", bodies(got))
	}
	w.open()
	if got, _ := w.store.Recent(anna, id, 50); bodies(got) != "eins" {
		t.Fatalf("after restart: %q", bodies(got))
	}
}

func TestThreadIdsSeparateRolesAndTenants(t *testing.T) {
	adult := func(tenant, id string) authz.Participant { return Guardian(tenant, id).Participant }
	child := func(tenant, id string) authz.Participant { return Child(tenant, id, "").Participant }
	base := threadID(adult("t", "a"), child("t", "b"))
	if base != threadID(child("t", "b"), adult("t", "a")) {
		t.Fatal("the id depends on the order")
	}
	for name, other := range map[string]string{
		"roles swapped":    threadID(child("t", "a"), adult("t", "b")),
		"two adults":       threadID(adult("t", "a"), adult("t", "b")),
		"other tenant":     threadID(adult("u", "a"), child("u", "b")),
		"ids run together": threadID(adult("t", "a\x00t"), child("", "b")),
	} {
		if other == base {
			t.Errorf("%s: same id", name)
		}
	}
}

func TestTheIdentityCheckCanBeReplacedButNotRemoved(t *testing.T) {
	w := newWorld(t)
	if err := w.store.SetGuardians(nil); err == nil {
		t.Fatal("nil identity check accepted")
	}
	if !w.has(alex, anna) {
		t.Fatal("precondition")
	}
	strict := &guardians{inactive: map[string]bool{"family-a/anna": true}}
	if err := w.store.SetGuardians(strict); err != nil {
		t.Fatal(err)
	}
	if w.has(alex, anna) || len(w.store.Threads(anna)) != 0 {
		t.Fatal("the replaced identity check is not used")
	}
}
