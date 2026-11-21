package familyweb

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/familystore"
)

func (iw *inviteWorld) family(t *testing.T, auth testAuth, selfService bool) http.Handler {
	t.Helper()
	opt := Options{}
	if selfService {
		opt.Children, opt.InvitePages = iw.families, true
	}
	h, err := NewWithOptions(auth, iw.families, opt)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func childPost(h http.Handler, form url.Values, contentType string) *httptest.ResponseRecorder {
	if contentType == "" {
		contentType = "application/x-www-form-urlencoded"
	}
	r := httptest.NewRequest(http.MethodPost, "/family/children", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func childGet(h http.Handler, query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/children"+query, nil))
	return w
}

func familyGet(h http.Handler) string {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family", nil))
	return w.Body.String()
}

func TestAGuardianAddsAChildToTheirOwnFamily(t *testing.T) {
	iw := newInviteWorld(t)
	h := iw.family(t, testAuth{session: sessionA}, true)
	page := familyGet(h)
	for _, want := range []string{`href="/family/children"`, "Weiteres Kind anlegen", `href="/family/invite"`, "Eine Familie einladen"} {
		if !strings.Contains(page, want) {
			t.Errorf("family page lacks %q", want)
		}
	}
	form := childGet(h, "")
	if form.Code != http.StatusOK || form.Header().Get("Cache-Control") != "no-store" || !strings.Contains(form.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("form page: %d %v", form.Code, form.Header())
	}
	for _, want := range []string{`<html lang="de">`, `data-yp-skin="classic"`, "<h1>Weiteres Kind anlegen</h1>", `action="/family/children"`, `maxlength="40"`, `required`,
		`name="csrf_token" value="` + sessionA.CSRFToken + `"`, `href="/family"`, "Geräte verwalten", "Betreiber", "synthetischen Profilen"} {
		if !strings.Contains(form.Body.String(), want) {
			t.Errorf("form page lacks %q", want)
		}
	}
	style := form.Body.String()[strings.Index(form.Body.String(), "<style>")+7 : strings.Index(form.Body.String(), "</style>")]
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).MatchString(style[strings.Index(style, "body{margin:0}"):]) || strings.Contains(form.Body.String(), "<script") {
		t.Error("form page must use skin tokens only and no script")
	}
	w := childPost(h, url.Values{"csrf_token": {sessionA.CSRFToken}, "name": {"  Tim  "}}, "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family/children?done=added" {
		t.Fatalf("add: %d %s", w.Code, w.Body)
	}
	cards := iw.families.Cards(sessionA.GuardianID, sessionA.TenantID)
	if len(cards) != 2 {
		t.Fatalf("cards: %+v", cards)
	}
	var added string
	for _, c := range cards {
		if c.DisplayName == "Tim" {
			added = c.ID
			if !c.Permissions.ManageDevices || !c.Permissions.ApproveContacts {
				t.Fatal("new child without full grant")
			}
		}
	}
	// The server chooses the id; it is random and well formed.
	if !regexp.MustCompile(`^c-[0-9a-f]{32}$`).MatchString(added) || !validChildID(added) {
		t.Fatalf("child id %q", added)
	}
	if len(iw.families.Cards(sessionB.GuardianID, sessionB.TenantID)) != 1 {
		t.Fatal("another family changed")
	}
	if !strings.Contains(familyGet(h), "Tim") {
		t.Fatal("new child not shown")
	}
}

func TestAddingAChildIsOffUnlessConfigured(t *testing.T) {
	iw := newInviteWorld(t)
	h := iw.family(t, testAuth{session: sessionA}, false)
	page := familyGet(h)
	if strings.Contains(page, "/family/children") || strings.Contains(page, "/family/invite") {
		t.Fatal("self-service offered without configuration")
	}
	if w := childGet(h, ""); w.Code != http.StatusNotFound {
		t.Fatalf("form page without configuration: %d", w.Code)
	}
	if w := childPost(h, url.Values{"csrf_token": {sessionA.CSRFToken}, "name": {"Tim"}}, ""); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("add without configuration: %d", w.Code)
	}
	if len(iw.families.Cards(sessionA.GuardianID, sessionA.TenantID)) != 1 {
		t.Fatal("child added without configuration")
	}
}

func TestAddingAChildRejectsInvalidSubmissions(t *testing.T) {
	iw := newInviteWorld(t)
	h := iw.family(t, testAuth{session: sessionA}, true)
	cases := []struct {
		name, contentType string
		form              url.Values
		want              int
	}{
		{"no csrf", "", url.Values{"name": {"Tim"}}, http.StatusBadRequest},
		{"wrong csrf", "", url.Values{"csrf_token": {"wrong"}, "name": {"Tim"}}, http.StatusForbidden},
		{"json", "application/json", url.Values{"csrf_token": {sessionA.CSRFToken}, "name": {"Tim"}}, http.StatusUnsupportedMediaType},
		{"no name", "", url.Values{"csrf_token": {sessionA.CSRFToken}}, http.StatusBadRequest},
		{"two names", "", url.Values{"csrf_token": {sessionA.CSRFToken}, "name": {"Tim", "Tom"}}, http.StatusBadRequest},
		{"blank name", "", url.Values{"csrf_token": {sessionA.CSRFToken}, "name": {"   "}}, http.StatusBadRequest},
		{"markup", "", url.Values{"csrf_token": {sessionA.CSRFToken}, "name": {"<i>Tim</i>"}}, http.StatusBadRequest},
		{"long name", "", url.Values{"csrf_token": {sessionA.CSRFToken}, "name": {strings.Repeat("x", 41)}}, http.StatusBadRequest},
		// A client cannot choose the id or the family of the new child.
		{"client id is ignored but harmless", "", url.Values{"csrf_token": {sessionA.CSRFToken}, "name": {"Ok"}, "child_id": {"child-b"}, "tenant_id": {"tenant-b"}}, http.StatusSeeOther},
	}
	for _, c := range cases {
		if w := childPost(h, c.form, c.contentType); w.Code != c.want {
			t.Errorf("%s: %d, want %d", c.name, w.Code, c.want)
		}
	}
	cards := iw.families.Cards(sessionA.GuardianID, sessionA.TenantID)
	if len(cards) != 2 {
		t.Fatalf("cards after invalid submissions: %+v", cards)
	}
	for _, c := range cards {
		if c.ID == "child-b" {
			t.Fatal("client-chosen id used")
		}
	}
	if len(iw.families.Cards(sessionB.GuardianID, sessionB.TenantID)) != 1 {
		t.Fatal("another family changed")
	}
	anonymous := iw.family(t, testAuth{err: errors.New("no session")}, true)
	if w := childPost(anonymous, url.Values{"csrf_token": {sessionA.CSRFToken}, "name": {"Tim"}}, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	if w := childGet(anonymous, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated form page: %d", w.Code)
	}
	// An invalid name comes back as the page with an explanation, not as bare text.
	if w := childPost(h, url.Values{"csrf_token": {sessionA.CSRFToken}, "name": {"<i>Tim</i>"}}, ""); !strings.Contains(w.Body.String(), `role="alert"`) || strings.Contains(w.Body.String(), "<i>Tim") || !strings.Contains(w.Body.String(), `action="/family/children"`) {
		t.Fatalf("invalid name: %s", w.Body)
	}
	// The notice after adding is one fixed text.
	if page := childGet(h, "?done=added").Body.String(); !strings.Contains(page, `role="status"`) {
		t.Fatal("no confirmation after adding")
	}
	if page := childGet(h, "?done=%3Cb%3Ex").Body.String(); strings.Contains(page, `role="status"`) {
		t.Fatal("query text selects a notice")
	}
}

func TestAddingAChildStopsAtTheFamilyLimit(t *testing.T) {
	iw := newInviteWorld(t)
	h := iw.family(t, testAuth{session: sessionA}, true)
	for i := 1; i < familystore.MaxChildrenPerFamily; i++ {
		if w := childPost(h, url.Values{"csrf_token": {sessionA.CSRFToken}, "name": {"Kind"}}, ""); w.Code != http.StatusSeeOther {
			t.Fatalf("child %d: %d", i, w.Code)
		}
	}
	w := childPost(h, url.Values{"csrf_token": {sessionA.CSRFToken}, "name": {"Kind"}}, "")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `role="alert"`) || !strings.Contains(w.Body.String(), "höchstens 8 Kinder") {
		t.Fatalf("over the limit: %d %s", w.Code, w.Body)
	}
	if page := childGet(h, "").Body.String(); strings.Contains(page, `action="/family/children"`) || !strings.Contains(page, "höchstens 8 Kinder") {
		t.Fatal("form offered at the limit")
	}
}

func TestAGuardianWithoutFullAuthorityAddsNoChild(t *testing.T) {
	iw := newInviteWorld(t)
	stranger := Session{GuardianID: "guardian-a", TenantID: "tenant-b", CSRFToken: "csrf-x-0123456789"}
	h := iw.family(t, testAuth{session: stranger}, true)
	if w := childPost(h, url.Values{"csrf_token": {stranger.CSRFToken}, "name": {"Tim"}}, ""); w.Code != http.StatusForbidden {
		t.Fatalf("stranger: %d", w.Code)
	}
	if page := familyGet(h); strings.Contains(page, `/family/children`) || strings.Contains(page, `/family/invite`) {
		t.Fatal("self-service linked without authority")
	}
	if w := childGet(h, ""); w.Code != http.StatusForbidden {
		t.Fatalf("form page without authority: %d", w.Code)
	}
	if len(iw.families.Cards(sessionB.GuardianID, sessionB.TenantID)) != 1 {
		t.Fatal("child added to a foreign family")
	}
}
