package main

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/guardiansession"
	"github.com/pblumer/yapuno/internal/signup"
)

var (
	inviteLink = regexp.MustCompile(`https://app\.example\.org/join\?code=([A-Za-z0-9_-]{22})`)
	childLink  = regexp.MustCompile(`/family/devices\?child_id=(c-[0-9a-f]{32})`)
)

func newSignupServer(t *testing.T, withLocks bool) *securityServer {
	t.Helper()
	s := newSecurityServer(t, withLocks)
	s.signupPath = filepath.Join(s.root, "signup.json")
	s.start(withLocks)
	return s
}

// csrf returns the CSRF token shown on a guardian page.
func (s *securityServer) csrf(cookie *http.Cookie, page string) string {
	s.t.Helper()
	m := csrfField.FindStringSubmatch(s.do(http.MethodGet, page, cookie, nil).Body.String())
	if m == nil {
		s.t.Fatalf("no CSRF token on %s", page)
	}
	return m[1]
}

// invite creates an invitation through the real page and returns its code.
func (s *securityServer) invite(cookie *http.Cookie) string {
	s.t.Helper()
	w := s.do(http.MethodPost, "/family/invite", cookie, url.Values{"csrf_token": {s.csrf(cookie, "/family/invite")}})
	m := inviteLink.FindStringSubmatch(w.Body.String())
	if w.Code != http.StatusOK || m == nil {
		s.t.Fatalf("invitation: %d %s", w.Code, w.Body)
	}
	return m[1]
}

// join asks for a confirmation link and returns its token, or "" if no mail was sent.
func (s *securityServer) join(code, email, child string) string {
	s.t.Helper()
	w := s.do(http.MethodPost, "/join/request", nil, url.Values{"code": {code}, "email": {email}, "child_name": {child}, "confirm": {"yes"}})
	if w.Code != http.StatusOK {
		s.t.Fatalf("join request: %d %s", w.Code, w.Body)
	}
	link := s.mailer.registration(email)
	if link == "" {
		return ""
	}
	u, err := url.Parse(link)
	if err != nil || u.Host != "app.example.org" || u.Path != "/join/verify" {
		s.t.Fatalf("registration link %q", link)
	}
	return u.Query().Get("token")
}

