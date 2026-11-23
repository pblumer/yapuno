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

	"github.com/pblumer/yapuno/internal/guardianchat"
	"github.com/pblumer/yapuno/internal/guardiannames"
)

func openNames(t *testing.T) *guardiannames.Store {
	t.Helper()
	s, err := guardiannames.Open(filepath.Join(t.TempDir(), "guardian-names.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (cw *chatWorld) pagesWithNames(t *testing.T, auth testAuth, names *guardiannames.Store) http.Handler {
	t.Helper()
	h, err := NewChatPagesWithNames(auth, cw.chat, cw.families, names, cw.looks)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// chatBody returns the body of a chat page; path is relative to /family/chat.
func chatBody(h http.Handler, path string) string {
	return chatGet(h, path).Body.String()
}

// Guardian b chose a name for other parents. Guardian a, whose child is
// connected, sees it together with how the two are related.
func TestAConnectedGuardianIsShownByTheNameTheyChoseForAdults(t *testing.T) {
	cw := newChatWorld(t)
	names := openNames(t)
	if err := names.Set(sessionB.TenantID, sessionB.GuardianID, guardiannames.Names{ForChildren: "Papi", ForAdults: "Bruno Beispiel"}); err != nil {
		t.Fatal(err)
	}
	a := cw.pagesWithNames(t, testAuth{session: sessionA}, names)
	inbox := chatBody(a, "")
	if !strings.Contains(inbox, "Bruno Beispiel") || !strings.Contains(inbox, "Elternteil von Bea") {
		t.Fatalf("inbox: %s", inbox)
	}
	// The name for the children is not for other adults.
	if strings.Contains(inbox, "Papi") {
		t.Fatal("another family sees the name meant for the guardian's own children")
	}
	thread := chatBody(a, "/"+cw.thread(t, sessionA, guardianchat.Parents))
	if !strings.Contains(thread, "<h1>Bruno Beispiel</h1>") || !strings.Contains(thread, "Elternteil von Bea") || strings.Contains(thread, "Papi") {
		t.Fatalf("thread: %s", thread)
	}
	// Guardian b has not been given a name by guardian a and sees the relation only.
	b := cw.pagesWithNames(t, testAuth{session: sessionB}, names)
	if other := chatBody(b, ""); !strings.Contains(other, "Elternteil von Alex") || strings.Contains(other, "Bruno Beispiel") {
		t.Fatalf("the other inbox: %s", other)
	}
	// A family that is not connected sees nothing of it.
	c := cw.pagesWithNames(t, testAuth{session: sessionC}, names)
	if apart := chatBody(c, ""); strings.Contains(apart, "Bruno") {
		t.Fatal("an unconnected family sees the name")
	}
}

func TestWithoutANameTheRelationIsShownAsBefore(t *testing.T) {
	cw := newChatWorld(t)
	names := openNames(t)
	// Only a name for the children: other adults still see the relation.
	if err := names.Set(sessionB.TenantID, sessionB.GuardianID, guardiannames.Names{ForChildren: "Papi"}); err != nil {
		t.Fatal(err)
	}
	for name, h := range map[string]http.Handler{"with a name store": cw.pagesWithNames(t, testAuth{session: sessionA}, names), "without one": cw.pages(t, testAuth{session: sessionA})} {
		inbox := chatBody(h, "")
		thread := chatBody(h, "/"+cw.thread(t, sessionA, guardianchat.Parents))
		if !strings.Contains(inbox, "Elternteil von Bea") || !strings.Contains(thread, "<h1>Elternteil von Bea</h1>") || strings.Contains(inbox, "Papi") || strings.Contains(thread, "Papi") {
			t.Errorf("%s: %s", name, inbox)
		}
	}
}

func TestAChosenNameIsShownAsTextOnly(t *testing.T) {
	cw := newChatWorld(t)
	// The store refuses markup; the page must not rely on that.
	a, err := NewChatPagesWithNames(testAuth{session: sessionA}, cw.chat, cw.families, fixedNames{guardiannames.Names{ForAdults: `Bruno "<b>&`}}, cw.looks)
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{chatBody(a, ""), chatBody(a, "/"+cw.thread(t, sessionA, guardianchat.Parents))} {
		if strings.Contains(page, "<b>&") || !strings.Contains(page, "Bruno &#34;&lt;b&gt;&amp;") {
			t.Fatalf("name not escaped: %s", page)
		}
	}
}

// fixedNames answers every question with the same names.
type fixedNames struct{ names guardiannames.Names }

func (f fixedNames) Get(string, string) guardiannames.Names { return f.names }

func namePages(t *testing.T, auth testAuth, names *guardiannames.Store, cw *contactWorld) http.Handler {
	t.Helper()
	h, err := NewNamePages(auth, names, cw.looks)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func namePost(h http.Handler, form url.Values, contentType string) *httptest.ResponseRecorder {
	if contentType == "" {
		contentType = "application/x-www-form-urlencoded"
	}
	r := httptest.NewRequest(http.MethodPost, "/family/name", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestTheNamePageSaysWhoSeesWhichName(t *testing.T) {
	cw := newContactWorld(t)
	names := openNames(t)
	if err := names.Set(sessionA.TenantID, sessionA.GuardianID, guardiannames.Names{ForChildren: "Mamsi", ForAdults: "Annegret"}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	namePages(t, testAuth{session: sessionA}, names, cw).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/name", nil))
	body := w.Body.String()
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("page: %d %v", w.Code, w.Header())
	}
	for _, want := range []string{"Mein Name", "Für deine Kinder", "Für andere Eltern", `name="for_children" value="Mamsi"`, `name="for_adults" value="Annegret"`,
		`maxlength="40"`, `autocomplete="off"`, `name="csrf_token" value="` + sessionA.CSRFToken + `"`, `action="/family/name"`,
		"Kinder anderer Familien", "solange eure Kinder verbunden sind", "prüft", "freiwillig", `href="/family"`, "synthetischen Profilen"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "<script") {
		t.Error("page carries script")
	}
	style := body[strings.Index(body, "<style>")+7 : strings.Index(body, "</style>")]
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).MatchString(style[strings.Index(style, "body{margin:0}"):]) {
		t.Error("component styles must use skin tokens only")
	}
	// Another guardian sees their own, empty form.
	other := httptest.NewRecorder()
	namePages(t, testAuth{session: sessionB}, names, cw).ServeHTTP(other, httptest.NewRequest(http.MethodGet, "/family/name", nil))
	if strings.Contains(other.Body.String(), "Mamsi") || strings.Contains(other.Body.String(), "Annegret") {
		t.Fatal("a guardian sees another guardian's names")
	}
}

func TestAGuardianSetsChangesAndRemovesOnlyTheirOwnNames(t *testing.T) {
	cw := newContactWorld(t)
	names := openNames(t)
	if err := names.Set(sessionB.TenantID, sessionB.GuardianID, guardiannames.Names{ForAdults: "Bruno"}); err != nil {
		t.Fatal(err)
	}
	h := namePages(t, testAuth{session: sessionA}, names, cw)
	// The form may try to name somebody else; the session decides.
	w := namePost(h, url.Values{"csrf_token": {sessionA.CSRFToken}, "for_children": {" Mami "}, "for_adults": {"Anna"},
		"guardian_id": {sessionB.GuardianID}, "tenant_id": {sessionB.TenantID}}, "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family/name?done=saved" {
		t.Fatalf("save: %d %q", w.Code, w.Header().Get("Location"))
	}
	if got := names.Get(sessionA.TenantID, sessionA.GuardianID); got != (guardiannames.Names{ForChildren: "Mami", ForAdults: "Anna"}) {
		t.Fatalf("own names: %+v", got)
	}
	if got := names.Get(sessionB.TenantID, sessionB.GuardianID); got != (guardiannames.Names{ForAdults: "Bruno"}) {
		t.Fatalf("another guardian's names changed: %+v", got)
	}
	done := httptest.NewRecorder()
	h.ServeHTTP(done, httptest.NewRequest(http.MethodGet, "/family/name?done=saved", nil))
	if !strings.Contains(done.Body.String(), `role="status"`) || !strings.Contains(done.Body.String(), "gespeichert") {
		t.Fatal("saving is not confirmed")
	}
	odd := httptest.NewRecorder()
	h.ServeHTTP(odd, httptest.NewRequest(http.MethodGet, "/family/name?done=%3Cb%3Ehello", nil))
	if strings.Contains(odd.Body.String(), "hello") || strings.Contains(odd.Body.String(), `role="status"`) {
		t.Fatal("query text rendered")
	}
	// An empty field removes that name; the other stays.
	if w := namePost(h, url.Values{"csrf_token": {sessionA.CSRFToken}, "for_children": {""}, "for_adults": {"Anna"}}, ""); w.Code != http.StatusSeeOther {
		t.Fatalf("remove one: %d", w.Code)
	}
	if got := names.Get(sessionA.TenantID, sessionA.GuardianID); got != (guardiannames.Names{ForAdults: "Anna"}) {
		t.Fatalf("after removing one: %+v", got)
	}
}

func TestANameThatCannotBeShownIsRefusedWithAnExplanation(t *testing.T) {
	cw := newContactWorld(t)
	names := openNames(t)
	h := namePages(t, testAuth{session: sessionA}, names, cw)
	w := namePost(h, url.Values{"csrf_token": {sessionA.CSRFToken}, "for_children": {"Mami"}, "for_adults": {"Anna <3>"}}, "")
	body := w.Body.String()
	if w.Code != http.StatusBadRequest || !strings.Contains(body, `role="alert"`) || !strings.Contains(body, "40 Zeichen") {
		t.Fatalf("refusal: %d %s", w.Code, body)
	}
	// What was typed is kept in the form, as text.
	if !strings.Contains(body, `name="for_children" value="Mami"`) || !strings.Contains(body, "Anna &lt;3&gt;") || strings.Contains(body, "<3>") {
		t.Fatalf("form after refusal: %s", body)
	}
	if got := names.Get(sessionA.TenantID, sessionA.GuardianID); got != (guardiannames.Names{}) {
		t.Fatalf("a refused name was stored: %+v", got)
	}
}

func TestNameActionsRejectInvalidSubmissions(t *testing.T) {
	cw := newContactWorld(t)
	names := openNames(t)
	if w := namePost(namePages(t, testAuth{err: errors.New("no session")}, names, cw), url.Values{"csrf_token": {sessionA.CSRFToken}, "for_children": {"x"}, "for_adults": {"y"}}, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("without session: %d", w.Code)
	}
	h := namePages(t, testAuth{session: sessionA}, names, cw)
	for name, c := range map[string]struct {
		contentType string
		form        url.Values
		want        int
	}{
		"no csrf":        {"", url.Values{"for_children": {"x"}, "for_adults": {"y"}}, http.StatusBadRequest},
		"wrong csrf":     {"", url.Values{"csrf_token": {"wrong"}, "for_children": {"x"}, "for_adults": {"y"}}, http.StatusForbidden},
		"json":           {"application/json", url.Values{"csrf_token": {sessionA.CSRFToken}, "for_children": {"x"}, "for_adults": {"y"}}, http.StatusUnsupportedMediaType},
		"field missing":  {"", url.Values{"csrf_token": {sessionA.CSRFToken}, "for_children": {"x"}}, http.StatusBadRequest},
		"field repeated": {"", url.Values{"csrf_token": {sessionA.CSRFToken}, "for_children": {"x", "z"}, "for_adults": {"y"}}, http.StatusBadRequest},
	} {
		if w := namePost(h, c.form, c.contentType); w.Code != c.want {
			t.Errorf("%s: %d, want %d", name, w.Code, c.want)
		}
	}
	if got := names.Get(sessionA.TenantID, sessionA.GuardianID); got != (guardiannames.Names{}) {
		t.Fatalf("an invalid submission stored a name: %+v", got)
	}
	if _, err := NewNamePages(nil, names, nil); err == nil {
		t.Error("nil authenticator accepted")
	}
	if _, err := NewNamePages(testAuth{session: sessionA}, nil, nil); err == nil {
		t.Error("nil store accepted")
	}
}

func TestTheOverviewLeadsToTheNamePageAndShowsTheCurrentNames(t *testing.T) {
	cw := newChatWorld(t)
	names := openNames(t)
	none := cw.homeAt(t, Options{Names: names}, "/family")
	if !strings.Contains(none, `href="/family/name"`) || !strings.Contains(none, "Mein Name") || !strings.Contains(none, "Noch kein Name") {
		t.Fatalf("overview without names: %s", none)
	}
	if err := names.Set(sessionA.TenantID, sessionA.GuardianID, guardiannames.Names{ForChildren: "Mami", ForAdults: "Anna"}); err != nil {
		t.Fatal(err)
	}
	with := cw.homeAt(t, Options{Names: names}, "/family")
	if !strings.Contains(with, "Mami") || !strings.Contains(with, "Anna") || strings.Contains(with, "Noch kein Name") {
		t.Fatalf("overview with names: %s", with)
	}
	if off := cw.homeAt(t, Options{}, "/family"); strings.Contains(off, "/family/name") {
		t.Fatal("the name page is offered although names are off")
	}
}
