package familyweb

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func (cw *chatWorld) home(t *testing.T, auth testAuth, opt Options) *httptest.ResponseRecorder {
	t.Helper()
	h, err := NewWithOptions(auth, cw.families, opt)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family", nil))
	return w
}

func TestTheFamilyOverviewOffersEveryFunctionAsAVisibleControl(t *testing.T) {
	cw := newChatWorld(t)
	opt := Options{Looks: cw.looks, DevicePages: true, ContactPages: true, SecurityPages: true, Children: cw.families, InvitePages: true, Chat: cw.chat, Version: "abc1234"}
	w := cw.home(t, testAuth{session: sessionA}, opt)
	body := w.Body.String()
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("overview: %d %v", w.Code, w.Header())
	}
	family := cw.thread(t, sessionA, 1)
	for _, want := range []string{
		`<html lang="de">`, `data-yp-skin="classic"`, "<h1>Deine Familie</h1>", "Alex",
		`href="/family/devices?child_id=child-a"`, "Geräte von Alex", `href="/family/contacts?child_id=child-a"`, "Kontakte von Alex",
		`href="/family/chat/` + family + `"`, "Mit Alex schreiben", `href="/family/devices/lost?child_id=child-a"`,
		`action="/family/rename"`, `name="csrf_token" value="` + sessionA.CSRFToken + `"`, "Namen ändern",
		`href="/family/chat"`, "Chats", `href="/family/children"`, `href="/family/invite"`, `href="/family/look"`, `href="/family/security"`,
		"So kommt ihr zum ersten Chat", "Version abc1234", "synthetischen Profilen",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	// The old placeholder said nothing could be done and was in English.
	for _, absent := range []string{"Preview only", "Your family", "family profiles", "<script", "Bea", "child-b", "nicht eingeschaltet"} {
		if strings.Contains(body, absent) {
			t.Errorf("overview contains %q", absent)
		}
	}
	style := body[strings.Index(body, "<style>")+7 : strings.Index(body, "</style>")]
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).MatchString(style[strings.Index(style, "body{margin:0}"):]) {
		t.Error("component styles must use skin tokens only")
	}
	// Every action is a link or button, never decoration.
	if strings.Contains(body, `<span class="yp-action">`) {
		t.Error("an action is rendered as plain text")
	}
}

func TestTheOverviewSaysWhatThisInstallationDoesNotOffer(t *testing.T) {
	cw := newChatWorld(t)
	w := cw.home(t, testAuth{session: sessionA}, Options{SecurityPages: true, Inactive: []string{"Geräte verbinden", "Registrierung per Einladung"}})
	body := w.Body.String()
	for _, want := range []string{"Auf dieser Installation nicht eingeschaltet", "Geräte verbinden, Registrierung per Einladung", `href="/family/security"`, "Betreiber"} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	for _, absent := range []string{"/family/devices", "/family/contacts", "/family/chat", "/family/children", "/family/invite", "/family/look", "Version"} {
		if strings.Contains(body, absent) {
			t.Errorf("overview links to %q although it is off", absent)
		}
	}
	// The guide does not send the guardian to pages that do not exist.
	if !strings.Contains(body, "So kommt ihr zum ersten Chat") {
		t.Error("guide missing")
	}
}

func TestTheOverviewShowsOnlyTheGuardiansOwnChildrenAndRights(t *testing.T) {
	cw := newChatWorld(t)
	opt := Options{DevicePages: true, ContactPages: true, SecurityPages: true, Chat: cw.chat}
	stranger := Session{GuardianID: "guardian-a", TenantID: "tenant-b", CSRFToken: "csrf-x-0123456789"}
	body := cw.home(t, testAuth{session: stranger}, opt).Body.String()
	if strings.Contains(body, "Alex") || strings.Contains(body, "Bea") || strings.Contains(body, "child_id=") || !strings.Contains(body, "Noch kein Kind") {
		t.Errorf("overview for a guardian without children: %s", body)
	}
	limited, err := NewWithOptions(testAuth{session: sessionA}, limitedFamilies{}, opt)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	limited.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/family", nil))
	page := rec.Body.String()
	if strings.Contains(page, "/family/devices") || strings.Contains(page, "/family/contacts") || strings.Contains(page, `action="/family/rename"`) || !strings.Contains(page, "keine Verwaltungsrechte") {
		t.Errorf("overview without rights: %s", page)
	}
}
