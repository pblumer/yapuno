package guardianlogin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type directory struct {
	principal Principal
	errorFlag bool
}

func (d *directory) LookupGuardian(_ context.Context, email string) (Principal, error) {
	if d.errorFlag {
		return Principal{}, errors.New("directory offline")
	}
	if strings.EqualFold(email, d.principal.Email) {
		return d.principal, nil
	}
	return Principal{}, nil
}

type sender struct {
	links []string
	fail  bool
}

func (s *sender) SendSignInLink(_ context.Context, _, link string) error {
	if s.fail {
		return errors.New("mailer failed")
	}
	s.links = append(s.links, link)
	return nil
}

type sessionIssuer struct {
	mu                   sync.Mutex
	count                int
	guardianID, tenantID string
}

func (s *sessionIssuer) IssueVerified(_ http.ResponseWriter, guardianID, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count++
	s.guardianID = guardianID
	s.tenantID = tenantID
	return nil
}
func (s *sessionIssuer) calls() int { s.mu.Lock(); defer s.mu.Unlock(); return s.count }

func harness(t *testing.T) (*Handler, *directory, *sender, *sessionIssuer, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	dir := &directory{principal: Principal{GuardianID: "g-1", TenantID: "t-1", Email: "parent@example.org", Active: true}}
	email := &sender{}
	sessions := &sessionIssuer{}
	h, err := New(Config{Origin: "https://yapuno.example.org", Directory: dir, Sender: email, Sessions: sessions, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	return h, dir, email, sessions, &now
}
func start(h *Handler, email string) *httptest.ResponseRecorder {
	form := url.Values{"email": {email}}
	r := httptest.NewRequest(http.MethodPost, "https://yapuno.example.org/login/request", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.Routes().ServeHTTP(w, r)
	return w
}
func verify(h *Handler, token string) *httptest.ResponseRecorder {
	form := url.Values{"token": {token}}
	r := httptest.NewRequest(http.MethodPost, "https://yapuno.example.org/login/verify", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.Routes().ServeHTTP(w, r)
	return w
}
func tokenFromLink(t *testing.T, link string) string {
	t.Helper()
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "yapuno.example.org" || parsed.Scheme != "https" || parsed.Path != "/login/verify" {
		t.Fatalf("untrusted link: %q", link)
	}
	return parsed.Query().Get("token")
}
func TestOneTimeEmailLoginAndLinkPreview(t *testing.T) {
	h, _, s, sessions, _ := harness(t)
	w := start(h, " Parent@Example.Org ")
	if w.Code != http.StatusOK || len(s.links) != 1 {
		t.Fatalf("link not sent: %d %+v", w.Code, s.links)
	}
	token := tokenFromLink(t, s.links[0])
	if len(token) != 43 {
		t.Fatalf("weak token len %d", len(token))
	}
	r := httptest.NewRequest(http.MethodGet, "https://yapuno.example.org/login/verify?token="+url.QueryEscape(token), nil)
	preview := httptest.NewRecorder()
	h.Routes().ServeHTTP(preview, r)
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), "Jetzt anmelden") || preview.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("invalid preview: %d", preview.Code)
	}
	if sessions.calls() != 0 {
		t.Fatal("GET consumed login link")
	}
	if code := verify(h, token).Code; code != http.StatusSeeOther {
		t.Fatalf("first use %d", code)
	}
	if sessions.calls() != 1 || sessions.guardianID != "g-1" || sessions.tenantID != "t-1" {
		t.Fatalf("wrong identity issued: %+v", sessions)
	}
	if code := verify(h, token).Code; code != http.StatusForbidden {
		t.Fatalf("replay accepted: %d", code)
	}
}
func TestUnknownAndDisabledAccountGeneric(t *testing.T) {
	h, dir, s, session, _ := harness(t)
	known := start(h, "parent@example.org")
	unknown := start(h, "unknown@example.org")
	if known.Code != unknown.Code || known.Body.String() != unknown.Body.String() {
		t.Fatal("account enumeration via response")
	}
	if len(s.links) != 1 {
		t.Fatal("unknown email got link")
	}
	dir.principal.Active = false
	h2, _, s2, _, _ := harness(t)
	h2.cfg.Directory = dir
	if code := start(h2, "parent@example.org").Code; code != http.StatusOK || len(s2.links) != 0 {
		t.Fatal("disabled guardian received link")
	}
	if session.calls() != 0 {
		t.Fatal("a session was issued")
	}
}
func TestExpirationRevocationAndThrottling(t *testing.T) {
	h, dir, s, sessions, now := harness(t)
	start(h, "parent@example.org")
	first := tokenFromLink(t, s.links[0])
	start(h, "parent@example.org")
	if len(s.links) != 1 {
		t.Fatal("email cooldown bypass")
	}
	*now = now.Add(11 * time.Minute)
	if code := verify(h, first).Code; code != http.StatusForbidden {
		t.Fatalf("expired link accepted: %d", code)
	}
	start(h, "parent@example.org")
	if len(s.links) != 2 {
		t.Fatal("link not reissued after cooldown")
	}
	dir.principal.Active = false
	if code := verify(h, tokenFromLink(t, s.links[1])).Code; code != http.StatusForbidden {
		t.Fatalf("revoked identity accepted: %d", code)
	}
	if sessions.calls() != 0 {
		t.Fatal("session issued after expiry or revocation")
	}
}
func TestInvalidEmailAndOriginAndDeliveryFailure(t *testing.T) {
	h, _, s, sessions, _ := harness(t)
	for _, email := range []string{"", "bad", "user@example.org\nBcc:attacker@example.org", "A <a@example.org>"} {
		if code := start(h, email).Code; code != http.StatusBadRequest {
			t.Fatalf("invalid email %q: %d", email, code)
		}
	}
	for _, origin := range []string{"http://yapuno.example.org", "https://evil.test/path", "https://user:secret@yapuno.example.org", "https://yapuno.example.org?redirect=evil"} {
		cfg := h.cfg
		cfg.Origin = origin
		if _, err := New(cfg); err == nil {
			t.Fatalf("accepted invalid origin: %s", origin)
		}
	}
	s.fail = true
	if code := start(h, "parent@example.org").Code; code != http.StatusOK {
		t.Fatalf("delivery error leaked: %d", code)
	}
	if len(h.pending) != 0 || sessions.calls() != 0 {
		t.Fatal("failed delivery left an active link")
	}
}
func TestConcurrentSingleConsumption(t *testing.T) {
	h, _, s, sessions, _ := harness(t)
	start(h, "parent@example.org")
	token := tokenFromLink(t, s.links[0])
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); verify(h, token) }()
	}
	wg.Wait()
	if sessions.calls() != 1 {
		t.Fatalf("replay race: %d sessions", sessions.calls())
	}
}
