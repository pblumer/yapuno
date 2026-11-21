package familyweb

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/signup"
)

type inviteWorld struct {
	*contactWorld
	invitations *signup.Store
}

func newInviteWorld(t *testing.T) *inviteWorld {
	t.Helper()
	cw := newContactWorld(t)
	store, err := signup.Open(filepath.Join(t.TempDir(), "signup.json"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return &inviteWorld{contactWorld: cw, invitations: store}
}

func (iw *inviteWorld) pages(t *testing.T, auth testAuth) http.Handler {
	t.Helper()
	h, err := NewInvitePages(auth, iw.invitations, iw.families, "https://yapuno.example", iw.looks)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func inviteGet(h http.Handler, query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/invite"+query, nil))
	return w
}

func invitePost(h http.Handler, path string, form url.Values, contentType string) *httptest.ResponseRecorder {
	if contentType == "" {
		contentType = "application/x-www-form-urlencoded"
	}
	r := httptest.NewRequest(http.MethodPost, "/family/invite"+path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var joinLink = regexp.MustCompile(`https://yapuno\.example/join\?code=([A-Za-z0-9_-]{22})`)

func TestInvitePagesRequireAuthentication(t *testing.T) {
	iw := newInviteWorld(t)
	h := iw.pages(t, testAuth{err: errors.New("no session")})
	if w := inviteGet(h, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("view: %d", w.Code)
	}
	for _, path := range []string{"", "/withdraw"} {
		if w := invitePost(h, path, url.Values{"csrf_token": {sessionA.CSRFToken}, "invitation_id": {"x"}}, ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("%q: %d", path, w.Code)
		}
	}
	if len(iw.invitations.Pending(sessionA.GuardianID, sessionA.TenantID)) != 0 {
		t.Fatal("invitation created without a session")
	}
}

func TestInvitePageExplainsWhatAnInvitationDoes(t *testing.T) {
	iw := newInviteWorld(t)
	w := inviteGet(iw.pages(t, testAuth{session: sessionA}), "")
	body := w.Body.String()
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("view: %d %v", w.Code, w.Header())
	}
	for _, want := range []string{"Eine Familie einladen", "persönlich kennst", "7 Tage", "nur einmal", "keine Kontakte", "prüft nicht",
		`action="/family/invite"`, `name="csrf_token" value="` + sessionA.CSRFToken + `"`, `href="/family"`, "synthetischen Profilen"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "<script") || strings.Contains(body, "/join?code=") {
		t.Error("page must not contain script or a link before one is created")
	}
	style := body[strings.Index(body, "<style>")+7 : strings.Index(body, "</style>")]
	component := style[strings.Index(style, "body{margin:0}"):]
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).MatchString(component) {
		t.Error("component styles must use skin tokens only")
	}
}

func TestCreatingAnInvitationShowsTheLinkOnce(t *testing.T) {
	iw := newInviteWorld(t)
	h := iw.pages(t, testAuth{session: sessionA})
	w := invitePost(h, "", url.Values{"csrf_token": {sessionA.CSRFToken}}, "")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("create: %d %v", w.Code, w.Header())
	}
	match := joinLink.FindStringSubmatch(w.Body.String())
	if match == nil {
		t.Fatalf("no link on the page: %s", w.Body)
	}
	for _, want := range []string{"nur jetzt", "7 Tage", `href="/family/invite"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("link page lacks %q", want)
		}
	}
	inv, err := iw.invitations.Lookup(match[1])
	if err != nil || inv.Guardian != sessionA.GuardianID || inv.Tenant != sessionA.TenantID {
		t.Fatalf("the shown link is not the stored invitation: %+v %v", inv, err)
	}
	// The overview lists the invitation, but never shows the link again.
	list := inviteGet(h, "").Body.String()
	if joinLink.MatchString(list) || !strings.Contains(list, `name="invitation_id" value="`+inv.ID+`"`) || !strings.Contains(list, "Offene Einladungen") {
		t.Fatalf("overview: %s", list)
	}
	// Another family sees nothing of it.
	other := inviteGet(iw.pages(t, testAuth{session: sessionB}), "").Body.String()
	if strings.Contains(other, inv.ID) {
		t.Fatal("foreign invitation listed")
	}
}

func TestInvitationsAreLimitedWithAnExplanation(t *testing.T) {
	iw := newInviteWorld(t)
	h := iw.pages(t, testAuth{session: sessionA})
	for i := 0; i < signup.MaxOpenPerGuardian; i++ {
		if w := invitePost(h, "", url.Values{"csrf_token": {sessionA.CSRFToken}}, ""); w.Code != http.StatusOK {
			t.Fatalf("invitation %d: %d", i, w.Code)
		}
	}
	w := invitePost(h, "", url.Values{"csrf_token": {sessionA.CSRFToken}}, "")
	if w.Code != http.StatusTooManyRequests || joinLink.MatchString(w.Body.String()) || !strings.Contains(w.Body.String(), `role="alert"`) || !strings.Contains(w.Body.String(), "offene Einladungen") {
		t.Fatalf("over the limit: %d %s", w.Code, w.Body)
	}
	if len(iw.invitations.Pending(sessionA.GuardianID, sessionA.TenantID)) != signup.MaxOpenPerGuardian {
		t.Fatal("limit not enforced")
	}
	// With the limit reached the page offers no further invitation.
	if strings.Contains(inviteGet(h, "").Body.String(), "Einladungslink erstellen</button>") {
		t.Fatal("create button shown at the limit")
	}
}

func TestWithdrawingRemovesOnlyOnesOwnInvitation(t *testing.T) {
	iw := newInviteWorld(t)
	a, b := iw.pages(t, testAuth{session: sessionA}), iw.pages(t, testAuth{session: sessionB})
	code, inv, err := iw.invitations.Invite(sessionA.GuardianID, sessionA.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	if w := invitePost(b, "/withdraw", url.Values{"csrf_token": {sessionB.CSRFToken}, "invitation_id": {inv.ID}}, ""); w.Code != http.StatusNotFound {
		t.Fatalf("foreign withdrawal: %d", w.Code)
	}
	if _, err := iw.invitations.Lookup(code); err != nil {
		t.Fatal("another family withdrew the invitation")
	}
	w := invitePost(a, "/withdraw", url.Values{"csrf_token": {sessionA.CSRFToken}, "invitation_id": {inv.ID}}, "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family/invite?done=withdrawn" {
		t.Fatalf("withdrawal: %d %q", w.Code, w.Header().Get("Location"))
	}
	if _, err := iw.invitations.Lookup(code); !errors.Is(err, signup.ErrNotFound) {
		t.Fatal("withdrawn invitation still valid")
	}
	done := inviteGet(a, "?done=withdrawn").Body.String()
	if !strings.Contains(done, `role="status"`) || !strings.Contains(done, "zurückgezogen") {
		t.Fatalf("notice: %s", done)
	}
	// Only known notices are shown; the query text itself never is.
	if odd := inviteGet(a, "?done=%3Cb%3Ehello").Body.String(); strings.Contains(odd, "hello") || strings.Contains(odd, `role="status"`) {
		t.Fatal("query text rendered")
	}
}

func TestInviteActionsRejectInvalidSubmissions(t *testing.T) {
	iw := newInviteWorld(t)
	h := iw.pages(t, testAuth{session: sessionA})
	_, inv, _ := iw.invitations.Invite(sessionA.GuardianID, sessionA.TenantID)
	cases := []struct {
		name, path, contentType string
		form                    url.Values
		want                    int
	}{
		{"no csrf", "", "", url.Values{}, http.StatusBadRequest},
		{"wrong csrf", "", "", url.Values{"csrf_token": {"wrong"}}, http.StatusForbidden},
		{"json", "", "application/json", url.Values{"csrf_token": {sessionA.CSRFToken}}, http.StatusUnsupportedMediaType},
		{"withdraw without csrf", "/withdraw", "", url.Values{"invitation_id": {inv.ID}}, http.StatusBadRequest},
		{"withdraw with wrong csrf", "/withdraw", "", url.Values{"csrf_token": {"wrong"}, "invitation_id": {inv.ID}}, http.StatusForbidden},
		{"withdraw without id", "/withdraw", "", url.Values{"csrf_token": {sessionA.CSRFToken}}, http.StatusBadRequest},
		{"withdraw two ids", "/withdraw", "", url.Values{"csrf_token": {sessionA.CSRFToken}, "invitation_id": {inv.ID, inv.ID}}, http.StatusBadRequest},
		{"withdraw odd id", "/withdraw", "", url.Values{"csrf_token": {sessionA.CSRFToken}, "invitation_id": {"<b>"}}, http.StatusBadRequest},
		{"withdraw unknown id", "/withdraw", "", url.Values{"csrf_token": {sessionA.CSRFToken}, "invitation_id": {"AAAAAAAAAAAAAAAA"}}, http.StatusNotFound},
	}
	for _, c := range cases {
		if w := invitePost(h, c.path, c.form, c.contentType); w.Code != c.want {
			t.Errorf("%s: %d, want %d", c.name, w.Code, c.want)
		}
	}
	if len(iw.invitations.Pending(sessionA.GuardianID, sessionA.TenantID)) != 1 {
		t.Fatal("an invalid submission changed the invitations")
	}
}

// limitedFamilies shows one child for which the guardian holds only the
// given permissions.
type limitedFamilies struct{ perms familydashboard.Permissions }

func (f limitedFamilies) Cards(string, string) []familydashboard.ChildCard {
	return []familydashboard.ChildCard{{ID: "child-a", DisplayName: "Alex", Permissions: f.perms}}
}

func (limitedFamilies) RenameChild(string, string, string, string) error { return nil }

func TestOnlyGuardiansWithFullAuthorityInvite(t *testing.T) {
	iw := newInviteWorld(t)
	stranger := Session{GuardianID: "nobody", TenantID: "tenant-a", CSRFToken: "csrf-limited-0123456"}
	attempts := map[string]http.Handler{"no child in this family": iw.pages(t, testAuth{session: stranger})}
	for name, perms := range map[string]familydashboard.Permissions{
		"devices only":  {ManageDevices: true},
		"contacts only": {ApproveContacts: true},
	} {
		h, err := NewInvitePages(testAuth{session: stranger}, iw.invitations, limitedFamilies{perms}, "https://yapuno.example", nil)
		if err != nil {
			t.Fatal(err)
		}
		attempts[name] = h
	}
	for name, h := range attempts {
		if w := inviteGet(h, ""); w.Code != http.StatusForbidden {
			t.Errorf("%s, view: %d", name, w.Code)
		}
		if w := invitePost(h, "", url.Values{"csrf_token": {stranger.CSRFToken}}, ""); w.Code != http.StatusForbidden {
			t.Errorf("%s, create: %d", name, w.Code)
		}
	}
	if len(iw.invitations.Pending(stranger.GuardianID, stranger.TenantID)) != 0 {
		t.Fatal("invitation created without authority")
	}
	full, err := NewInvitePages(testAuth{session: stranger}, iw.invitations, limitedFamilies{familydashboard.Permissions{ManageDevices: true, ApproveContacts: true}}, "https://yapuno.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := inviteGet(full, ""); w.Code != http.StatusOK {
		t.Fatalf("full authority: %d", w.Code)
	}
}

func TestInvitePagesFollowTheGuardiansSkinAndNeedTheirDependencies(t *testing.T) {
	iw := newInviteWorld(t)
	if body := inviteGet(iw.pages(t, testAuth{session: sessionA}), "").Body.String(); !strings.Contains(body, `data-yp-skin="classic"`) {
		t.Error("default skin not applied")
	}
	if err := iw.looks.Set(appearance.Subject{Kind: appearance.KindGuardian, TenantID: sessionA.TenantID, ID: sessionA.GuardianID},
		appearance.Preference{Skin: "night", Figure: appearance.Default().Figure}); err != nil {
		t.Fatal(err)
	}
	if body := inviteGet(iw.pages(t, testAuth{session: sessionA}), "").Body.String(); !strings.Contains(body, `data-yp-skin="night"`) {
		t.Error("saved skin not applied")
	}
	auth := testAuth{session: sessionA}
	if _, err := NewInvitePages(nil, iw.invitations, iw.families, "https://yapuno.example", nil); err == nil {
		t.Error("nil authenticator accepted")
	}
	if _, err := NewInvitePages(auth, nil, iw.families, "https://yapuno.example", nil); err == nil {
		t.Error("nil invitations accepted")
	}
	if _, err := NewInvitePages(auth, iw.invitations, nil, "https://yapuno.example", nil); err == nil {
		t.Error("nil families accepted")
	}
	for _, origin := range []string{"", "http://yapuno.example", "https://yapuno.example/path", "https://user@yapuno.example"} {
		if _, err := NewInvitePages(auth, iw.invitations, iw.families, origin, nil); err == nil {
			t.Errorf("origin %q accepted", origin)
		}
	}
	if _, err := NewInvitePages(auth, iw.invitations, iw.families, "https://yapuno.example", nil); err != nil {
		t.Errorf("without looks: %v", err)
	}
}
