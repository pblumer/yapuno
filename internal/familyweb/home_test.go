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

func TestALongVersionIsShortenedSoItFitsAPhone(t *testing.T) {
	cw := newChatWorld(t)
	commit := "0784135a9c1e4f6b8d2a3c5e7f9012345678abcd"
	body := cw.home(t, testAuth{session: sessionA}, Options{Version: commit}).Body.String()
	if !strings.Contains(body, "Version 0784135a9c1e") || strings.Contains(body, commit) {
		t.Fatal("a full commit id is shown unshortened")
	}
	if body := cw.home(t, testAuth{session: sessionA}, Options{Version: "v1.2"}).Body.String(); !strings.Contains(body, "Version v1.2") {
		t.Fatal("a short version is changed")
	}
	// Whatever the build passes in is shown as text only.
	if body := cw.home(t, testAuth{session: sessionA}, Options{Version: "<b>x</b>"}).Body.String(); strings.Contains(body, "<b>x</b>") {
		t.Fatal("version rendered as markup")
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

func (cw *chatWorld) homeAt(t *testing.T, opt Options, target string) string {
	t.Helper()
	h, err := NewWithOptions(testAuth{session: sessionA}, cw.families, opt)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("%s: %d", target, w.Code)
	}
	return w.Body.String()
}

// A guardian who has just registered through an invitation learns whether
// the children were connected, as they asked.
func TestTheOverviewSaysHowARegistrationWithConnectionEnded(t *testing.T) {
	cw := newChatWorld(t)
	opt := Options{ContactPages: true, DevicePages: true, SecurityPages: true}
	connected := cw.homeAt(t, opt, "/family?done=connected")
	if !strings.Contains(connected, `role="status"`) || !strings.Contains(connected, "Kinder sind verbunden") || strings.Contains(connected, `role="alert"`) {
		t.Fatalf("connected: %s", connected)
	}
	unconnected := cw.homeAt(t, opt, "/family?done=unconnected")
	if !strings.Contains(unconnected, `role="alert"`) || !strings.Contains(unconnected, "nicht verbunden") || !strings.Contains(unconnected, "Kontakte") || strings.Contains(unconnected, `role="status"`) {
		t.Fatalf("unconnected: %s", unconnected)
	}
	for _, target := range []string{"/family", "/family?done=", "/family?done=%3Cb%3Ehello", "/family?done=Connected"} {
		if body := cw.homeAt(t, opt, target); strings.Contains(body, `role="status"`) || strings.Contains(body, `role="alert"`) || strings.Contains(body, "hello") {
			t.Errorf("%s shows a notice", target)
		}
	}
}

func TestTheGuideDescribesTheShortestWayThisInstallationOffers(t *testing.T) {
	cw := newChatWorld(t)
	// Invitations and contacts: one invitation registers and connects.
	both := cw.homeAt(t, Options{ContactPages: true, DevicePages: true, InvitePages: true, Children: cw.families}, "/family")
	for _, want := range []string{"wähle das Kind", "stimmt der Verbindung zu", "bereits registriert"} {
		if !strings.Contains(both, want) {
			t.Errorf("guide lacks %q", want)
		}
	}
	// Invitations without contacts cannot connect anybody.
	inviteOnly := cw.homeAt(t, Options{DevicePages: true, InvitePages: true, Children: cw.families}, "/family")
	if strings.Contains(inviteOnly, "wähle das Kind") || !strings.Contains(inviteOnly, "Kontakte zwischen Familien sind auf dieser Installation nicht eingeschaltet") {
		t.Fatalf("guide promises a connection without contacts: %s", inviteOnly)
	}
	// Contacts without invitations: the code is the only way.
	contactsOnly := cw.homeAt(t, Options{ContactPages: true, DevicePages: true}, "/family")
	if strings.Contains(contactsOnly, "wähle das Kind") || strings.Contains(contactsOnly, "bereits registriert") || !strings.Contains(contactsOnly, "erstelle einen Code") {
		t.Fatalf("guide: %s", contactsOnly)
	}
}

// Staying signed in could not be stored: the guardian is signed in, but only
// for the ordinary time, and is told so.
func TestTheOverviewSaysWhenStayingSignedInDidNotWork(t *testing.T) {
	cw := newChatWorld(t)
	body := cw.homeAt(t, Options{SecurityPages: true}, "/family?done=not-remembered")
	if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, "nicht gespeichert") || strings.Contains(body, `role="status"`) {
		t.Fatalf("overview: %s", body)
	}
}
