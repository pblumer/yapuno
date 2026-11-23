package main

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/messages"
	"github.com/pblumer/yapuno/internal/signup"
)

var crewThread = regexp.MustCompile(`href="/child/chat/([0-9a-f]{32})"`)

// newDeletingServer runs everything a family leaves traces in: devices,
// contacts, both chats, looks, reports, invitations and remembered sessions.
func newDeletingServer(t *testing.T, withLocks bool) *chatServer {
	t.Helper()
	s := newSecurityServer(t, withLocks)
	s.signupPath = filepath.Join(s.root, "signup.json")
	s.chatPath = filepath.Join(s.root, "guardian-chat.log")
	s.sessionsPath = filepath.Join(s.root, "guardian-sessions.json")
	s.everything = true
	s.start(withLocks)
	return &chatServer{securityServer: s}
}

// connect makes two children contacts the ordinary way, with a code.
func (s *chatServer) connect(g1 string, a contacts.Party, g2 string, b contacts.Party) string {
	s.t.Helper()
	code, err := s.contacts.Invite(g1, a.TenantID, a.ChildID)
	if err != nil {
		s.t.Fatal(err)
	}
	id, err := s.contacts.Redeem(g2, b.TenantID, b.ChildID, code)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := s.contacts.Confirm(g1, a.TenantID, a.ChildID, id); err != nil {
		s.t.Fatal(err)
	}
	return id
}

// device pairs a device for a child and signs it in.
func (s *chatServer) device(guardian string, p contacts.Party) (string, *http.Cookie) {
	s.t.Helper()
	id, key := pairingtest.Pair(s.t, s.pairing, guardian, p.TenantID, p.ChildID)
	return id, childLogin(s.t, s.handler, id, key)
}

// files returns the content of every data file, by name.
func (s *chatServer) files() map[string]string {
	s.t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		s.t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.root, e.Name()))
		if err != nil {
			s.t.Fatal(err)
		}
		out[e.Name()] = string(raw)
	}
	return out
}

var (
	alexParty = contacts.Party{TenantID: "tenant-a", ChildID: "child-a"}
	beaParty  = contacts.Party{TenantID: "tenant-b", ChildID: "child-b"}
)

