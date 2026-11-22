package main

import (
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/signup"
)

var contactLink = regexp.MustCompile(`name="link_id" value="([0-9a-f]{32})"`)

// newInvitingServer runs invitations together with contacts and both chats,
// like the trial installation.
func newInvitingServer(t *testing.T) *chatServer {
	t.Helper()
	s := newSecurityServer(t, false)
	s.signupPath = filepath.Join(s.root, "signup.json")
	s.chatPath = filepath.Join(s.root, "guardian-chat.log")
	s.start(false)
	return &chatServer{securityServer: s}
}

// inviteFor creates an invitation through the real page; child is the
// inviting child, or "" for an invitation that connects nobody.
func (s *securityServer) inviteFor(cookie *http.Cookie, child string) string {
	s.t.Helper()
	w := s.do(http.MethodPost, "/family/invite", cookie, url.Values{"csrf_token": {s.csrf(cookie, "/family/invite")}, "child_id": {child}})
	m := inviteLink.FindStringSubmatch(w.Body.String())
	if w.Code != http.StatusOK || m == nil {
		s.t.Fatalf("invitation: %d %s", w.Code, w.Body)
	}
	return m[1]
}

// register completes a registration and returns the new guardian's session
// and where the browser is sent.
func (s *securityServer) register(code, email, child, connect string) (*http.Cookie, string) {
	s.t.Helper()
	token := s.join(code, email, child)
	if token == "" {
		s.t.Fatal("no confirmation mail")
	}
	form := url.Values{"token": {token}}
	if connect != "" {
		form.Set("connect", connect)
	}
	w := s.do(http.MethodPost, "/join/verify", nil, form)
	cookies := w.Result().Cookies()
	if w.Code != http.StatusSeeOther || len(cookies) != 1 {
		s.t.Fatalf("verify: %d %s", w.Code, w.Body)
	}
	return cookies[0], w.Header().Get("Location")
}

