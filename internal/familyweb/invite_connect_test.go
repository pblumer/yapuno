package familyweb

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/familydashboard"
)

// connecting serves the invitation pages of an installation on which
// contacts are switched on.
func (iw *inviteWorld) connecting(t *testing.T, auth testAuth) http.Handler {
	t.Helper()
	h, err := NewInvitePages(auth, iw.invitations, iw.families, "https://yapuno.example", InviteOptions{Looks: iw.looks, Connect: true, ParentsChat: true})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestTheInvitePageAsksWhichChildToConnect(t *testing.T) {
	iw := newInviteWorld(t)
	if err := iw.families.AddChild(sessionA.GuardianID, sessionA.TenantID, "child-a2", "Ari"); err != nil {
		t.Fatal(err)
	}
	body := inviteGet(iw.connecting(t, testAuth{session: sessionA}), "").Body.String()
	for _, want := range []string{
		`<input type="radio" name="child_id" value="child-a" required>`, "Ja, mit Alex",
		`<input type="radio" name="child_id" value="child-a2" required>`, "Ja, mit Ari",
		`<input type="radio" name="child_id" value="" required>`, "Nein, nur einladen",
		"Vornamen", "ohne dass du noch einmal gefragt wirst", "jederzeit beenden", "Eltern beider Familien", "prüft nicht",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// No choice is made for the guardian, and no child of another family is offered.
	if strings.Contains(body, "checked") || strings.Contains(body, "Bea") || strings.Contains(body, "child-b") {
		t.Fatalf("page: %s", body)
	}
	// What is no longer true with a connection is not claimed.
	if strings.Contains(body, "werden dadurch keine Kontakte") {
		t.Fatal("page still says an invitation never connects children")
	}
}

func TestAnInvitationCanApproveAContactForOneChild(t *testing.T) {
	iw := newInviteWorld(t)
	h := iw.connecting(t, testAuth{session: sessionA})
	w := invitePost(h, "", url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}}, "")
	match := joinLink.FindStringSubmatch(w.Body.String())
	if w.Code != http.StatusOK || match == nil {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "mit Alex verbinden") || !strings.Contains(w.Body.String(), "nur der Familie") {
		t.Fatalf("link page does not say what the link allows: %s", w.Body)
	}
	inv, err := iw.invitations.Lookup(match[1])
	if err != nil || inv.Child != "child-a" || inv.Guardian != sessionA.GuardianID || inv.Tenant != sessionA.TenantID {
		t.Fatalf("invitation: %+v %v", inv, err)
	}
	if list := inviteGet(h, "").Body.String(); !strings.Contains(list, "verbindet mit Alex") {
		t.Fatalf("overview does not name the child: %s", list)
	}

	plain := invitePost(h, "", url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {""}}, "")
	match = joinLink.FindStringSubmatch(plain.Body.String())
	if plain.Code != http.StatusOK || match == nil || strings.Contains(plain.Body.String(), "verbinden") {
		t.Fatalf("plain invitation: %d %s", plain.Code, plain.Body)
	}
	if inv, err := iw.invitations.Lookup(match[1]); err != nil || inv.Child != "" {
		t.Fatalf("plain invitation: %+v %v", inv, err)
	}
	if list := inviteGet(h, "").Body.String(); strings.Count(list, "verbindet mit") != 1 || !strings.Contains(list, "ohne Verbindung") {
		t.Fatalf("overview: %s", list)
	}
}

// mixedFamilies shows a guardian with full authority for one child and with
// device authority only for a second one.
type mixedFamilies struct{}

func (mixedFamilies) Cards(string, string) []familydashboard.ChildCard {
	return []familydashboard.ChildCard{
		{ID: "child-a", DisplayName: "Alex", Permissions: familydashboard.Permissions{ManageDevices: true, ApproveContacts: true}},
		{ID: "child-d", DisplayName: "Dana", Permissions: familydashboard.Permissions{ManageDevices: true}},
	}
}

func (mixedFamilies) RenameChild(string, string, string, string) error { return nil }

func TestAnInvitationNamesOnlyAChildTheGuardianMayApproveContactsFor(t *testing.T) {
	iw := newInviteWorld(t)
	h := iw.connecting(t, testAuth{session: sessionA})
	for name, form := range map[string]url.Values{
		"child of another family": {"child_id": {"child-b"}},
		"unknown child":           {"child_id": {"ghost"}},
		"markup":                  {"child_id": {"<b>"}},
	} {
		form.Set("csrf_token", sessionA.CSRFToken)
		if w := invitePost(h, "", form, ""); w.Code != http.StatusForbidden || joinLink.MatchString(w.Body.String()) {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	for name, form := range map[string]url.Values{
		"no choice":   {},
		"two choices": {"child_id": {"child-a", ""}},
	} {
		form.Set("csrf_token", sessionA.CSRFToken)
		if w := invitePost(h, "", form, ""); w.Code != http.StatusBadRequest || joinLink.MatchString(w.Body.String()) {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if len(iw.invitations.Pending(sessionA.GuardianID, sessionA.TenantID)) != 0 {
		t.Fatal("a refused request created an invitation")
	}
	mixed, err := NewInvitePages(testAuth{session: sessionA}, iw.invitations, mixedFamilies{}, "https://yapuno.example", InviteOptions{Connect: true})
	if err != nil {
		t.Fatal(err)
	}
	if body := inviteGet(mixed, "").Body.String(); !strings.Contains(body, "Ja, mit Alex") || strings.Contains(body, "Dana") {
		t.Fatalf("a child without contact authority is offered: %s", body)
	}
	if w := invitePost(mixed, "", url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-d"}}, ""); w.Code != http.StatusForbidden {
		t.Fatalf("child without contact authority: %d", w.Code)
	}
	if len(iw.invitations.Pending(sessionA.GuardianID, sessionA.TenantID)) != 0 {
		t.Fatal("an invitation approves a contact without authority")
	}
}

// Where contacts are switched off, an invitation cannot carry an approval.
func TestWithoutContactsAnInvitationNamesNoChild(t *testing.T) {
	iw := newInviteWorld(t)
	h := iw.pages(t, testAuth{session: sessionA})
	if body := inviteGet(h, "").Body.String(); strings.Contains(body, `name="child_id"`) || !strings.Contains(body, "keine Kontakte") {
		t.Fatalf("page: %s", body)
	}
	if w := invitePost(h, "", url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}}, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("child named although contacts are off: %d", w.Code)
	}
	if len(iw.invitations.Pending(sessionA.GuardianID, sessionA.TenantID)) != 0 {
		t.Fatal("invitation created")
	}
	w := invitePost(h, "", url.Values{"csrf_token": {sessionA.CSRFToken}}, "")
	match := joinLink.FindStringSubmatch(w.Body.String())
	if w.Code != http.StatusOK || match == nil {
		t.Fatalf("plain invitation: %d", w.Code)
	}
	if inv, err := iw.invitations.Lookup(match[1]); err != nil || inv.Child != "" {
		t.Fatalf("invitation: %+v %v", inv, err)
	}
}

func TestTheParentsChannelIsOnlyMentionedOnTheInvitePageWhereItExists(t *testing.T) {
	iw := newInviteWorld(t)
	h, err := NewInvitePages(testAuth{session: sessionA}, iw.invitations, iw.families, "https://yapuno.example", InviteOptions{Connect: true})
	if err != nil {
		t.Fatal(err)
	}
	if body := inviteGet(h, "").Body.String(); strings.Contains(body, "Eltern beider Familien") || !strings.Contains(body, "Ja, mit Alex") {
		t.Fatalf("page: %s", body)
	}
}
