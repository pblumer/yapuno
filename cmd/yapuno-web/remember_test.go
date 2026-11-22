package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/guardiansession"
)

// newRememberingServer keeps remembered sessions in a file. Every start then
// uses a fresh session manager, as a restarted process would.
func newRememberingServer(t *testing.T, withLocks bool) *securityServer {
	t.Helper()
	s := newSecurityServer(t, withLocks)
	s.sessionsPath = filepath.Join(s.root, "guardian-sessions.json")
	s.start(withLocks)
	return s
}

// signIn runs the real sign-in and returns the session cookie.
func (s *securityServer) signIn(email string, remember bool) *http.Cookie {
	s.t.Helper()
	token := s.requestLink(email)
	if token == "" {
		s.t.Fatal("no sign-in link")
	}
	form := url.Values{"token": {token}}
	if remember {
		form.Set("remember", "yes")
	}
	w := s.do(http.MethodPost, "/login/verify", nil, form)
	cookies := w.Result().Cookies()
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family" || len(cookies) != 1 || cookies[0].Name != guardiansession.CookieName {
		s.t.Fatalf("sign-in: %d %q %+v", w.Code, w.Header().Get("Location"), cookies)
	}
	return cookies[0]
}

// remember issues a remembered session directly, like a second device.
func (s *securityServer) remember(guardian, tenant string) *http.Cookie {
	s.t.Helper()
	w := httptest.NewRecorder()
	if err := s.sessions.IssueRemembered(w, guardian, tenant); err != nil {
		s.t.Fatal(err)
	}
	return w.Result().Cookies()[0]
}

func TestAGuardianStaysSignedInOnADeviceAcrossARestart(t *testing.T) {
	s := newRememberingServer(t, false)
	token := s.requestLink("anna@example.org")
	confirm := s.do(http.MethodGet, "/login/verify?token="+token, nil, nil).Body.String()
	if !strings.Contains(confirm, `name="remember" value="yes"`) || !strings.Contains(confirm, "Auf diesem Gerät 30 Tage angemeldet bleiben") || !strings.Contains(confirm, "nach 30 Minuten") {
		t.Fatalf("confirm page: %s", confirm)
	}
	w := s.do(http.MethodPost, "/login/verify", nil, url.Values{"token": {token}, "remember": {"yes"}})
	if w.Code != http.StatusSeeOther || len(w.Result().Cookies()) != 1 {
		t.Fatalf("sign-in: %d", w.Code)
	}
	phone := w.Result().Cookies()[0]
	if phone.MaxAge != 30*24*60*60 || !phone.Secure || !phone.HttpOnly || phone.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie: %+v", phone)
	}
	// A second link for the same address is only sent after a minute, so the
	// ordinary session is issued directly.
	laptop := s.issue("guardian-a", "tenant-a")
	if laptop.MaxAge != 30*60 {
		t.Fatalf("an ordinary sign-in got another lifetime: %+v", laptop)
	}
	if !s.signedIn(phone) || !s.signedIn(laptop) {
		t.Fatal("not signed in")
	}
	// A form of the page before the restart still works after it.
	csrf := s.csrf(phone, "/family/security")

	s.start(false)
	if !s.signedIn(phone) {
		t.Fatal("the remembered sign-in did not survive the restart")
	}
	if s.signedIn(laptop) {
		t.Fatal("an ordinary sign-in survived the restart")
	}
	if page := s.do(http.MethodGet, "/family/security", phone, nil).Body.String(); !strings.Contains(page, "1 aktive Anmeldung.") {
		t.Fatalf("security page: %s", page)
	}
	out := s.do(http.MethodPost, "/family/security/logout", phone, url.Values{"csrf_token": {csrf}})
	if out.Code != http.StatusSeeOther {
		t.Fatalf("sign-out with the earlier form: %d", out.Code)
	}
	s.start(false)
	if s.signedIn(phone) {
		t.Fatal("a signed-out device is signed in again after a restart")
	}
	raw, err := os.ReadFile(s.sessionsPath)
	info, statErr := os.Lstat(s.sessionsPath)
	if err != nil || statErr != nil || info.Mode().Perm() != 0600 || strings.Contains(string(raw), phone.Value) || strings.Contains(string(raw), "anna@") {
		t.Fatalf("session file: %v %v %s", err, statErr, raw)
	}
}