func TestInvitedFamilyRegistersItselfEndToEnd(t *testing.T) {
	s := newSignupServer(t, false)
	anna := s.issue("guardian-a", "tenant-a")
	if page := s.do(http.MethodGet, "/family", anna, nil).Body.String(); !strings.Contains(page, `href="/family/invite"`) || !strings.Contains(page, `href="/family/children"`) {
		t.Fatal("family page does not offer the self-service functions")
	}
	if w := s.do(http.MethodGet, "/family/invite", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("invite page without session: %d", w.Code)
	}
	code := s.invite(anna)

	// The invited person has no session and no account.
	if w := s.do(http.MethodGet, "/join?code="+code, nil, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `action="/join/request"`) {
		t.Fatalf("join page: %d", w.Code)
	}
	token := s.join(code, "bruno@example.org", "Leo")
	if token == "" {
		t.Fatal("no confirmation mail")
	}
	if w := s.do(http.MethodGet, "/join/verify?token="+token, nil, nil); w.Code != http.StatusOK {
		t.Fatalf("confirm page: %d", w.Code)
	}
	w := s.do(http.MethodPost, "/join/verify", nil, url.Values{"token": {token}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family" {
		t.Fatalf("verify: %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != guardiansession.CookieName {
		t.Fatalf("session cookie: %+v", cookies)
	}
	bruno := cookies[0]
	page := s.do(http.MethodGet, "/family", bruno, nil).Body.String()
	if !strings.Contains(page, "Leo") || strings.Contains(page, "Alex") || strings.Contains(page, "Bea") {
		t.Fatalf("new family page: %s", page)
	}
	// The new guardian manages their own child like any other guardian.
	first := childLink.FindStringSubmatch(page)
	if first == nil {
		t.Fatal("no device link for the new child")
	}
	if w := s.do(http.MethodGet, "/family/devices?child_id="+first[1], bruno, nil); w.Code != http.StatusOK {
		t.Fatalf("devices of the new child: %d", w.Code)
	}
	// ...and reaches nothing of the inviting family, nor does the inviter reach them.
	if w := s.do(http.MethodGet, "/family/devices?child_id=child-a", bruno, nil); w.Code != http.StatusForbidden {
		t.Fatalf("foreign child: %d", w.Code)
	}
	if w := s.do(http.MethodGet, "/family/devices?child_id="+first[1], anna, nil); w.Code != http.StatusForbidden {
		t.Fatalf("inviter reaches the new child: %d", w.Code)
	}
	if strings.Contains(s.do(http.MethodGet, "/family", anna, nil).Body.String(), "Leo") {
		t.Fatal("inviter sees the new family")
	}
	if strings.Contains(s.do(http.MethodGet, "/family/invite", anna, nil).Body.String(), "bruno") {
		t.Fatal("inviter sees who registered")
	}

	// A second child, created by the guardian.
	if w := s.do(http.MethodPost, "/family/children", bruno, url.Values{"csrf_token": {s.csrf(bruno, "/family/children")}, "name": {"Lia"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("second child: %d %s", w.Code, w.Body)
	}
	if page := s.do(http.MethodGet, "/family", bruno, nil).Body.String(); !strings.Contains(page, "Leo") || !strings.Contains(page, "Lia") || len(childLink.FindAllString(page, -1)) != 2 {
		t.Fatal("second child not shown")
	}

	// From now on the normal sign-in works for the new address.
	loginToken := s.requestLink("bruno@example.org")
	if loginToken == "" {
		t.Fatal("registered guardian gets no sign-in link")
	}
	if w := s.do(http.MethodPost, "/login/verify", nil, url.Values{"token": {loginToken}}); w.Code != http.StatusSeeOther {
		t.Fatalf("sign-in of the registered guardian: %d", w.Code)
	}
	// The invitation is spent, and the same address cannot register again.
	if w := s.do(http.MethodGet, "/join?code="+code, nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("spent invitation: %d", w.Code)
	}
	s.mailer.registrations = nil
	if s.join(s.invite(anna), "bruno@example.org", "Leo") != "" {
		t.Fatal("a registered address received a registration mail")
	}
	// The registered guardian can invite, too; identities are stored privately.
	if s.invite(bruno) == "" {
		t.Fatal("registered guardian cannot invite")
	}
	info, err := os.Lstat(filepath.Join(s.root, "guardians.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("guardian file: %v %v", info, err)
	}
	raw, _ := os.ReadFile(filepath.Join(s.root, "signup.json"))
	if strings.Contains(string(raw), "bruno") || strings.Contains(string(raw), "Leo") || strings.Contains(string(raw), code) {
		t.Fatal("the invitation file holds an address, a name or a code")
	}
}

func TestRegistrationSurvivesARestartOnlyAsFarAsItWasConfirmed(t *testing.T) {
	s := newSignupServer(t, false)
	anna := s.issue("guardian-a", "tenant-a")
	code := s.invite(anna)
	token := s.join(code, "bruno@example.org", "Leo")
	s.start(false)
	// The unconfirmed registration is gone, the invitation is not.
	if w := s.do(http.MethodPost, "/join/verify", nil, url.Values{"token": {token}}); w.Code != http.StatusForbidden {
		t.Fatalf("link after restart: %d", w.Code)
	}
	if w := s.do(http.MethodGet, "/join?code="+code, nil, nil); w.Code != http.StatusOK {
		t.Fatalf("invitation after restart: %d", w.Code)
	}
	s.mailer.registrations = nil
	token = s.join(code, "carla@example.org", "Mo")
	if w := s.do(http.MethodPost, "/join/verify", nil, url.Values{"token": {token}}); w.Code != http.StatusSeeOther {
		t.Fatalf("registration after restart: %d", w.Code)
	}
	// A confirmed family is durable: it is there after another restart.
	s.start(false)
	if s.requestLink("carla@example.org") == "" {
		t.Fatal("registered family lost by a restart")
	}
}

func TestLockingTheInvitersAccessVoidsTheirInvitations(t *testing.T) {
	s := newSignupServer(t, true)
	anna := s.issue("guardian-a", "tenant-a")
	code := s.invite(anna)
	token := s.join(code, "bruno@example.org", "Leo")
	if w := s.act(anna, "lock", url.Values{"confirm": {"yes"}}); w.Code != http.StatusOK {
		t.Fatalf("lock: %d %s", w.Code, w.Body)
	}
	if w := s.do(http.MethodGet, "/join?code="+code, nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("invitation of a locked guardian: %d", w.Code)
	}
	if w := s.do(http.MethodPost, "/join/verify", nil, url.Values{"token": {token}}); w.Code != http.StatusForbidden {
		t.Fatalf("confirmation through a locked guardian's invitation: %d", w.Code)
	}
	if s.requestLink("bruno@example.org") != "" {
		t.Fatal("a family was registered through a locked guardian's invitation")
	}
}

func TestWithoutTheSwitchNobodyRegistersOrInvites(t *testing.T) {
	s := newSecurityServer(t, false)
	anna := s.issue("guardian-a", "tenant-a")
	if page := s.do(http.MethodGet, "/family", anna, nil).Body.String(); strings.Contains(page, "/family/invite") || strings.Contains(page, "/family/children") {
		t.Fatal("self-service offered without the switch")
	}
	for _, path := range []string{"/join", "/join?code=" + strings.Repeat("A", 22), "/join/verify?token=x", "/family/invite", "/family/children"} {
		if w := s.do(http.MethodGet, path, anna, nil); w.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/join/request", "/join/verify", "/family/invite", "/family/children"} {
		w := s.do(http.MethodPost, path, anna, url.Values{"csrf_token": {s.csrf(anna, "/family")}, "name": {"Tim"}, "token": {"x"}})
		if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: %d", path, w.Code)
		}
	}
	if len(s.families.Cards("guardian-a", "tenant-a")) != 1 || len(s.mailer.registrations) != 0 {
		t.Fatal("something was registered without the switch")
	}
}

func TestSelfRegistrationNeedsAMailerThatSendsRegistrationLinks(t *testing.T) {
	s := newSecurityServer(t, false)
	invitations, err := signup.Open(filepath.Join(s.root, "signup.json"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newHandler("https://app.example.org", s.families, s.guardians, &fakeMailer{}, s.sessions, optionalStores{signup: invitations}); err == nil {
		t.Fatal("self-registration accepted without a registration mailer")
	}
	if _, err := newHandler("https://app.example.org", s.families, s.guardians, s.mailer, s.sessions, optionalStores{signup: invitations}); err != nil {
		t.Fatalf("self-registration with a capable mailer: %v", err)
	}
}
