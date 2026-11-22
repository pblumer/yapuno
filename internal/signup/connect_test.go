package signup

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const (
	connectButton = `name="connect" value="yes"`
	plainButton   = `name="connect" value="no"`
)

// The invitation names Lea. The invited guardian sees that before giving an
// address, and decides in the last step, by pressing one of two buttons.
func TestAnInvitationThatNamesAChildOffersToConnectTheChildren(t *testing.T) {
	r := newConnectingRig(t)
	code, _ := r.inviteFor(t, "lea")

	join := r.get("/join?code=" + code).Body.String()
	for _, want := range []string{"Familie von Lea", "letzten Schritt", "nur, wenn du die Kinder verbindest", "Eltern beider Familien", "jederzeit"} {
		if !strings.Contains(join, want) {
			t.Errorf("join page lacks %q", want)
		}
	}
	// Nothing is agreed on the first page, and nothing is promised falsely.
	for _, not := range []string{`name="connect"`, "nicht automatisch Kontakte", "weder deine Adresse noch den Namen"} {
		if strings.Contains(join, not) {
			t.Errorf("join page contains %q", not)
		}
	}
	token := r.confirmed(t, code, "bruno@example.org", "Leo")
	page := r.get("/join/verify?token=" + token)
	body := page.Body.String()
	if page.Code != http.StatusOK || strings.Count(body, `<form method="post" action="/join/verify"`) != 1 {
		t.Fatalf("confirm page: %d %s", page.Code, body)
	}
	for _, want := range []string{"Leo", "Lea", connectButton, plainButton, "Familie anlegen und Kinder verbinden", "Nur Familie anlegen", `name="token" value="` + token + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("confirm page lacks %q", want)
		}
	}
	// Looking at the page neither registers nor connects.
	if len(r.families.created) != 0 || len(r.connections.made) != 0 {
		t.Fatal("showing the page changed something")
	}
	done := r.post("/join/verify", url.Values{"token": {token}, "connect": {"yes"}})
	if done.Code != http.StatusSeeOther || done.Header().Get("Location") != "/family?done=connected" {
		t.Fatalf("verify: %d %q", done.Code, done.Header().Get("Location"))
	}
	if len(r.families.created) != 1 || len(r.connections.made) != 1 {
		t.Fatalf("families %+v, connections %+v", r.families.created, r.connections.made)
	}
	f := r.families.created[0]
	if got := r.connections.made[0]; got != (connection{"anna", "family-a", "lea", f.guardian, f.tenant, f.child}) {
		t.Fatalf("connection: %+v for family %+v", got, f)
	}
	if len(r.sessions.issued) != 1 {
		t.Fatal("not signed in")
	}
}

func TestTheInvitedGuardianMayDeclineTheConnection(t *testing.T) {
	for name, form := range map[string]url.Values{
		"declined":      {"connect": {"no"}},
		"no choice":     {},
		"empty choice":  {"connect": {""}},
		"other value":   {"connect": {"YES"}},
		"padded choice": {"connect": {" yes"}},
	} {
		r := newConnectingRig(t)
		code, _ := r.inviteFor(t, "lea")
		form.Set("token", r.confirmed(t, code, "bruno@example.org", "Leo"))
		done := r.post("/join/verify", form)
		if done.Code != http.StatusSeeOther || done.Header().Get("Location") != "/family" {
			t.Errorf("%s: %d %q", name, done.Code, done.Header().Get("Location"))
		}
		if len(r.families.created) != 1 || len(r.connections.made) != 0 {
			t.Errorf("%s: families %d, connections %d", name, len(r.families.created), len(r.connections.made))
		}
	}
}

// The inviting guardian approved nothing unless the invitation names a child.
func TestAnInvitationWithoutAChildConnectsNobody(t *testing.T) {
	r := newConnectingRig(t)
	code, _ := r.invite(t)
	join := r.get("/join?code=" + code).Body.String()
	if strings.Contains(join, "Lea") || !strings.Contains(join, "nicht automatisch Kontakte") || !strings.Contains(join, "weder deine Adresse noch den Namen") {
		t.Fatalf("join page: %s", join)
	}
	token := r.confirmed(t, code, "bruno@example.org", "Leo")
	if body := r.get("/join/verify?token=" + token).Body.String(); strings.Contains(body, `name="connect"`) || !strings.Contains(body, "Jetzt Familie anlegen") {
		t.Fatalf("confirm page offers a connection: %s", body)
	}
	// A crafted form cannot obtain what the inviting guardian did not approve.
	done := r.post("/join/verify", url.Values{"token": {token}, "connect": {"yes"}})
	if done.Code != http.StatusSeeOther || done.Header().Get("Location") != "/family" || len(r.connections.made) != 0 || len(r.families.created) != 1 {
		t.Fatalf("verify: %d %q, connections %+v", done.Code, done.Header().Get("Location"), r.connections.made)
	}
}

// Without contacts on this installation an invitation only registers.
func TestWithoutContactsNoConnectionIsOffered(t *testing.T) {
	r := newRig(t)
	code, _ := r.inviteFor(t, "lea")
	if join := r.get("/join?code=" + code).Body.String(); !strings.Contains(join, "nicht automatisch Kontakte") || strings.Contains(join, "letzten Schritt") {
		t.Fatalf("join page: %s", join)
	}
	token := r.confirmed(t, code, "bruno@example.org", "Leo")
	if body := r.get("/join/verify?token=" + token).Body.String(); strings.Contains(body, `name="connect"`) {
		t.Fatal("confirm page offers a connection")
	}
	done := r.post("/join/verify", url.Values{"token": {token}, "connect": {"yes"}})
	if done.Code != http.StatusSeeOther || done.Header().Get("Location") != "/family" || len(r.families.created) != 1 {
		t.Fatalf("verify: %d %q", done.Code, done.Header().Get("Location"))
	}
}

