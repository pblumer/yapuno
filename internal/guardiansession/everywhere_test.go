package guardiansession

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func issue(t *testing.T, mgr *Manager, guardian, tenant string) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	if err := mgr.IssueVerified(w, guardian, tenant); err != nil {
		t.Fatal(err)
	}
	return w.Result().Cookies()[0]
}

func alive(mgr *Manager, cookie *http.Cookie) bool {
	r := httptest.NewRequest(http.MethodGet, "/family", nil)
	r.AddCookie(cookie)
	_, err := mgr.Authenticate(r)
	return err == nil
}

func TestRevokeAllEndsEverySessionOfExactlyThatGuardian(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	mgr, err := New(func() time.Time { return now }, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	phone, laptop := issue(t, mgr, "guardian-a", "tenant-a"), issue(t, mgr, "guardian-a", "tenant-a")
	partner := issue(t, mgr, "guardian-b", "tenant-a")
	namesake := issue(t, mgr, "guardian-a", "tenant-z")
	if got := mgr.Count("guardian-a", "tenant-a"); got != 2 {
		t.Fatalf("count: %d", got)
	}
	if got := mgr.RevokeAll("guardian-a", "tenant-a"); got != 2 {
		t.Fatalf("revoked: %d", got)
	}
	if alive(mgr, phone) || alive(mgr, laptop) {
		t.Fatal("session survived sign-out everywhere")
	}
	if !alive(mgr, partner) || !alive(mgr, namesake) {
		t.Fatal("another guardian's session was ended")
	}
	if mgr.Count("guardian-a", "tenant-a") != 0 || mgr.Count("guardian-b", "tenant-a") != 1 || mgr.Count("guardian-a", "tenant-z") != 1 {
		t.Fatal("counts wrong after sign-out everywhere")
	}
	if mgr.RevokeAll("guardian-a", "tenant-a") != 0 || mgr.RevokeAll("", "tenant-a") != 0 || mgr.RevokeAll("guardian-b", "") != 0 || !alive(mgr, partner) {
		t.Fatal("repeated or empty sign-out everywhere must change nothing")
	}
	// A new sign-in afterwards works: this is not a lock.
	if !alive(mgr, issue(t, mgr, "guardian-a", "tenant-a")) {
		t.Fatal("new session rejected after sign-out everywhere")
	}
}

func TestCountIgnoresExpiredSessions(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	mgr, err := New(func() time.Time { return now }, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	issue(t, mgr, "guardian-a", "tenant-a")
	now = now.Add(20 * time.Minute)
	issue(t, mgr, "guardian-a", "tenant-a")
	now = now.Add(15 * time.Minute)
	if got := mgr.Count("guardian-a", "tenant-a"); got != 1 {
		t.Fatalf("expired session counted: %d", got)
	}
	if mgr.Count("", "tenant-a") != 0 || mgr.Count("guardian-a", "") != 0 {
		t.Fatal("empty identity counted")
	}
}

func TestClearRemovesTheCookieWithTheSameAttributes(t *testing.T) {
	mgr, err := New(time.Now, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	mgr.Clear(w)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies: %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != CookieName || c.Value != "" || c.MaxAge >= 0 || c.Path != "/" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("clearing cookie wrong: %+v", c)
	}
}
