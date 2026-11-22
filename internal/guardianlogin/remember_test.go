package guardianlogin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// rememberingIssuer records which kind of session was issued.
type rememberingIssuer struct {
	sessionIssuer
	remembered int
	failLong   bool
	failShort  bool
}

func (s *rememberingIssuer) IssueRemembered(_ http.ResponseWriter, guardianID, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failLong {
		return errors.New("disk full")
	}
	s.remembered++
	s.guardianID, s.tenantID = guardianID, tenantID
	return nil
}

func (s *rememberingIssuer) IssueVerified(w http.ResponseWriter, guardianID, tenantID string) error {
	if s.failShort {
		return errors.New("no entropy")
	}
	return s.sessionIssuer.IssueVerified(w, guardianID, tenantID)
}

func (s *rememberingIssuer) longCalls() int { s.mu.Lock(); defer s.mu.Unlock(); return s.remembered }

// rememberingHarness is an installation on which guardians can stay signed in.
func rememberingHarness(t *testing.T) (*Handler, *sender, *rememberingIssuer) {
	t.Helper()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	dir := &directory{principal: Principal{GuardianID: "g-1", TenantID: "t-1", Email: "parent@example.org", Active: true}}
	mail, sessions := &sender{}, &rememberingIssuer{}
	h, err := New(Config{Origin: "https://yapuno.example.org", Directory: dir, Sender: mail, Sessions: sessions, Now: func() time.Time { return now },
		Remembered: sessions, RememberDays: 30, SessionMinutes: 30})
	if err != nil {
		t.Fatal(err)
	}
	return h, mail, sessions
}

func get(h *Handler, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.Routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://yapuno.example.org"+target, nil))
	return w
}

func post(h *Handler, target string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "https://yapuno.example.org"+target, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.Routes().ServeHTTP(w, r)
	return w
}

func linkToken(t *testing.T, h *Handler, s *sender) string {
	t.Helper()
	before := len(s.links)
	start(h, "parent@example.org")
	if len(s.links) != before+1 {
		t.Fatal("no sign-in link")
	}
	return tokenFromLink(t, s.links[before])
}

