package guardiansession

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGuardianSessionLifecycleAndIsolation(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	mgr, err := New(func() time.Time { return now }, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	if err := mgr.IssueVerified(w, "guardian-a", "tenant-a"); err != nil {
		t.Fatal(err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != CookieName || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge <= 0 {
		t.Fatalf("cookie security attributes missing: %+v", cookie)
	}
	request := httptest.NewRequest(http.MethodGet, "/family?guardian_id=attacker&tenant_id=elsewhere", nil)
	request.Header.Set("X-Guardian-ID", "attacker")
	request.AddCookie(cookie)
	session, err := mgr.Authenticate(request)
	if err != nil {
		t.Fatal(err)
	}
	if session.GuardianID != "guardian-a" || session.TenantID != "tenant-a" || len(session.CSRFToken) < 32 {
		t.Fatalf("session spoofed or CSRF entropy missing: %+v", session)
	}
	noCookie := httptest.NewRequest(http.MethodGet, "/family", nil)
	if _, err := mgr.Authenticate(noCookie); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("missing session authorized: %v", err)
	}
	mgr.Revoke(request)
	if _, err := mgr.Authenticate(request); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked session accepted: %v", err)
	}
}

func TestSessionExpiryAndDuplicateCookies(t *testing.T) {
	now := time.Now()
	mgr, err := New(func() time.Time { return now }, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	if err := mgr.IssueVerified(w, "guardian", "tenant"); err != nil {
		t.Fatal(err)
	}
	cookie := w.Result().Cookies()[0]
	r := httptest.NewRequest(http.MethodGet, "/family", nil)
	r.AddCookie(cookie)
	r.AddCookie(cookie)
	if _, err := mgr.Authenticate(r); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("duplicate cookie accepted: %v", err)
	}
	r = httptest.NewRequest(http.MethodGet, "/family", nil)
	r.AddCookie(cookie)
	now = now.Add(30 * time.Minute)
	if _, err := mgr.Authenticate(r); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired session accepted: %v", err)
	}
}

func TestRejectInvalidSessionPolicyAndIdentity(t *testing.T) {
	if _, err := New(nil, 30*time.Minute); err == nil {
		t.Fatal("nil clock accepted")
	}
	if _, err := New(time.Now, 0); err == nil {
		t.Fatal("zero TTL accepted")
	}
	mgr, _ := New(time.Now, time.Hour)
	if err := mgr.IssueVerified(httptest.NewRecorder(), "", "tenant"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unverified guardian accepted: %v", err)
	}
}
