package familyweb

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/erasure"
	"github.com/pblumer/yapuno/internal/familystore"
)

// fakeEraser records what it is asked to delete and answers as told.
type fakeEraser struct {
	children [][3]string
	families [][2]string
	err      error
}

func (e *fakeEraser) DeleteChild(guardianID, tenantID, childID string) error {
	e.children = append(e.children, [3]string{guardianID, tenantID, childID})
	return e.err
}

func (e *fakeEraser) DeleteFamily(guardianID, tenantID string) error {
	e.families = append(e.families, [2]string{guardianID, tenantID})
	return e.err
}

type clearedCookie struct{ cleared int }

func (c *clearedCookie) Clear(w http.ResponseWriter) {
	c.cleared++
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", MaxAge: -1})
}

type deleteWorld struct {
	*contactWorld
	eraser *fakeEraser
	cookie *clearedCookie
}

// newDeleteWorld: family a has Alex and Ari, family b has Bea only.
func newDeleteWorld(t *testing.T) *deleteWorld {
	t.Helper()
	cw := newContactWorld(t)
	if err := cw.families.AddChild(sessionA.GuardianID, sessionA.TenantID, "child-a2", "Ari"); err != nil {
		t.Fatal(err)
	}
	return &deleteWorld{contactWorld: cw, eraser: &fakeEraser{}, cookie: &clearedCookie{}}
}