func TestAGuardianDeletesAChildAndEverythingAboutItEndToEnd(t *testing.T) {
	s := newDeletingServer(t, false)
	anna, bruno := s.issue("guardian-a", "tenant-a"), s.issue("guardian-b", "tenant-b")
	// Alex gets a sibling, so that Alex is not the last child.
	if w := s.do(http.MethodPost, "/family/children", anna, url.Values{"csrf_token": {s.csrf(anna, "/family/children")}, "name": {"Ari"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("second child: %d", w.Code)
	}
	var ari contacts.Party
	for _, c := range s.families.Cards("guardian-a", "tenant-a") {
		if c.DisplayName == "Ari" {
			ari = contacts.Party{TenantID: "tenant-a", ChildID: c.ID}
		}
	}
	// Traces of Alex: a device, a contact with a chat, a chat with the
	// guardian, a look, a report, and an invitation that names Alex.
	link := s.connect("guardian-a", alexParty, "guardian-b", beaParty)
	ariLink := s.connect("guardian-a", ari, "guardian-b", beaParty)
	alexDevice, alex := s.device("guardian-a", alexParty)
	beaDevice, bea := s.device("guardian-b", beaParty)
	if code := s.write("/child/chat", alex, link, "Hoi Bea vo Alex"); code != http.StatusSeeOther {
		t.Fatalf("child writes: %d", code)
	}
	if code := s.write("/child/chat", bea, link, "Gemein vo Bea"); code != http.StatusSeeOther {
		t.Fatalf("other child writes: %d", code)
	}
	own := s.threads(anna, "Deine Kinder")
	var withAlex string
	for _, id := range own {
		if strings.Contains(s.read("/family/chat", anna, id), "<h1>Alex</h1>") {
			withAlex = id
		}
	}
	if withAlex == "" || s.write("/family/chat", anna, withAlex, "Znacht für Alex") != http.StatusSeeOther {
		t.Fatal("guardian cannot write to Alex")
	}
	figure := appearance.Preference{Skin: "night", Figure: appearance.Figure{Shape: "ghost", Color: "peach"}}
	if err := s.looks.Set(appearance.Subject{Kind: appearance.KindChild, TenantID: "tenant-a", ID: "child-a"}, figure); err != nil {
		t.Fatal(err)
	}
	alexP := messages.Principal{Party: alexParty, DeviceID: alexDevice}
	beaP := messages.Principal{Party: beaParty, DeviceID: beaDevice}
	history, err := s.messages.Recent(alexP, link, 10)
	if err != nil || len(history) != 2 {
		t.Fatalf("history: %v %v", history, err)
	}
	if _, err := s.reports.File(alexP, link, history[1].Seq); err != nil {
		t.Fatalf("Alex reports: %v", err)
	}
	if _, err := s.reports.File(beaP, link, history[0].Seq); err != nil {
		t.Fatalf("Bea reports: %v", err)
	}
	inviteCode := s.inviteFor(anna, "child-a")

	// The overview leads to the page; the page asks for the name.
	if page := s.do(http.MethodGet, "/family", anna, nil).Body.String(); !strings.Contains(page, `href="/family/children/delete?child_id=child-a"`) {
		t.Fatal("overview does not offer to delete the profile")
	}
	if w := s.do(http.MethodGet, "/family/children/delete?child_id=child-a", anna, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Profil von Alex löschen") {
		t.Fatalf("delete page: %d", w.Code)
	}
	csrf := s.csrf(anna, "/family/children/delete?child_id=child-a")
	if w := s.do(http.MethodPost, "/family/children/delete", anna, url.Values{"csrf_token": {csrf}, "child_id": {"child-a"}, "name": {"Ari"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("wrong name: %d", w.Code)
	}
	if _, ok := s.families.ChildName("tenant-a", "child-a"); !ok {
		t.Fatal("deleted without the name")
	}
	// Another family cannot delete Alex, whatever it types.
	if w := s.do(http.MethodPost, "/family/children/delete", bruno, url.Values{"csrf_token": {s.csrf(bruno, "/family")}, "child_id": {"child-a"}, "name": {"Alex"}}); w.Code != http.StatusForbidden {
		t.Fatalf("foreign guardian: %d", w.Code)
	}
	w := s.do(http.MethodPost, "/family/children/delete", anna, url.Values{"csrf_token": {csrf}, "child_id": {"child-a"}, "name": {"Alex"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family?done=child-deleted" {
		t.Fatalf("delete: %d %q %s", w.Code, w.Header().Get("Location"), w.Body)
	}

	// state checks what must hold from now on, also after a restart.
	state := func(when string) {
		t.Helper()
		home := s.do(http.MethodGet, "/family", anna, nil).Body.String()
		if strings.Contains(home, "Alex") || !strings.Contains(home, "Ari") {
			t.Fatalf("%s: overview: %s", when, home)
		}
		// The device is signed out and cannot sign in again.
		if w := s.do(http.MethodGet, "/child", alex, nil); strings.Contains(w.Body.String(), "Hoi Alex") {
			t.Fatalf("%s: the deleted child's device is still signed in", when)
		}
		if _, ok := s.pairing.DeviceByID(alexDevice); ok {
			t.Fatalf("%s: the device still exists", when)
		}
		if s.contacts.Exists(link) || len(s.contacts.Connections(alexParty)) != 0 {
			t.Fatalf("%s: a contact of the deleted child exists", when)
		}
		// The sibling keeps its contact, so the parents can still write.
		if !s.contacts.Exists(ariLink) || len(s.threads(anna, "Andere Eltern")) != 1 {
			t.Fatalf("%s: the sibling's contact or the parents' chat was removed", when)
		}
		if got := s.looks.Get(appearance.Subject{Kind: appearance.KindChild, TenantID: "tenant-a", ID: "child-a"}); got != appearance.Default() {
			t.Fatalf("%s: the look is left: %+v", when, got)
		}
		for name, content := range s.files() {
			if name == "reports.json" {
				// What Alex reported is gone. What Bea reported about a
				// message of Alex belongs to Bea's family and stays.
				if strings.Contains(content, "Gemein vo Bea") || !strings.Contains(content, "Hoi Bea vo Alex") {
					t.Fatalf("%s: reports: %s", when, content)
				}
				continue
			}
			for _, gone := range []string{"Hoi Bea vo Alex", "Znacht für Alex", alexDevice, `"display_name": "Alex"`, `"child-a"`} {
				if strings.Contains(content, gone) {
					t.Fatalf("%s: %s still holds %q", when, name, gone)
				}
			}
		}
	}
	state("after deletion")
	// The other child lost this contact and its chat, nothing else.
	beaHome := s.do(http.MethodGet, "/child", bea, nil).Body.String()
	if strings.Contains(beaHome, "Alex") || !strings.Contains(beaHome, "Ari") {
		t.Fatalf("the other child's start page: %s", beaHome)
	}
	if page := s.do(http.MethodGet, "/child/chat/"+link, bea, nil); strings.Contains(page.Body.String(), "Hoi Bea vo Alex") {
		t.Fatal("the other child still reads the chat")
	}
	if _, ok := s.pairing.DeviceByID(beaDevice); !ok {
		t.Fatal("the other child lost its device")
	}
	// The other family still sees the message its own child reported.
	if page := s.do(http.MethodGet, "/family/contacts?child_id=child-b", bruno, nil).Body.String(); !strings.Contains(page, "Hoi Bea vo Alex") {
		t.Fatal("the other family no longer sees its report")
	}
	// The invitation that named Alex still registers a family, without an offer.
	if join := s.do(http.MethodGet, "/join?code="+inviteCode, nil, nil); join.Code != http.StatusOK || strings.Contains(join.Body.String(), "Alex") {
		t.Fatalf("invitation that named the deleted child: %d %s", join.Code, join.Body)
	}
	// Nothing comes back with a restart.
	s.start(false)
	anna = s.issue("guardian-a", "tenant-a")
	state("after a restart")
	// Ari is now the only child and goes only with the family.
	last := s.do(http.MethodGet, "/family/children/delete?child_id="+ari.ChildID, anna, nil)
	if last.Code != http.StatusOK || !strings.Contains(last.Body.String(), "einzige Kind") || strings.Contains(last.Body.String(), `action="/family/children/delete"`) {
		t.Fatalf("last child page: %d", last.Code)
	}
	if w := s.do(http.MethodPost, "/family/children/delete", anna, url.Values{"csrf_token": {s.csrf(anna, "/family")}, "child_id": {ari.ChildID}, "name": {"Ari"}}); w.Code != http.StatusConflict {
		t.Fatalf("last child deleted: %d", w.Code)
	}
}

func TestAGuardianDeletesTheFamilyAndCanRegisterAgainEndToEnd(t *testing.T) {
	s := newDeletingServer(t, false)
	anna := s.issue("guardian-a", "tenant-a")
	// A family that registered itself and connected its child with Alex.
	bruno, location := s.register(s.inviteFor(anna, "child-a"), "bruno@example.org", "Leo", "yes")
	if location != "/family?done=connected" {
		t.Fatalf("registration: %q", location)
	}
	connections := s.contacts.Connections(alexParty)
	if len(connections) != 1 {
		t.Fatalf("connections: %+v", connections)
	}
	leo := connections[0].Peer
	guardians := s.families.GuardiansOf(leo.TenantID, leo.ChildID)
	if len(guardians) != 1 {
		t.Fatalf("guardians of the new family: %v", guardians)
	}
	brunoID := guardians[0]
	// Traces: a second, remembered sign-in, a device, both chats, a look and
	// an open invitation.
	tablet := s.remember(brunoID, leo.TenantID)
	leoDevice, leoCookie := s.device(brunoID, leo)
	_, alex := s.device("guardian-a", alexParty)
	var childChat string
	for _, m := range crewThread.FindAllStringSubmatch(s.do(http.MethodGet, "/child", leoCookie, nil).Body.String(), -1) {
		if s.write("/child/chat", leoCookie, m[1], "Hoi Alex vo Leo") == http.StatusSeeOther {
			childChat = m[1]
		}
	}
	if childChat == "" || !strings.Contains(s.read("/child/chat", alex, childChat), "Hoi Alex vo Leo") {
		t.Fatal("the children cannot write to each other")
	}
	parents := s.threads(bruno, "Andere Eltern")
	if len(parents) == 0 || s.write("/family/chat", bruno, parents[0], "Grüsse vo Bruno") != http.StatusSeeOther {
		t.Fatal("the parents cannot write to each other")
	}
	if err := s.looks.Set(appearance.Subject{Kind: appearance.KindGuardian, TenantID: leo.TenantID, ID: brunoID},
		appearance.Preference{Skin: "night", Figure: appearance.Figure{Shape: "ghost", Color: "peach"}}); err != nil {
		t.Fatal(err)
	}
	openInvitation := s.inviteFor(bruno, "")

	// The security page leads to the page; the page asks for the word.
	if page := s.do(http.MethodGet, "/family/security", bruno, nil).Body.String(); !strings.Contains(page, `href="/family/delete"`) {
		t.Fatal("security page does not offer to delete the family")
	}
	if w := s.do(http.MethodGet, "/family/delete", bruno, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Leo") || strings.Contains(w.Body.String(), "Alex") {
		t.Fatalf("delete page: %d %s", w.Code, w.Body)
	}
	csrf := s.csrf(bruno, "/family/delete")
	if w := s.do(http.MethodPost, "/family/delete", bruno, url.Values{"csrf_token": {csrf}, "word": {"ja"}}); w.Code != http.StatusBadRequest || !s.signedIn(bruno) {
		t.Fatalf("wrong word: %d", w.Code)
	}
	// Another family cannot delete this one: the session decides which family is meant.
	if w := s.do(http.MethodPost, "/family/delete", anna, url.Values{"csrf_token": {s.csrf(anna, "/family/delete")}, "word": {"nein"}}); w.Code != http.StatusBadRequest || !s.signedIn(bruno) {
		t.Fatalf("other family: %d", w.Code)
	}
	w := s.do(http.MethodPost, "/family/delete", bruno, url.Values{"csrf_token": {csrf}, "word": {"LÖSCHEN"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?notice=family-deleted" {
		t.Fatalf("delete: %d %q %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	cleared := w.Result().Cookies()
	if len(cleared) != 1 || cleared[0].MaxAge >= 0 || cleared[0].Value != "" {
		t.Fatalf("the session cookie was not dropped: %+v", cleared)
	}
	if page := s.do(http.MethodGet, "/login?notice=family-deleted", nil, nil).Body.String(); !strings.Contains(page, "Familie ist gelöscht") {
		t.Fatal("the deletion is not confirmed")
	}

	state := func(when string) {
		t.Helper()
		if s.signedIn(bruno) || s.signedIn(tablet) {
			t.Fatalf("%s: a guardian of the deleted family is signed in", when)
		}
		if home := s.do(http.MethodGet, "/child", leoCookie, nil); strings.Contains(home.Body.String(), "Hoi Leo") {
			t.Fatalf("%s: a device of the deleted family is signed in", when)
		}
		if _, ok := s.pairing.DeviceByID(leoDevice); ok {
			t.Fatalf("%s: a device of the deleted family exists", when)
		}
		if len(s.contacts.Connections(alexParty)) != 0 || len(s.threads(anna, "Andere Eltern")) != 0 {
			t.Fatalf("%s: the other family still has a contact or a parents' chat", when)
		}
		if page := s.do(http.MethodGet, "/family/contacts?child_id=child-a", anna, nil).Body.String(); strings.Contains(page, "Leo") {
			t.Fatalf("%s: the other family still sees the deleted child", when)
		}
		// No file names the family, a guardian, a child or an address.
		for name, content := range s.files() {
			for _, gone := range []string{leo.TenantID, leo.ChildID, brunoID, "bruno@example.org", "Hoi Alex vo Leo", "Grüsse vo Bruno", leoDevice} {
				if strings.Contains(content, gone) {
					t.Fatalf("%s: %s still holds %q", when, name, gone)
				}
			}
		}
		if w := s.do(http.MethodGet, "/join?code="+openInvitation, nil, nil); w.Code != http.StatusNotFound {
			t.Fatalf("%s: an invitation of the deleted family still works: %d", when, w.Code)
		}
		// The other family is untouched.
		if !s.signedIn(anna) || !strings.Contains(s.do(http.MethodGet, "/family", anna, nil).Body.String(), "Alex") {
			t.Fatalf("%s: the other family was affected", when)
		}
	}
	state("after deletion")
	s.mailer.links = nil
	if s.requestLink("bruno@example.org") != "" {
		t.Fatal("a deleted guardian gets a sign-in link")
	}
	s.start(false)
	anna = s.issue("guardian-a", "tenant-a")
	state("after a restart")

	// The address is free: the same person registers again, as a new family.
	s.mailer.registrations = nil
	again, _ := s.register(s.inviteFor(anna, ""), "bruno@example.org", "Leo", "")
	home := s.do(http.MethodGet, "/family", again, nil).Body.String()
	if !strings.Contains(home, "Leo") || strings.Contains(home, leo.ChildID) {
		t.Fatalf("second registration: %s", home)
	}
	if len(s.contacts.Connections(alexParty)) != 0 {
		t.Fatal("the new family inherited a contact")
	}
}

func TestDeletingAFamilyLiftsItsLocksAndTheLastFamilyStays(t *testing.T) {
	s := newDeletingServer(t, true)
	anna, ben, bruno := s.issue("guardian-a", "tenant-a"), s.issue("guardian-a2", "tenant-a"), s.issue("guardian-b", "tenant-b")
	if w := s.act(ben, "lock", url.Values{"confirm": {"yes"}}); w.Code != http.StatusOK {
		t.Fatalf("lock: %d", w.Code)
	}
	if !strings.Contains(s.files()["guardian-locks.json"], "guardian-a2") {
		t.Fatal("no lock to begin with")
	}
	// A sign-in link that is still in Anna's mailbox.
	pending := s.requestLink("anna@example.org")
	if pending == "" {
		t.Fatal("no sign-in link")
	}
	// Anna answers for every child of the family and deletes it; the other
	// guardian, who is locked, is neither asked nor told.
	w := s.do(http.MethodPost, "/family/delete", anna, url.Values{"csrf_token": {s.csrf(anna, "/family/delete")}, "word": {"löschen"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?notice=family-deleted" {
		t.Fatalf("delete: %d %q", w.Code, w.Header().Get("Location"))
	}
	files := s.files()
	for _, name := range []string{"guardian-locks.json", "guardians.json", "families.json"} {
		for _, gone := range []string{"tenant-a", "anna@example.org", "ben@example.org"} {
			if strings.Contains(files[name], gone) {
				t.Fatalf("%s still holds %q: %s", name, gone, files[name])
			}
		}
	}
	if s.signedIn(anna) || s.signedIn(ben) || !s.signedIn(bruno) {
		t.Fatal("wrong guardians are signed in")
	}
	if w := s.do(http.MethodPost, "/login/verify", nil, url.Values{"token": {pending}}); w.Code != http.StatusForbidden || len(w.Result().Cookies()) != 0 {
		t.Fatalf("a link sent before the deletion still signs in: %d", w.Code)
	}
	// Family b is the last one now. Somebody must be left who can invite.
	page := s.do(http.MethodGet, "/family/delete", bruno, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "letzte Familie") || strings.Contains(page.Body.String(), `action="/family/delete"`) {
		t.Fatalf("last family page: %d %s", page.Code, page.Body)
	}
	if w := s.do(http.MethodPost, "/family/delete", bruno, url.Values{"csrf_token": {s.csrf(bruno, "/family")}, "word": {"LÖSCHEN"}}); w.Code != http.StatusConflict || !s.signedIn(bruno) {
		t.Fatalf("last family deleted: %d", w.Code)
	}
}

// An equal guardian of one child may delete that child, but not a family in
// which there is a child they do not answer for.
func TestDeletionFollowsTheGuardiansAuthority(t *testing.T) {
	s := newDeletingServer(t, false)
	anna, ben := s.issue("guardian-a", "tenant-a"), s.issue("guardian-a2", "tenant-a")
	if w := s.do(http.MethodPost, "/family/children", anna, url.Values{"csrf_token": {s.csrf(anna, "/family/children")}, "name": {"Ari"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("second child: %d", w.Code)
	}
	if w := s.do(http.MethodGet, "/family/delete", ben, nil); w.Code != http.StatusForbidden {
		t.Fatalf("family page for a guardian of one child: %d", w.Code)
	}
	if w := s.do(http.MethodPost, "/family/delete", ben, url.Values{"csrf_token": {s.csrf(ben, "/family")}, "word": {"LÖSCHEN"}}); w.Code != http.StatusForbidden {
		t.Fatalf("family deleted by a guardian of one child: %d", w.Code)
	}
	if len(s.families.Cards("guardian-a", "tenant-a")) != 2 {
		t.Fatal("something was deleted")
	}
	w := s.do(http.MethodPost, "/family/children/delete", ben, url.Values{"csrf_token": {s.csrf(ben, "/family")}, "child_id": {"child-a"}, "name": {"Alex"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("equal guardian deletes the child: %d", w.Code)
	}
	// Alex was Ben's only child: Ben's session ends with the profile.
	if s.signedIn(ben) || !s.signedIn(anna) {
		t.Fatal("wrong guardians are signed in")
	}
}

func TestWithoutSelfRegistrationNothingCanBeDeleted(t *testing.T) {
	s := newChatServer(t, false)
	anna := s.issue("guardian-a", "tenant-a")
	if page := s.do(http.MethodGet, "/family", anna, nil).Body.String(); strings.Contains(page, "/family/children/delete") {
		t.Fatal("deleting a profile is offered")
	}
	if page := s.do(http.MethodGet, "/family/security", anna, nil).Body.String(); strings.Contains(page, "/family/delete") {
		t.Fatal("deleting the family is offered")
	}
	for _, target := range []string{"/family/children/delete?child_id=child-a", "/family/delete"} {
		if w := s.do(http.MethodGet, target, anna, nil); w.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d", target, w.Code)
		}
	}
	csrf := s.csrf(anna, "/family")
	for target, form := range map[string]url.Values{
		"/family/children/delete": {"csrf_token": {csrf}, "child_id": {"child-a"}, "name": {"Alex"}},
		"/family/delete":          {"csrf_token": {csrf}, "word": {"LÖSCHEN"}},
	} {
		if w := s.do(http.MethodPost, target, anna, form); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: %d", target, w.Code)
		}
	}
	if _, ok := s.families.ChildName("tenant-a", "child-a"); !ok || !s.signedIn(anna) {
		t.Fatal("something was deleted without the switch")
	}
}

// While a child is deleted, the family's invitations must stay: if a later
// step fails the family still exists and still needs them.
func TestDeletingAChildLeavesTheFamilysInvitationsAlone(t *testing.T) {
	s := newDeletingServer(t, false)
	anna := s.issue("guardian-a", "tenant-a")
	open := s.inviteFor(anna, "child-a")
	plain := s.inviteFor(anna, "")
	invitations, err := signup.Open(s.signupPath, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	early := invitationsOfChild{invitations}
	if err := early.PurgeTenant("tenant-a"); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{open, plain} {
		if _, err := invitations.Lookup(code); err != nil {
			t.Fatalf("the family's invitation was removed before the family: %v", err)
		}
	}
	if err := early.PurgeChild("tenant-a", "child-a"); err != nil {
		t.Fatal(err)
	}
	if inv, err := invitations.Lookup(open); err != nil || inv.Child != "" {
		t.Fatalf("the invitation still names the child: %+v %v", inv, err)
	}
}