// The inviting guardian may have lost the authority for that child since.
func TestNoOfferWhenTheInvitingGuardianMayNoLongerApprove(t *testing.T) {
	r := newConnectingRig(t)
	code, _ := r.inviteFor(t, "lea")
	token := r.confirmed(t, code, "bruno@example.org", "Leo")
	r.connections.mu.Lock()
	delete(r.connections.names, "anna/family-a/lea")
	r.connections.fail = true
	r.connections.mu.Unlock()
	if join := r.get("/join?code=" + code).Body.String(); strings.Contains(join, "Lea") || !strings.Contains(join, "nicht automatisch Kontakte") {
		t.Fatalf("join page: %s", join)
	}
	if body := r.get("/join/verify?token=" + token).Body.String(); strings.Contains(body, `name="connect"`) || strings.Contains(body, "Lea") {
		t.Fatal("confirm page offers a connection")
	}
	// The registration itself is still good; the guardian learns that the
	// children are not connected.
	done := r.post("/join/verify", url.Values{"token": {token}, "connect": {"yes"}})
	if done.Code != http.StatusSeeOther || done.Header().Get("Location") != "/family?done=unconnected" {
		t.Fatalf("verify: %d %q", done.Code, done.Header().Get("Location"))
	}
	if len(r.families.created) != 1 || len(r.families.discarded) != 0 || len(r.sessions.issued) != 1 || !r.directory.Has("bruno@example.org") {
		t.Fatal("a failed connection damaged the registration")
	}
}

func TestTheConfirmPageOffersNothingForAnUnknownOrExpiredLink(t *testing.T) {
	r := newConnectingRig(t)
	code, _ := r.inviteFor(t, "lea")
	token := r.confirmed(t, code, "bruno@example.org", "Leo")
	for name, target := range map[string]string{
		"unknown token":   "/join/verify?token=" + strings.Repeat("A", 43),
		"malformed token": "/join/verify?token=short",
	} {
		rec := r.get(target)
		if body := rec.Body.String(); rec.Code != http.StatusOK || strings.Contains(body, `name="connect"`) || strings.Contains(body, "Lea") || strings.Contains(body, "Leo") {
			t.Errorf("%s: %d %s", name, rec.Code, body)
		}
	}
	r.clock.add(linkTTL)
	if body := r.get("/join/verify?token=" + token).Body.String(); strings.Contains(body, `name="connect"`) || strings.Contains(body, "Lea") || strings.Contains(body, "Leo") {
		t.Fatal("an expired link still names the children")
	}
}

func TestAnAmbiguousChoiceIsRefusedAndTheLinkStaysUsable(t *testing.T) {
	r := newConnectingRig(t)
	code, _ := r.inviteFor(t, "lea")
	token := r.confirmed(t, code, "bruno@example.org", "Leo")
	bad := r.post("/join/verify", url.Values{"token": {token}, "connect": {"yes", "no"}})
	if bad.Code != http.StatusBadRequest || len(r.families.created) != 0 || len(r.connections.made) != 0 {
		t.Fatalf("ambiguous choice: %d", bad.Code)
	}
	if done := r.post("/join/verify", url.Values{"token": {token}, "connect": {"yes"}}); done.Code != http.StatusSeeOther || len(r.connections.made) != 1 {
		t.Fatalf("the refused request used up the link: %d", done.Code)
	}
}

func TestNamesOnTheRegistrationPagesAreEscaped(t *testing.T) {
	r := newRigWith(t, &fakeConnections{names: map[string]string{"anna/family-a/lea": `Lea<script>&"`}})
	code, _ := r.inviteFor(t, "lea")
	token := r.confirmed(t, code, "bruno@example.org", "Leo")
	for name, body := range map[string]string{
		"join":    r.get("/join?code=" + code).Body.String(),
		"confirm": r.get("/join/verify?token=" + token).Body.String(),
	} {
		if strings.Contains(body, "<script") || !strings.Contains(body, "Lea&lt;script&gt;&amp;&#34;") {
			t.Errorf("%s: name not escaped", name)
		}
	}
}

// Whether parents can write to each other depends on the installation.
func TestTheParentsChannelIsOnlyMentionedWhereItExists(t *testing.T) {
	store, c, _ := newStore(t)
	connections := &fakeConnections{names: map[string]string{"anna/family-a/lea": "Lea"}}
	h, err := New(Config{Origin: "https://yapuno.example", Store: store, Directory: &fakeDirectory{emails: map[string][2]string{}, inactive: map[string]bool{}},
		Families: &fakeFamilies{}, Mailer: &fakeMailer{}, Sessions: &fakeSessions{}, Now: c.now, Connections: connections})
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := store.Invite("anna", "family-a", "lea")
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{routes: h.Routes()}
	if body := r.get("/join?code=" + code).Body.String(); !strings.Contains(body, "Familie von Lea") || strings.Contains(body, "Eltern beider Familien") {
		t.Fatalf("join page: %s", body)
	}
}

// An inviter who locked their access approves nothing any more, so a link
// that is still open names no child of theirs.
func TestTheConfirmPageNamesNoChildOfAnInviterWhoLostAccess(t *testing.T) {
	r := newConnectingRig(t)
	code, _ := r.inviteFor(t, "lea")
	token := r.confirmed(t, code, "bruno@example.org", "Leo")
	r.directory.mu.Lock()
	r.directory.inactive["family-a/anna"] = true
	r.directory.mu.Unlock()
	if body := r.get("/join/verify?token=" + token).Body.String(); strings.Contains(body, `name="connect"`) || strings.Contains(body, "Lea") {
		t.Fatalf("confirm page: %s", body)
	}
}