func TestOneInvitationRegistersAFamilyAndConnectsTheChildren(t *testing.T) {
	s := newInvitingServer(t)
	anna := s.issue("guardian-a", "tenant-a")
	if page := s.do(http.MethodGet, "/family/invite", anna, nil).Body.String(); !strings.Contains(page, "Ja, mit Alex") || !strings.Contains(page, "Eltern beider Familien") {
		t.Fatalf("invite page: %s", page)
	}
	code := s.inviteFor(anna, "child-a")
	if join := s.do(http.MethodGet, "/join?code="+code, nil, nil).Body.String(); !strings.Contains(join, "Familie von Alex") || !strings.Contains(join, "Eltern beider Familien") {
		t.Fatalf("join page: %s", join)
	}
	token := s.join(code, "bruno@example.org", "Leo")
	if confirm := s.do(http.MethodGet, "/join/verify?token="+token, nil, nil).Body.String(); !strings.Contains(confirm, "Dürfen Leo und Alex miteinander schreiben?") {
		t.Fatalf("confirm page: %s", confirm)
	}
	w := s.do(http.MethodPost, "/join/verify", nil, url.Values{"token": {token}, "connect": {"yes"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family?done=connected" || len(w.Result().Cookies()) != 1 {
		t.Fatalf("verify: %d %q", w.Code, w.Header().Get("Location"))
	}
	bruno := w.Result().Cookies()[0]
	home := s.do(http.MethodGet, "/family?done=connected", bruno, nil).Body.String()
	first := childLink.FindStringSubmatch(home)
	if first == nil || !strings.Contains(home, "Kinder sind verbunden") {
		t.Fatalf("new family page: %s", home)
	}
	alex, leo := contacts.Party{TenantID: "tenant-a", ChildID: "child-a"}, contacts.Party{ChildID: first[1]}
	for _, c := range s.contacts.Connections(alex) {
		leo.TenantID = c.Peer.TenantID
	}
	if leo.TenantID == "" || !s.contacts.Allowed(alex, leo) || !s.contacts.Allowed(leo, alex) {
		t.Fatal("the children are not contacts")
	}
	// Only the named child is connected, and only with this family.
	if s.contacts.Allowed(contacts.Party{TenantID: "tenant-b", ChildID: "child-b"}, leo) {
		t.Fatal("an uninvolved child was connected")
	}
	// Each family sees the other child by first name on its contact page.
	own := s.do(http.MethodGet, "/family/contacts?child_id=child-a", anna, nil).Body.String()
	other := s.do(http.MethodGet, "/family/contacts?child_id="+first[1], bruno, nil).Body.String()
	if !strings.Contains(own, "Leo") || !strings.Contains(other, "Alex") {
		t.Fatalf("contact pages: %s\n%s", own, other)
	}
	// The parents can write to each other at once. Alex has two guardians,
	// so the new guardian reaches both, as with any other contact.
	annaThreads, brunoThreads := s.threads(anna, "Andere Eltern"), s.threads(bruno, "Andere Eltern")
	if len(annaThreads) != 1 || len(brunoThreads) != 2 || (annaThreads[0] != brunoThreads[0] && annaThreads[0] != brunoThreads[1]) {
		t.Fatalf("parents' threads: %v %v", annaThreads, brunoThreads)
	}
	if code := s.write("/family/chat", bruno, annaThreads[0], "Hoi, danke für die Einladung"); code != http.StatusSeeOther {
		t.Fatalf("write: %d", code)
	}
	if !strings.Contains(s.read("/family/chat", anna, annaThreads[0]), "danke für die Einladung") {
		t.Fatal("the inviting guardian does not see the message")
	}
	// The invited family ends the contact like any other.
	link := contactLink.FindStringSubmatch(other)
	if link == nil {
		t.Fatal("no contact to remove")
	}
	removed := s.do(http.MethodPost, "/family/contacts/remove", bruno, url.Values{"csrf_token": {s.csrf(bruno, "/family/contacts?child_id="+first[1])}, "child_id": {first[1]}, "link_id": {link[1]}, "confirm": {"yes"}})
	if removed.Code != http.StatusSeeOther || s.contacts.Allowed(alex, leo) || len(s.threads(anna, "Andere Eltern")) != 0 {
		t.Fatalf("removal: %d", removed.Code)
	}
}

func TestAnInvitationConnectsNobodyWithoutBothApprovals(t *testing.T) {
	s := newInvitingServer(t)
	anna := s.issue("guardian-a", "tenant-a")
	alex := contacts.Party{TenantID: "tenant-a", ChildID: "child-a"}
	for name, c := range map[string]struct{ child, connect, email string }{
		"invited guardian declines":         {"child-a", "no", "bruno@example.org"},
		"invited guardian does not answer":  {"child-a", "", "carla@example.org"},
		"inviting guardian named no child":  {"", "yes", "dario@example.org"},
		"inviting guardian named no child2": {"", "", "elif@example.org"},
	} {
		_, location := s.register(s.inviteFor(anna, c.child), c.email, "Leo", c.connect)
		if location != "/family" {
			t.Errorf("%s: sent to %q", name, location)
		}
	}
	if got := s.contacts.Connections(alex); len(got) != 0 {
		t.Fatalf("connected without both approvals: %+v", got)
	}
	if page := s.do(http.MethodGet, "/family/contacts?child_id=child-a", anna, nil).Body.String(); strings.Contains(page, "Leo") {
		t.Fatal("the inviting family sees a child whose guardian did not agree")
	}
	if len(s.threads(anna, "Andere Eltern")) != 0 {
		t.Fatal("parents were connected")
	}
}

// An invitation names only the guardian's own child, and the pages show that
// child under its current name.
func TestAnInvitationNamesOnlyAnOwnChildUnderItsCurrentName(t *testing.T) {
	s := newInvitingServer(t)
	anna := s.issue("guardian-a", "tenant-a")
	code := s.inviteFor(anna, "child-a")
	// Another guardian's child cannot be named at all.
	w := s.do(http.MethodPost, "/family/invite", anna, url.Values{"csrf_token": {s.csrf(anna, "/family/invite")}, "child_id": {"child-b"}})
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign child named: %d", w.Code)
	}
	// Alex's profile is renamed meanwhile: the pages show the current name.
	if err := s.families.RenameChild("guardian-a", "tenant-a", "child-a", "Sascha"); err != nil {
		t.Fatal(err)
	}
	if join := s.do(http.MethodGet, "/join?code="+code, nil, nil).Body.String(); !strings.Contains(join, "Familie von Sascha") || strings.Contains(join, "Alex") {
		t.Fatalf("join page: %s", join)
	}
}

// Invitations without contacts on the installation stay what they were.
func TestWithoutContactsTheInvitePageOffersNoConnection(t *testing.T) {
	s := newSignupServer(t, false)
	anna := s.issue("guardian-a", "tenant-a")
	if page := s.do(http.MethodGet, "/family/invite", anna, nil).Body.String(); strings.Contains(page, `name="child_id"`) {
		t.Fatal("a connection is offered although contacts are off")
	}
	code := s.invite(anna)
	token := s.join(code, "bruno@example.org", "Leo")
	if page := s.do(http.MethodGet, "/join/verify?token="+token, nil, nil).Body.String(); strings.Contains(page, `name="connect"`) {
		t.Fatal("the registration offers a connection although contacts are off")
	}
	if w := s.do(http.MethodPost, "/join/verify", nil, url.Values{"token": {token}, "connect": {"yes"}}); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family" {
		t.Fatalf("verify: %d %q", w.Code, w.Header().Get("Location"))
	}
}

