package main

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/guardianchat"
	"github.com/pblumer/yapuno/internal/guardiannames"
)

// newNamingServer runs both chats, invitations and guardian names.
func newNamingServer(t *testing.T) *chatServer {
	t.Helper()
	s := newSecurityServer(t, false)
	s.signupPath = filepath.Join(s.root, "signup.json")
	s.chatPath = filepath.Join(s.root, "guardian-chat.log")
	s.namesPath = filepath.Join(s.root, "guardian-names.json")
	s.start(false)
	return &chatServer{securityServer: s}
}

// setNames saves a guardian's names through the real page.
func (s *chatServer) setNames(cookie *http.Cookie, forChildren, forAdults string) {
	s.t.Helper()
	w := s.do(http.MethodPost, "/family/name", cookie, url.Values{"csrf_token": {s.csrf(cookie, "/family/name")}, "for_children": {forChildren}, "for_adults": {forAdults}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family/name?done=saved" {
		s.t.Fatalf("save names: %d %s", w.Code, w.Body)
	}
}

func TestGuardiansAreShownByTheNamesTheyChoseEndToEnd(t *testing.T) {
	s := newNamingServer(t)
	anna, bruno := s.issue("guardian-a", "tenant-a"), s.issue("guardian-b", "tenant-b")
	s.connect("guardian-a", alexParty, "guardian-b", beaParty)
	// Before anybody chose a name.
	if page := s.do(http.MethodGet, "/family", anna, nil).Body.String(); !strings.Contains(page, `href="/family/name"`) || !strings.Contains(page, "Noch kein Name") {
		t.Fatalf("overview: %s", page)
	}
	if w := s.do(http.MethodGet, "/family/name", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("name page without a session: %d", w.Code)
	}
	s.setNames(anna, "Mami", "Anna Muster")
	if page := s.do(http.MethodGet, "/family", anna, nil).Body.String(); !strings.Contains(page, "Mami") || !strings.Contains(page, "Anna Muster") {
		t.Fatal("the overview does not show the guardian's own names")
	}

	check := func(when string) {
		t.Helper()
		// Alex sees the name for the children, not the one for adults.
		_, alex := s.device("guardian-a", alexParty)
		home := s.do(http.MethodGet, "/child", alex, nil).Body.String()
		if !strings.Contains(home, "Mami") || strings.Contains(home, "Anna Muster") {
			t.Fatalf("%s: the child's start page: %s", when, home)
		}
		named := false
		for _, m := range childThread.FindAllStringSubmatch(home, -1) {
			if strings.Contains(s.read("/child/chat", alex, m[1]), "<h1>Mami</h1>") {
				named = true
			}
		}
		if !named {
			t.Fatalf("%s: the child's chat is not headed by the chosen name", when)
		}
		// The other guardian of Alex chose no name and stays "Elternteil".
		if !strings.Contains(home, "Elternteil") {
			t.Fatalf("%s: a guardian without a name lost the neutral label: %s", when, home)
		}
		// The other family's guardian sees the name for adults with the relation.
		inbox := s.do(http.MethodGet, "/family/chat", bruno, nil).Body.String()
		if !strings.Contains(inbox, "Anna Muster") || !strings.Contains(inbox, "Elternteil von Alex") || strings.Contains(inbox, "Mami") {
			t.Fatalf("%s: the other guardian's inbox: %s", when, inbox)
		}
		// The other family's child sees neither name.
		_, bea := s.device("guardian-b", beaParty)
		beaHome := s.do(http.MethodGet, "/child", bea, nil).Body.String()
		if strings.Contains(beaHome, "Mami") || strings.Contains(beaHome, "Anna Muster") {
			t.Fatalf("%s: a child of another family sees a guardian's name: %s", when, beaHome)
		}
	}
	check("after saving")
	s.start(false)
	anna, bruno = s.issue("guardian-a", "tenant-a"), s.issue("guardian-b", "tenant-b")
	check("after a restart")

	// A guardian changes only their own names, whatever the form says.
	s.do(http.MethodPost, "/family/name", bruno, url.Values{"csrf_token": {s.csrf(bruno, "/family/name")}, "for_children": {""}, "for_adults": {"Bruno"},
		"guardian_id": {"guardian-a"}, "tenant_id": {"tenant-a"}})
	if got := s.names.Get("tenant-a", "guardian-a"); got != (guardiannames.Names{ForChildren: "Mami", ForAdults: "Anna Muster"}) {
		t.Fatalf("another guardian changed Anna's names: %+v", got)
	}
	if page := s.do(http.MethodGet, "/family/chat", anna, nil).Body.String(); !strings.Contains(page, "Bruno") || !strings.Contains(page, "Elternteil von Bea") {
		t.Fatalf("Anna's inbox: %s", page)
	}
	// Removing a name brings the neutral label back.
	s.setNames(anna, "Mami", "")
	if inbox := s.do(http.MethodGet, "/family/chat", bruno, nil).Body.String(); strings.Contains(inbox, "Anna Muster") || !strings.Contains(inbox, "Elternteil von Alex") {
		t.Fatalf("after removing the name: %s", inbox)
	}
	// Once the children are no longer connected the other family sees nothing.
	s.setNames(anna, "Mami", "Anna Muster")
	if err := s.contacts.PurgeChild("tenant-a", "child-a"); err != nil {
		t.Fatal(err)
	}
	if inbox := s.do(http.MethodGet, "/family/chat", bruno, nil).Body.String(); strings.Contains(inbox, "Anna Muster") {
		t.Fatal("a family that is no longer connected still sees the name")
	}
}

func TestANameGivenAtRegistrationIsShownToTheInvitingFamilyAndGoesWithTheFamily(t *testing.T) {
	s := newNamingServer(t)
	anna := s.issue("guardian-a", "tenant-a")
	code := s.inviteFor(anna, "child-a")
	if join := s.do(http.MethodGet, "/join?code="+code, nil, nil).Body.String(); !strings.Contains(join, `name="guardian_name"`) || !strings.Contains(join, "freiwillig") {
		t.Fatalf("join page: %s", join)
	}
	if w := s.do(http.MethodPost, "/join/request", nil, url.Values{"code": {code}, "email": {"bruno@example.org"}, "child_name": {"Leo"}, "confirm": {"yes"}, "guardian_name": {"Bruno Beispiel"}}); w.Code != http.StatusOK {
		t.Fatalf("join request: %d", w.Code)
	}
	link, err := url.Parse(s.mailer.registration("bruno@example.org"))
	if err != nil {
		t.Fatal(err)
	}
	w := s.do(http.MethodPost, "/join/verify", nil, url.Values{"token": {link.Query().Get("token")}, "connect": {"yes"}})
	if w.Code != http.StatusSeeOther || len(w.Result().Cookies()) != 1 {
		t.Fatalf("verify: %d", w.Code)
	}
	bruno := w.Result().Cookies()[0]
	inbox := s.do(http.MethodGet, "/family/chat", anna, nil).Body.String()
	if !strings.Contains(inbox, "Bruno Beispiel") || !strings.Contains(inbox, "Elternteil von Leo") {
		t.Fatalf("the inviting guardian's inbox: %s", inbox)
	}
	if page := s.do(http.MethodGet, "/family/name", bruno, nil).Body.String(); !strings.Contains(page, `name="for_adults" value="Bruno Beispiel"`) || !strings.Contains(page, `name="for_children" value=""`) {
		t.Fatalf("the new guardian's name page: %s", page)
	}
	// Deleting the family removes the name with everything else.
	del := s.do(http.MethodPost, "/family/delete", bruno, url.Values{"csrf_token": {s.csrf(bruno, "/family/delete")}, "word": {"LÖSCHEN"}})
	if del.Code != http.StatusSeeOther || del.Header().Get("Location") != "/login?notice=family-deleted" {
		t.Fatalf("delete: %d %q", del.Code, del.Header().Get("Location"))
	}
	for name, content := range s.files() {
		if strings.Contains(content, "Bruno Beispiel") {
			t.Fatalf("%s still holds the name of a deleted guardian", name)
		}
	}
	if inbox := s.do(http.MethodGet, "/family/chat", anna, nil).Body.String(); strings.Contains(inbox, "Bruno") {
		t.Fatal("the name of a deleted guardian is still shown")
	}
}

func TestWithoutANameFileGuardiansHaveNoNames(t *testing.T) {
	s := newInvitingServer(t)
	anna := s.issue("guardian-a", "tenant-a")
	overview := s.do(http.MethodGet, "/family", anna, nil).Body.String()
	if strings.Contains(overview, "/family/name") || !strings.Contains(overview, "Namen für Eltern") {
		t.Fatalf("overview: %s", overview)
	}
	if w := s.do(http.MethodGet, "/family/name", anna, nil); w.Code != http.StatusNotFound {
		t.Fatalf("name page: %d", w.Code)
	}
	if w := s.do(http.MethodPost, "/family/name", anna, url.Values{"csrf_token": {s.csrf(anna, "/family")}, "for_children": {"Mami"}, "for_adults": {"Anna"}}); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("saving a name: %d", w.Code)
	}
	if join := s.do(http.MethodGet, "/join?code="+s.inviteFor(anna, ""), nil, nil).Body.String(); strings.Contains(join, "guardian_name") {
		t.Fatal("the registration asks for a name")
	}
	with := newNamingServer(t)
	if page := with.do(http.MethodGet, "/family", with.issue("guardian-a", "tenant-a"), nil).Body.String(); strings.Contains(page, "Namen für Eltern") {
		t.Fatal("names are listed as switched off although they are on")
	}
}

// Names are only shown in the guardians' chat, so they need it.
func TestGuardianNamesNeedTheGuardianChat(t *testing.T) {
	s := newSecurityServer(t, false)
	names, err := guardiannames.Open(filepath.Join(s.root, "guardian-names.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newHandler("https://app.example.org", s.families, s.guardians, s.mailer, s.sessions, optionalStores{pairing: s.pairing, guardianNames: names}); err == nil {
		t.Fatal("names accepted without the guardians' chat")
	}
	chat, err := guardianchat.Open(filepath.Join(s.root, "guardian-chat.log"), s.families, noConnections{}, s.guardians, s.pairing, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer chat.Close()
	if _, err := newHandler("https://app.example.org", s.families, s.guardians, s.mailer, s.sessions, optionalStores{pairing: s.pairing, guardianNames: names, guardianChat: chat}); err != nil {
		t.Fatalf("names with the guardians' chat: %v", err)
	}
}