// The device that confirms the link is the one that gets the session, so
// that is where the guardian decides to stay signed in.
func TestTheLastStepOffersToStaySignedInOnThisDevice(t *testing.T) {
	h, s, sessions := rememberingHarness(t)
	token := linkToken(t, h, s)
	page := get(h, "/login/verify?token="+token).Body.String()
	for _, want := range []string{`<input type="checkbox" name="remember" value="yes">`, "Auf diesem Gerät 30 Tage angemeldet bleiben",
		"das nur du benutzt", "nach 30 Minuten", "überall ab", "Jetzt anmelden"} {
		if !strings.Contains(page, want) {
			t.Errorf("confirm page lacks %q", want)
		}
	}
	// The guardian decides; nothing is ticked for them.
	if strings.Contains(page, "checked") {
		t.Fatal("staying signed in is preselected")
	}
	// The first page asks for the address only.
	if strings.Contains(get(h, "/login").Body.String(), `name="remember"`) {
		t.Fatal("the choice is offered on the device that asks for the link")
	}
	w := post(h, "/login/verify", url.Values{"token": {token}, "remember": {"yes"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family" {
		t.Fatalf("verify: %d %q", w.Code, w.Header().Get("Location"))
	}
	if sessions.longCalls() != 1 || sessions.calls() != 0 || sessions.guardianID != "g-1" || sessions.tenantID != "t-1" {
		t.Fatalf("sessions: %d remembered, %d short", sessions.longCalls(), sessions.calls())
	}
}

func TestWithoutTheTickTheSessionIsShort(t *testing.T) {
	for name, form := range map[string]url.Values{
		"no field":    {},
		"empty":       {"remember": {""}},
		"other value": {"remember": {"on"}},
		"upper case":  {"remember": {"YES"}},
		"padded":      {"remember": {" yes"}},
	} {
		h, s, sessions := rememberingHarness(t)
		form.Set("token", linkToken(t, h, s))
		w := post(h, "/login/verify", form)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family" || sessions.longCalls() != 0 || sessions.calls() != 1 {
			t.Errorf("%s: %d %q, %d remembered, %d short", name, w.Code, w.Header().Get("Location"), sessions.longCalls(), sessions.calls())
		}
	}
}

func TestAnAmbiguousChoiceIsRefusedAndTheLinkStaysUsable(t *testing.T) {
	h, s, sessions := rememberingHarness(t)
	token := linkToken(t, h, s)
	if w := post(h, "/login/verify", url.Values{"token": {token}, "remember": {"yes", "no"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("two answers: %d", w.Code)
	}
	if sessions.longCalls()+sessions.calls() != 0 {
		t.Fatal("a session was issued")
	}
	if w := post(h, "/login/verify", url.Values{"token": {token}, "remember": {"yes"}}); w.Code != http.StatusSeeOther || sessions.longCalls() != 1 {
		t.Fatalf("the refused request used up the link: %d", w.Code)
	}
}

// The link is spent by then, so the guardian is signed in for a short time
// and told that staying signed in did not work.
func TestIfTheSessionCannotBeStoredTheGuardianIsSignedInBrieflyAndTold(t *testing.T) {
	h, s, sessions := rememberingHarness(t)
	sessions.failLong = true
	w := post(h, "/login/verify", url.Values{"token": {linkToken(t, h, s)}, "remember": {"yes"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family?done=not-remembered" {
		t.Fatalf("verify: %d %q", w.Code, w.Header().Get("Location"))
	}
	if sessions.longCalls() != 0 || sessions.calls() != 1 {
		t.Fatalf("sessions: %d remembered, %d short", sessions.longCalls(), sessions.calls())
	}
	// If no session at all can be issued, nobody is sent to the family page.
	h, s, sessions = rememberingHarness(t)
	sessions.failLong, sessions.failShort = true, true
	if w := post(h, "/login/verify", url.Values{"token": {linkToken(t, h, s)}, "remember": {"yes"}}); w.Code != http.StatusServiceUnavailable || w.Header().Get("Location") != "" {
		t.Fatalf("no session possible: %d %q", w.Code, w.Header().Get("Location"))
	}
}

// Where sessions are not remembered, nothing is offered and nothing obtained.
func TestWithoutRememberedSessionsNothingIsOffered(t *testing.T) {
	h, _, s, sessions, _ := harness(t)
	start(h, "parent@example.org")
	token := tokenFromLink(t, s.links[0])
	if page := get(h, "/login/verify?token="+token).Body.String(); strings.Contains(page, "remember") || strings.Contains(page, "angemeldet bleiben") {
		t.Fatalf("confirm page: %s", page)
	}
	w := post(h, "/login/verify", url.Values{"token": {token}, "remember": {"yes"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family" || sessions.calls() != 1 {
		t.Fatalf("verify: %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestRememberedSessionsNeedACompletePolicy(t *testing.T) {
	h, _, _ := rememberingHarness(t)
	for name, change := range map[string]func(*Config){
		"no days":            func(c *Config) { c.RememberDays = 0 },
		"too many days":      func(c *Config) { c.RememberDays = 32 },
		"no minutes":         func(c *Config) { c.SessionMinutes = 0 },
		"days without store": func(c *Config) { c.Remembered = nil },
	} {
		cfg := h.cfg
		change(&cfg)
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestEveryPageIsGermanStyledUncachedAndWithoutScript(t *testing.T) {
	h, s, _ := rememberingHarness(t)
	token := linkToken(t, h, s)
	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"form":         get(h, "/login"),
		"signed out":   get(h, "/login?notice=signed-out"),
		"asked":        start(h, "other@example.org"),
		"bad address":  start(h, "not an address"),
		"confirm":      get(h, "/login/verify?token="+token),
		"invalid link": post(h, "/login/verify", url.Values{"token": {strings.Repeat("A", 43)}}),
	} {
		body := rec.Body.String()
		if rec.Header().Get("Content-Type") != "text/html; charset=utf-8" || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" ||
			rec.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") ||
			!strings.Contains(rec.Header().Get("Content-Security-Policy"), "form-action 'self'") {
			t.Errorf("%s: headers %v", name, rec.Header())
		}
		if strings.Contains(body, "<script") || !strings.Contains(body, `<html lang="de">`) || !strings.Contains(body, "width=device-width") || !strings.Contains(body, `data-yp-skin="classic"`) {
			t.Errorf("%s: page shell: %s", name, body)
			continue
		}
		style := body[strings.Index(body, "<style>")+7 : strings.Index(body, "</style>")]
		if hex.MatchString(style[strings.Index(style, "body{margin:0}"):]) {
			t.Errorf("%s: component styles must use skin tokens only", name)
		}
		if !strings.Contains(body, "synthetischen Profilen") {
			t.Errorf("%s: preview note missing", name)
		}
	}
}

func TestMessagesAreGermanAndKeepTheirStatus(t *testing.T) {
	h, s, _ := rememberingHarness(t)
	asked := start(h, "parent@example.org")
	if asked.Code != http.StatusOK || !strings.Contains(asked.Body.String(), `role="status"`) || !strings.Contains(asked.Body.String(), "Falls diese Adresse zugelassen ist") ||
		!strings.Contains(asked.Body.String(), "10 Minuten") || !strings.Contains(asked.Body.String(), "Spam") {
		t.Fatalf("answer: %d %s", asked.Code, asked.Body)
	}
	// The answer must not tell a registered address from an unknown one.
	if unknown := start(h, "unknown@example.org"); unknown.Code != asked.Code || unknown.Body.String() != asked.Body.String() {
		t.Fatal("the answer depends on the address")
	}
	bad := start(h, "not an address")
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "gültige E-Mail-Adresse") || !strings.Contains(bad.Body.String(), `href="/login"`) {
		t.Fatalf("bad address: %d %s", bad.Code, bad.Body)
	}
	token := tokenFromLink(t, s.links[0])
	if w := post(h, "/login/verify", url.Values{"token": {token}}); w.Code != http.StatusSeeOther {
		t.Fatal("sign-in failed")
	}
	for name, form := range map[string]url.Values{
		"used link":      {"token": {token}},
		"unknown link":   {"token": {strings.Repeat("B", 43)}},
		"malformed link": {"token": {"short"}},
	} {
		w := post(h, "/login/verify", form)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "ungültig oder abgelaufen") || !strings.Contains(w.Body.String(), `href="/login"`) || strings.Contains(w.Body.String(), "invalid") {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	// Whatever is in the link is shown as text only.
	if page := get(h, "/login/verify?token=%22%3E%3Cb%3Ex").Body.String(); strings.Contains(page, "<b>x") {
		t.Fatal("token rendered as markup")
	}
}

func TestTheSignInPageLeadsAChildsDeviceToPairingOnlyWhereThatExists(t *testing.T) {
	h, _, _ := rememberingHarness(t)
	if page := get(h, "/login").Body.String(); strings.Contains(page, "/pair") {
		t.Fatal("pairing offered although it is off")
	}
	cfg := h.cfg
	cfg.PairingPage = true
	with, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	page := get(with, "/login").Body.String()
	if !strings.Contains(page, `href="/pair"`) || !strings.Contains(page, "Gerät eines Kindes verbinden") || !strings.Contains(page, "Bei Yapuno anmelden") ||
		!strings.Contains(page, `type="email" name="email" autocomplete="email" required maxlength="254"`) || !strings.Contains(page, `action="/login/request"`) {
		t.Fatalf("sign-in page: %s", page)
	}
}