// deviceOnly shows a guardian who manages the devices of Alex but may not
// approve contacts. The family store cannot hold such a grant yet; the offer
// must not depend on that.
type deviceOnly struct{}

func (deviceOnly) Cards(string, string) []familydashboard.ChildCard {
	return []familydashboard.ChildCard{{ID: "child-a", DisplayName: "Alex", Permissions: familydashboard.Permissions{ManageDevices: true}}}
}

// The offer on the registration pages follows the inviting guardian's
// current authority for exactly the named child.
func TestTheOfferNeedsContactAuthorityForTheNamedChild(t *testing.T) {
	s := newInvitingServer(t)
	offer := invitedContacts{families: s.families, contacts: s.contacts}
	if name, ok := offer.Inviting("guardian-a", "tenant-a", "child-a"); !ok || name != "Alex" {
		t.Fatalf("guardian with authority: %q %v", name, ok)
	}
	for name, who := range map[string][3]string{
		"unknown child":           {"guardian-a", "tenant-a", "ghost"},
		"child of another family": {"guardian-a", "tenant-a", "child-b"},
		"guardian of another":     {"guardian-b", "tenant-a", "child-a"},
		"own child, wrong tenant": {"guardian-a", "tenant-b", "child-a"},
		"empty child":             {"guardian-a", "tenant-a", ""},
	} {
		if got, ok := offer.Inviting(who[0], who[1], who[2]); ok || got != "" {
			t.Errorf("%s: %q %v", name, got, ok)
		}
	}
	limited := invitedContacts{families: deviceOnly{}, contacts: s.contacts}
	if got, ok := limited.Inviting("guardian-d", "tenant-a", "child-a"); ok || got != "" {
		t.Fatalf("offer without contact authority: %q %v", got, ok)
	}
	// The contact store decides: without authority nothing is connected.
	if err := offer.Connect("guardian-b", "tenant-a", "child-a", "guardian-b", "tenant-b", "child-b"); !errors.Is(err, contacts.ErrForbidden) {
		t.Fatalf("connected without authority for the inviting child: %v", err)
	}
	if err := offer.Connect("guardian-a", "tenant-a", "child-a", "guardian-b", "tenant-b", "child-b"); err != nil {
		t.Fatal(err)
	}
	if !s.contacts.Allowed(contacts.Party{TenantID: "tenant-a", ChildID: "child-a"}, contacts.Party{TenantID: "tenant-b", ChildID: "child-b"}) {
		t.Fatal("not connected")
	}
}

// Contacts without the guardians' chat: children can be connected, but no
// page promises that parents can write to each other.
func TestWithoutTheGuardianChatNoParentsChannelIsPromised(t *testing.T) {
	s := newSecurityServer(t, false)
	invitations, err := signup.Open(filepath.Join(s.root, "signup.json"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	contactStore, err := contacts.Open(filepath.Join(s.root, "contacts.json"), s.families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if s.handler, err = newHandler("https://app.example.org", s.families, s.guardians, s.mailer, s.sessions, optionalStores{pairing: s.pairing, signup: invitations, contacts: contactStore}); err != nil {
		t.Fatal(err)
	}
	anna := s.issue("guardian-a", "tenant-a")
	invite := s.do(http.MethodGet, "/family/invite", anna, nil).Body.String()
	if !strings.Contains(invite, "Ja, mit Alex") || strings.Contains(invite, "Eltern beider Familien") {
		t.Fatalf("invite page: %s", invite)
	}
	code := s.inviteFor(anna, "child-a")
	if join := s.do(http.MethodGet, "/join?code="+code, nil, nil).Body.String(); !strings.Contains(join, "Familie von Alex") || strings.Contains(join, "Eltern beider Familien") {
		t.Fatalf("join page: %s", join)
	}
	token := s.join(code, "bruno@example.org", "Leo")
	if confirm := s.do(http.MethodGet, "/join/verify?token="+token, nil, nil).Body.String(); !strings.Contains(confirm, `name="connect" value="yes"`) || strings.Contains(confirm, "Eltern beider Familien") {
		t.Fatalf("confirm page: %s", confirm)
	}
	if w := s.do(http.MethodPost, "/join/verify", nil, url.Values{"token": {token}, "connect": {"yes"}}); w.Header().Get("Location") != "/family?done=connected" {
		t.Fatalf("verify: %d %q", w.Code, w.Header().Get("Location"))
	}
	if len(contactStore.Connections(contacts.Party{TenantID: "tenant-a", ChildID: "child-a"})) != 1 {
		t.Fatal("the children are not connected")
	}
}