func (dw *deleteWorld) pages(t *testing.T, auth testAuth) http.Handler {
	t.Helper()
	h, err := NewDeletePages(auth, dw.eraser, dw.families, dw.families, dw.cookie, dw.looks)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func deleteGet(h http.Handler, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func deletePost(h http.Handler, target string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func childForm(csrf, child, name string) url.Values {
	return url.Values{"csrf_token": {csrf}, "child_id": {child}, "name": {name}}
}

func TestDeletePagesRequireAuthentication(t *testing.T) {
	dw := newDeleteWorld(t)
	h := dw.pages(t, testAuth{err: errors.New("no session")})
	for _, target := range []string{"/family/children/delete?child_id=child-a", "/family/delete"} {
		if w := deleteGet(h, target); w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s: %d", target, w.Code)
		}
	}
	if w := deletePost(h, "/family/children/delete", childForm(sessionA.CSRFToken, "child-a", "Alex")); w.Code != http.StatusUnauthorized {
		t.Errorf("child: %d", w.Code)
	}
	if w := deletePost(h, "/family/delete", url.Values{"csrf_token": {sessionA.CSRFToken}, "word": {"LÖSCHEN"}}); w.Code != http.StatusUnauthorized {
		t.Errorf("family: %d", w.Code)
	}
	if len(dw.eraser.children)+len(dw.eraser.families) != 0 {
		t.Fatal("something was deleted without a session")
	}
}

func TestTheChildPageSaysWhatIsDeletedAndAsksForTheName(t *testing.T) {
	dw := newDeleteWorld(t)
	w := deleteGet(dw.pages(t, testAuth{session: sessionA}), "/family/children/delete?child_id=child-a")
	body := w.Body.String()
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("page: %d %v", w.Code, w.Header())
	}
	for _, want := range []string{"Profil von Alex löschen", "Geräte", "Kontakte", "Chats", "nicht rückgängig", "Andere Elternteile", "nicht gelöscht",
		`action="/family/children/delete"`, `name="csrf_token" value="` + sessionA.CSRFToken + `"`, `name="child_id" value="child-a"`,
		`name="name"`, `autocomplete="off"`, "Profil endgültig löschen", `href="/family"`, "synthetischen Profilen"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// The name is asked for, not filled in.
	if regexp.MustCompile(`name="name"[^>]*value=`).MatchString(body) || strings.Contains(body, "<script") {
		t.Fatal("the confirmation is prefilled or the page carries script")
	}
	style := body[strings.Index(body, "<style>")+7 : strings.Index(body, "</style>")]
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).MatchString(style[strings.Index(style, "body{margin:0}"):]) {
		t.Error("component styles must use skin tokens only")
	}
	if len(dw.eraser.children) != 0 {
		t.Fatal("showing the page deleted something")
	}
}

func TestAChildIsDeletedOnlyWhenItsNameIsTyped(t *testing.T) {
	dw := newDeleteWorld(t)
	h := dw.pages(t, testAuth{session: sessionA})
	for name, typed := range map[string]string{"empty": "", "another child": "Ari", "partial": "Ale", "with more": "Alex!", "the id": "child-a"} {
		w := deletePost(h, "/family/children/delete", childForm(sessionA.CSRFToken, "child-a", typed))
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `role="alert"`) || !strings.Contains(w.Body.String(), "Nichts wurde gelöscht") {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if len(dw.eraser.children) != 0 {
		t.Fatalf("deleted without the name: %v", dw.eraser.children)
	}
	// Case and surrounding spaces do not matter on a phone keyboard.
	w := deletePost(h, "/family/children/delete", childForm(sessionA.CSRFToken, "child-a", "  alex "))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family?done=child-deleted" {
		t.Fatalf("delete: %d %q", w.Code, w.Header().Get("Location"))
	}
	if len(dw.eraser.children) != 1 || dw.eraser.children[0] != [3]string{sessionA.GuardianID, sessionA.TenantID, "child-a"} {
		t.Fatalf("deleted: %v", dw.eraser.children)
	}
	if dw.cookie.cleared != 0 {
		t.Fatal("deleting a child signed the guardian out")
	}
}

func TestOnlyAGuardianWithFullAuthorityReachesTheChildPage(t *testing.T) {
	dw := newDeleteWorld(t)
	// Guardian b names a child of family a; the session decides, not the form.
	b := dw.pages(t, testAuth{session: sessionB})
	if w := deleteGet(b, "/family/children/delete?child_id=child-a"); w.Code != http.StatusForbidden {
		t.Fatalf("foreign child, page: %d", w.Code)
	}
	if w := deletePost(b, "/family/children/delete", childForm(sessionB.CSRFToken, "child-a", "Alex")); w.Code != http.StatusForbidden {
		t.Fatalf("foreign child, delete: %d", w.Code)
	}
	a := dw.pages(t, testAuth{session: sessionA})
	for _, target := range []string{"/family/children/delete", "/family/children/delete?child_id=", "/family/children/delete?child_id=ghost",
		"/family/children/delete?child_id=child-a&child_id=child-a2", "/family/children/delete?child_id=%3Cb%3E"} {
		if w := deleteGet(a, target); w.Code != http.StatusForbidden && w.Code != http.StatusBadRequest {
			t.Errorf("GET %s: %d", target, w.Code)
		}
	}
	if len(dw.eraser.children) != 0 {
		t.Fatalf("deleted: %v", dw.eraser.children)
	}
}

func TestTheLastChildIsOfferedTheFamilyDeletionInstead(t *testing.T) {
	dw := newDeleteWorld(t)
	b := dw.pages(t, testAuth{session: sessionB})
	w := deleteGet(b, "/family/children/delete?child_id=child-b")
	body := w.Body.String()
	if w.Code != http.StatusOK || strings.Contains(body, `action="/family/children/delete"`) || !strings.Contains(body, "einzige Kind") || !strings.Contains(body, `href="/family/delete"`) {
		t.Fatalf("last child page: %d %s", w.Code, body)
	}
	if w := deletePost(b, "/family/children/delete", childForm(sessionB.CSRFToken, "child-b", "Bea")); w.Code != http.StatusConflict || len(dw.eraser.children) != 0 {
		t.Fatalf("last child deleted: %d %v", w.Code, dw.eraser.children)
	}
}

func TestChildDeletionOutcomesAreReported(t *testing.T) {
	for name, c := range map[string]struct {
		err      error
		status   int
		location string
		says     string
	}{
		"left overs":     {erasure.ErrLeftovers, http.StatusSeeOther, "/family?done=child-deleted-leftovers", ""},
		"incomplete":     {erasure.ErrIncomplete, http.StatusServiceUnavailable, "", "noch einmal"},
		"not allowed":    {familystore.ErrForbidden, http.StatusForbidden, "", ""},
		"last child":     {familystore.ErrLastChild, http.StatusConflict, "", "einzige Kind"},
		"something else": {errors.New("boom"), http.StatusInternalServerError, "", ""},
	} {
		dw := newDeleteWorld(t)
		dw.eraser.err = c.err
		w := deletePost(dw.pages(t, testAuth{session: sessionA}), "/family/children/delete", childForm(sessionA.CSRFToken, "child-a", "Alex"))
		if w.Code != c.status || w.Header().Get("Location") != c.location || !strings.Contains(w.Body.String(), c.says) {
			t.Errorf("%s: %d %q %s", name, w.Code, w.Header().Get("Location"), w.Body)
		}
	}
}

func TestTheFamilyPageSaysWhatIsDeletedAndAsksForTheWord(t *testing.T) {
	dw := newDeleteWorld(t)
	w := deleteGet(dw.pages(t, testAuth{session: sessionA}), "/family/delete")
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("page: %d", w.Code)
	}
	for _, want := range []string{"Familie löschen", "Alex", "Ari", "alle Elternteile", "Geräte", "Kontakte", "Chats", "abgemeldet", "nicht rückgängig", "neu registrieren",
		`action="/family/delete"`, `name="csrf_token" value="` + sessionA.CSRFToken + `"`, `name="word"`, "LÖSCHEN", "Familie endgültig löschen", `href="/family/security"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "Bea") || regexp.MustCompile(`name="word"[^>]*value=`).MatchString(body) || strings.Contains(body, "<script") {
		t.Fatal("the page names another family, is prefilled or carries script")
	}
}

func TestAFamilyIsDeletedOnlyWhenTheWordIsTypedAndThenTheGuardianIsSignedOut(t *testing.T) {
	dw := newDeleteWorld(t)
	h := dw.pages(t, testAuth{session: sessionA})
	for name, typed := range map[string]string{"empty": "", "yes": "ja", "partial": "LÖSCH", "without umlaut": "LOSCHEN", "with more": "LÖSCHEN!"} {
		w := deletePost(h, "/family/delete", url.Values{"csrf_token": {sessionA.CSRFToken}, "word": {typed}})
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Nichts wurde gelöscht") {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if len(dw.eraser.families) != 0 || dw.cookie.cleared != 0 {
		t.Fatalf("deleted without the word: %v", dw.eraser.families)
	}
	w := deletePost(h, "/family/delete", url.Values{"csrf_token": {sessionA.CSRFToken}, "word": {" löschen "}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?notice=family-deleted" {
		t.Fatalf("delete: %d %q", w.Code, w.Header().Get("Location"))
	}
	if len(dw.eraser.families) != 1 || dw.eraser.families[0] != [2]string{sessionA.GuardianID, sessionA.TenantID} || dw.cookie.cleared != 1 {
		t.Fatalf("deleted %v, cookie cleared %d times", dw.eraser.families, dw.cookie.cleared)
	}
}

func TestFamilyDeletionOutcomesAreReported(t *testing.T) {
	for name, c := range map[string]struct {
		err      error
		status   int
		location string
		says     string
		cleared  int
	}{
		"left overs":     {erasure.ErrLeftovers, http.StatusSeeOther, "/login?notice=family-deleted-incomplete", "", 1},
		"incomplete":     {erasure.ErrIncomplete, http.StatusServiceUnavailable, "", "noch einmal", 0},
		"not allowed":    {familystore.ErrForbidden, http.StatusForbidden, "", "", 0},
		"last family":    {familystore.ErrLastFamily, http.StatusConflict, "", "letzte Familie", 0},
		"something else": {errors.New("boom"), http.StatusInternalServerError, "", "", 0},
	} {
		dw := newDeleteWorld(t)
		dw.eraser.err = c.err
		w := deletePost(dw.pages(t, testAuth{session: sessionA}), "/family/delete", url.Values{"csrf_token": {sessionA.CSRFToken}, "word": {"LÖSCHEN"}})
		if w.Code != c.status || w.Header().Get("Location") != c.location || !strings.Contains(w.Body.String(), c.says) || dw.cookie.cleared != c.cleared {
			t.Errorf("%s: %d %q cleared %d %s", name, w.Code, w.Header().Get("Location"), dw.cookie.cleared, w.Body)
		}
	}
}

// The page explains instead of offering a form that could only fail.
func TestTheFamilyPageExplainsWhenTheFamilyCannotBeDeleted(t *testing.T) {
	dw := newDeleteWorld(t)
	// Remove the two other families: family a is then the last one.
	for _, s := range []Session{sessionB, sessionC} {
		if _, err := dw.families.RemoveFamily(s.GuardianID, s.TenantID); err != nil {
			t.Fatal(err)
		}
	}
	w := deleteGet(dw.pages(t, testAuth{session: sessionA}), "/family/delete")
	if body := w.Body.String(); w.Code != http.StatusOK || strings.Contains(body, `action="/family/delete"`) || !strings.Contains(body, "letzte Familie") {
		t.Fatalf("last family page: %d %s", w.Code, body)
	}
	// A guardian who does not answer for every child of the family.
	limited := limitedFamilies{}
	h, err := NewDeletePages(testAuth{session: sessionA}, dw.eraser, refusing{familystore.ErrForbidden}, limited, dw.cookie, nil)
	if err != nil {
		t.Fatal(err)
	}
	w = deleteGet(h, "/family/delete")
	if body := w.Body.String(); w.Code != http.StatusForbidden || strings.Contains(body, `action="/family/delete"`) {
		t.Fatalf("partial authority: %d %s", w.Code, body)
	}
}

// refusing answers every question about deletion with the same refusal.
type refusing struct{ err error }

func (r refusing) MayRemoveChild(string, string, string) error { return r.err }
func (r refusing) MayRemoveFamily(string, string) error        { return r.err }

func TestDeleteActionsRejectInvalidSubmissions(t *testing.T) {
	dw := newDeleteWorld(t)
	h := dw.pages(t, testAuth{session: sessionA})
	post := func(target, contentType string, form url.Values) int {
		r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	const formType = "application/x-www-form-urlencoded"
	for name, c := range map[string]struct {
		target, contentType string
		form                url.Values
		want                int
	}{
		"child, no csrf":     {"/family/children/delete", formType, url.Values{"child_id": {"child-a"}, "name": {"Alex"}}, http.StatusBadRequest},
		"child, wrong csrf":  {"/family/children/delete", formType, childForm("wrong", "child-a", "Alex"), http.StatusForbidden},
		"child, json":        {"/family/children/delete", "application/json", childForm(sessionA.CSRFToken, "child-a", "Alex"), http.StatusUnsupportedMediaType},
		"child, no id":       {"/family/children/delete", formType, url.Values{"csrf_token": {sessionA.CSRFToken}, "name": {"Alex"}}, http.StatusBadRequest},
		"child, two ids":     {"/family/children/delete", formType, url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a", "child-a2"}, "name": {"Alex"}}, http.StatusBadRequest},
		"child, no name":     {"/family/children/delete", formType, url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}}, http.StatusBadRequest},
		"child, two names":   {"/family/children/delete", formType, url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}, "name": {"Alex", "Ari"}}, http.StatusBadRequest},
		"family, no csrf":    {"/family/delete", formType, url.Values{"word": {"LÖSCHEN"}}, http.StatusBadRequest},
		"family, wrong csrf": {"/family/delete", formType, url.Values{"csrf_token": {"wrong"}, "word": {"LÖSCHEN"}}, http.StatusForbidden},
		"family, json":       {"/family/delete", "application/json", url.Values{"csrf_token": {sessionA.CSRFToken}, "word": {"LÖSCHEN"}}, http.StatusUnsupportedMediaType},
		"family, no word":    {"/family/delete", formType, url.Values{"csrf_token": {sessionA.CSRFToken}}, http.StatusBadRequest},
		"family, two words":  {"/family/delete", formType, url.Values{"csrf_token": {sessionA.CSRFToken}, "word": {"LÖSCHEN", "LÖSCHEN"}}, http.StatusBadRequest},
	} {
		if got := post(c.target, c.contentType, c.form); got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
	if len(dw.eraser.children)+len(dw.eraser.families) != 0 || dw.cookie.cleared != 0 {
		t.Fatal("an invalid submission deleted something")
	}
}

func TestDeletePagesNeedTheirDependencies(t *testing.T) {
	dw := newDeleteWorld(t)
	auth := testAuth{session: sessionA}
	if _, err := NewDeletePages(nil, dw.eraser, dw.families, dw.families, dw.cookie, nil); err == nil {
		t.Error("nil authenticator accepted")
	}
	if _, err := NewDeletePages(auth, nil, dw.families, dw.families, dw.cookie, nil); err == nil {
		t.Error("nil eraser accepted")
	}
	if _, err := NewDeletePages(auth, dw.eraser, nil, dw.families, dw.cookie, nil); err == nil {
		t.Error("nil rules accepted")
	}
	if _, err := NewDeletePages(auth, dw.eraser, dw.families, nil, dw.cookie, nil); err == nil {
		t.Error("nil families accepted")
	}
	if _, err := NewDeletePages(auth, dw.eraser, dw.families, dw.families, nil, nil); err == nil {
		t.Error("nil cookie accepted")
	}
	if _, err := NewDeletePages(auth, dw.eraser, dw.families, dw.families, dw.cookie, nil); err != nil {
		t.Errorf("without looks: %v", err)
	}
}

func TestTheOverviewLinksToDeletingAProfileOnlyWhereThatIsPossible(t *testing.T) {
	cw := newChatWorld(t)
	on := cw.homeAt(t, Options{DeletePages: true, SecurityPages: true}, "/family")
	if !strings.Contains(on, `href="/family/children/delete?child_id=child-a"`) || !strings.Contains(on, "Profil von Alex löschen") {
		t.Fatalf("overview does not offer the deletion: %s", on)
	}
	if off := cw.homeAt(t, Options{SecurityPages: true}, "/family"); strings.Contains(off, "/family/children/delete") {
		t.Fatal("deletion offered although it is switched off")
	}
	// A guardian without full authority is not led to a page that refuses.
	limited, err := NewWithOptions(testAuth{session: sessionA}, limitedFamilies{}, Options{DeletePages: true})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	limited.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/family", nil))
	if strings.Contains(rec.Body.String(), "/family/children/delete") {
		t.Fatal("deletion offered without authority")
	}
}

func TestTheOverviewConfirmsADeletedProfile(t *testing.T) {
	cw := newChatWorld(t)
	done := cw.homeAt(t, Options{DeletePages: true}, "/family?done=child-deleted")
	if !strings.Contains(done, `role="status"`) || !strings.Contains(done, "Das Profil ist gelöscht") || strings.Contains(done, `role="alert"`) {
		t.Fatalf("confirmation: %s", done)
	}
	left := cw.homeAt(t, Options{DeletePages: true}, "/family?done=child-deleted-leftovers")
	if !strings.Contains(left, `role="alert"`) || !strings.Contains(left, "Das Profil ist gelöscht") || !strings.Contains(left, "Aufräumen") {
		t.Fatalf("leftovers: %s", left)
	}
}

func TestTheSecurityPageLinksToDeletingTheFamilyOnlyWhereThatIsPossible(t *testing.T) {
	sw := newSecurityWorld(t)
	auth := testAuth{session: sessionA}
	with, err := NewSecurityPages(auth, sw.fakes, sw.fakes, sw.families, SecurityOptions{DeleteFamily: true})
	if err != nil {
		t.Fatal(err)
	}
	body := securityGet(with).Body.String()
	if !strings.Contains(body, `href="/family/delete"`) || !strings.Contains(body, "Familie löschen") || !strings.Contains(body, "nicht rückgängig") {
		t.Fatalf("security page does not offer the deletion: %s", body)
	}
	if off := securityGet(sw.pages(t, auth, true, true)).Body.String(); strings.Contains(off, "/family/delete") {
		t.Fatal("deletion offered although it is switched off")
	}
}