func TestSigningOutEverywhereEndsRememberedSignInsForGood(t *testing.T) {
	s := newRememberingServer(t, false)
	phone := s.signIn("anna@example.org", true)
	s.mailer.links = nil
	partner := s.signIn("ben@example.org", true)
	if w := s.act(phone, "logout-all", url.Values{"confirm": {"yes"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("sign out everywhere: %d", w.Code)
	}
	s.start(false)
	if s.signedIn(phone) {
		t.Fatal("a remembered sign-in survived signing out everywhere")
	}
	if !s.signedIn(partner) {
		t.Fatal("another guardian was signed out")
	}
}

func TestTheEmergencyLockEndsARememberedSignIn(t *testing.T) {
	s := newRememberingServer(t, true)
	phone := s.signIn("anna@example.org", true)
	tablet := s.remember("guardian-a", "tenant-a")
	if w := s.act(phone, "lock", url.Values{"confirm": {"yes"}}); w.Code != http.StatusOK {
		t.Fatalf("lock: %d %s", w.Code, w.Body)
	}
	for _, restart := range []bool{false, true} {
		if restart {
			s.start(true)
		}
		if s.signedIn(phone) || s.signedIn(tablet) {
			t.Fatalf("a locked guardian is signed in (after restart: %v)", restart)
		}
	}
}

// A guardian who is deactivated by the operator loses remembered sign-ins
// with the next request, without anybody touching the session file.
func TestADeactivatedGuardianIsNotSignedInByARememberedSession(t *testing.T) {
	s := newRememberingServer(t, false)
	phone := s.signIn("anna@example.org", true)
	guardians := filepath.Join(s.root, "guardians.json")
	raw, err := os.ReadFile(guardians)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), `"guardian_id":"guardian-a","tenant_id":"tenant-a","active":true`, `"guardian_id":"guardian-a","tenant_id":"tenant-a","active":false`, 1)
	if edited == string(raw) {
		t.Fatal("guardian entry not found")
	}
	if err := os.WriteFile(guardians, []byte(edited), 0600); err != nil {
		t.Fatal(err)
	}
	if s.signedIn(phone) {
		t.Fatal("a deactivated guardian is still signed in")
	}
	s.start(false)
	if s.signedIn(phone) {
		t.Fatal("a deactivated guardian is signed in after a restart")
	}
}

func TestWithoutASessionFileNobodyStaysSignedIn(t *testing.T) {
	s := newSecurityServer(t, false)
	token := s.requestLink("anna@example.org")
	if page := s.do(http.MethodGet, "/login/verify?token="+token, nil, nil).Body.String(); strings.Contains(page, "remember") {
		t.Fatal("staying signed in is offered without a session file")
	}
	w := s.do(http.MethodPost, "/login/verify", nil, url.Values{"token": {token}, "remember": {"yes"}})
	if w.Code != http.StatusSeeOther || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge != 30*60 {
		t.Fatalf("sign-in: %d %+v", w.Code, w.Result().Cookies())
	}
	anna := w.Result().Cookies()[0]
	if page := s.do(http.MethodGet, "/family", anna, nil).Body.String(); !strings.Contains(page, "Angemeldet bleiben") || !strings.Contains(page, "nicht eingeschaltet") {
		t.Fatal("the overview does not list staying signed in as switched off")
	}
	with := newRememberingServer(t, false)
	if page := with.do(http.MethodGet, "/family", with.signIn("anna@example.org", true), nil).Body.String(); strings.Contains(page, "Angemeldet bleiben") {
		t.Fatal("staying signed in is listed as switched off although it is on")
	}
}

func TestTheSignInPageIsStyledAndLeadsAChildsDeviceToPairing(t *testing.T) {
	s := newSecurityServer(t, false)
	page := s.do(http.MethodGet, "/login", nil, nil)
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, `data-yp-skin="classic"`) || !strings.Contains(body, "Bei Yapuno anmelden") ||
		!strings.Contains(body, `href="/pair"`) || !strings.Contains(body, "Gerät eines Kindes verbinden") {
		t.Fatalf("sign-in page: %d %s", page.Code, body)
	}
	// Without pairing the link would lead nowhere.
	handler, err := newHandler("https://app.example.org", s.families, s.guardians, s.mailer, s.sessions, optionalStores{})
	if err != nil {
		t.Fatal(err)
	}
	s.handler = handler
	if body := s.do(http.MethodGet, "/login", nil, nil).Body.String(); strings.Contains(body, "/pair") {
		t.Fatal("pairing offered although it is off")
	}
}

func TestTheSessionFileMustBeUsableAtStart(t *testing.T) {
	sessions, err := guardiansession.New(time.Now, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sessions.json")
	if err := os.WriteFile(path, []byte(`{"sessions":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := sessions.Remember(path, rememberGuardiansFor); err == nil {
		t.Fatal("a world-readable session file was accepted")
	}
	if rememberGuardiansFor != 30*24*time.Hour || guardianSessionLifetime != 30*time.Minute {
		t.Fatalf("policy changed: %v %v", rememberGuardiansFor, guardianSessionLifetime)
	}
}
