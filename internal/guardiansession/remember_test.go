package guardiansession

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const month = 30 * 24 * time.Hour

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// remembering returns a manager that keeps remembered sessions in path, as a
// freshly started process would.
func remembering(t *testing.T, c *testClock, path string) *Manager {
	t.Helper()
	mgr, err := New(c.now, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Remember(path, month); err != nil {
		t.Fatal(err)
	}
	return mgr
}

func remembered(t *testing.T, mgr *Manager, guardian, tenant string) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	if err := mgr.IssueRemembered(w, guardian, tenant); err != nil {
		t.Fatal(err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies: %d", len(cookies))
	}
	return cookies[0]
}

func sessionOf(mgr *Manager, cookie *http.Cookie) (string, string, string, bool) {
	r := httptest.NewRequest(http.MethodGet, "/family", nil)
	r.AddCookie(cookie)
	s, err := mgr.Authenticate(r)
	return s.GuardianID, s.TenantID, s.CSRFToken, err == nil
}

func newClock() *testClock {
	return &testClock{t: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}
}

func TestWithoutAFileNoSessionIsRemembered(t *testing.T) {
	mgr, err := New(time.Now, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if mgr.Remembers() {
		t.Fatal("remembers without a file")
	}
	w := httptest.NewRecorder()
	if err := mgr.IssueRemembered(w, "guardian-a", "tenant-a"); !errors.Is(err, ErrNotRemembering) {
		t.Fatalf("issue: %v", err)
	}
	if len(w.Result().Cookies()) != 0 || mgr.Count("guardian-a", "tenant-a") != 0 {
		t.Fatal("a session was issued")
	}
}

func TestARememberedSessionSurvivesARestartAndAShortOneDoesNot(t *testing.T) {
	c := newClock()
	path := filepath.Join(t.TempDir(), "sessions.json")
	mgr := remembering(t, c, path)
	if !mgr.Remembers() || mgr.RememberedFor() != month {
		t.Fatalf("policy: %v %v", mgr.Remembers(), mgr.RememberedFor())
	}
	long := remembered(t, mgr, "guardian-a", "tenant-a")
	short := issue(t, mgr, "guardian-a", "tenant-a")
	if long.Name != CookieName || !long.Secure || !long.HttpOnly || long.SameSite != http.SameSiteStrictMode || long.Path != "/" ||
		long.MaxAge != int(month.Seconds()) || !long.Expires.Equal(c.now().Add(month)) {
		t.Fatalf("cookie of a remembered session: %+v", long)
	}
	if short.MaxAge != 30*60 {
		t.Fatalf("a short session got another lifetime: %+v", short)
	}
	guardian, tenant, csrf, ok := sessionOf(mgr, long)
	if !ok || guardian != "guardian-a" || tenant != "tenant-a" || len(csrf) < 32 || csrf == long.Value {
		t.Fatalf("session: %q %q %q %v", guardian, tenant, csrf, ok)
	}
	if mgr.Count("guardian-a", "tenant-a") != 2 {
		t.Fatalf("count: %d", mgr.Count("guardian-a", "tenant-a"))
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("file: %v %v", info, err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), long.Value) || strings.Contains(string(raw), csrf) || strings.Contains(string(raw), short.Value) {
		t.Fatal("the file holds a usable secret")
	}

	restarted := remembering(t, c, path)
	guardian, tenant, again, ok := sessionOf(restarted, long)
	if !ok || guardian != "guardian-a" || tenant != "tenant-a" {
		t.Fatal("remembered session lost by a restart")
	}
	// A form rendered before the restart still carries the right token.
	if again != csrf {
		t.Fatal("the CSRF token changed with the restart")
	}
	if _, _, _, ok := sessionOf(restarted, short); ok {
		t.Fatal("a short session survived the restart")
	}
	if restarted.Count("guardian-a", "tenant-a") != 1 {
		t.Fatalf("count after restart: %d", restarted.Count("guardian-a", "tenant-a"))
	}
}

func TestEverySessionHasItsOwnCSRFToken(t *testing.T) {
	c := newClock()
	mgr := remembering(t, c, filepath.Join(t.TempDir(), "sessions.json"))
	seen := map[string]bool{}
	for _, cookie := range []*http.Cookie{remembered(t, mgr, "g", "t"), remembered(t, mgr, "g", "t"), issue(t, mgr, "g", "t"), issue(t, mgr, "g", "t")} {
		_, _, csrf, ok := sessionOf(mgr, cookie)
		if !ok || len(csrf) < 32 || csrf == cookie.Value || seen[csrf] {
			t.Fatalf("csrf token %q", csrf)
		}
		seen[csrf] = true
	}
}

func TestARememberedSessionEndsAfterItsTime(t *testing.T) {
	c := newClock()
	path := filepath.Join(t.TempDir(), "sessions.json")
	mgr := remembering(t, c, path)
	cookie := remembered(t, mgr, "guardian-a", "tenant-a")
	c.add(month - time.Second)
	if _, _, _, ok := sessionOf(mgr, cookie); !ok {
		t.Fatal("ended too early")
	}
	if _, _, _, ok := sessionOf(remembering(t, c, path), cookie); !ok {
		t.Fatal("ended too early after a restart")
	}
	c.add(time.Second)
	if _, _, _, ok := sessionOf(remembering(t, c, path), cookie); ok {
		t.Fatal("still valid after a restart at its end")
	}
	if _, _, _, ok := sessionOf(mgr, cookie); ok || mgr.Count("guardian-a", "tenant-a") != 0 {
		t.Fatal("still valid at its end")
	}
	// The next change drops what has expired from the file as well.
	remembered(t, mgr, "guardian-b", "tenant-a")
	raw, _ := os.ReadFile(path)
	if strings.Count(string(raw), `"digest"`) != 1 {
		t.Fatalf("expired session kept on disk: %s", raw)
	}
}

func TestSigningOutEndsARememberedSessionForGood(t *testing.T) {
	c := newClock()
	path := filepath.Join(t.TempDir(), "sessions.json")
	mgr := remembering(t, c, path)
	phone, laptop := remembered(t, mgr, "guardian-a", "tenant-a"), remembered(t, mgr, "guardian-a", "tenant-a")
	r := httptest.NewRequest(http.MethodPost, "/family/security/logout", nil)
	r.AddCookie(phone)
	mgr.Revoke(r)
	for name, m := range map[string]*Manager{"running": mgr, "restarted": remembering(t, c, path)} {
		if _, _, _, ok := sessionOf(m, phone); ok {
			t.Errorf("%s: signed-out session accepted", name)
		}
		if _, _, _, ok := sessionOf(m, laptop); !ok {
			t.Errorf("%s: the other device was signed out too", name)
		}
	}
}

func TestSigningOutEverywhereEndsRememberedSessionsOfExactlyThatGuardian(t *testing.T) {
	c := newClock()
	path := filepath.Join(t.TempDir(), "sessions.json")
	mgr := remembering(t, c, path)
	phone, laptop := remembered(t, mgr, "guardian-a", "tenant-a"), issue(t, mgr, "guardian-a", "tenant-a")
	partner := remembered(t, mgr, "guardian-b", "tenant-a")
	namesake := remembered(t, mgr, "guardian-a", "tenant-z")
	if got := mgr.RevokeAll("guardian-a", "tenant-a"); got != 2 {
		t.Fatalf("ended: %d", got)
	}
	for name, m := range map[string]*Manager{"running": mgr, "restarted": remembering(t, c, path)} {
		if _, _, _, ok := sessionOf(m, phone); ok {
			t.Errorf("%s: remembered session survived", name)
		}
		if _, _, _, ok := sessionOf(m, partner); !ok {
			t.Errorf("%s: another guardian was signed out", name)
		}
		if _, _, _, ok := sessionOf(m, namesake); !ok {
			t.Errorf("%s: the same id in another family was signed out", name)
		}
	}
	if _, _, _, ok := sessionOf(mgr, laptop); ok {
		t.Fatal("short session survived")
	}
}

func TestAGuardianKeepsALimitedNumberOfRememberedSessions(t *testing.T) {
	c := newClock()
	path := filepath.Join(t.TempDir(), "sessions.json")
	mgr := remembering(t, c, path)
	other := remembered(t, mgr, "guardian-b", "tenant-a")
	var cookies []*http.Cookie
	for i := 0; i < MaxRememberedPerGuardian+2; i++ {
		c.add(time.Minute)
		cookies = append(cookies, remembered(t, mgr, "guardian-a", "tenant-a"))
	}
	for name, m := range map[string]*Manager{"running": mgr, "restarted": remembering(t, c, path)} {
		// The two oldest made room; nobody else is affected.
		for i, cookie := range cookies {
			if _, _, _, ok := sessionOf(m, cookie); ok != (i >= 2) {
				t.Errorf("%s: session %d valid: %v", name, i, ok)
			}
		}
		if _, _, _, ok := sessionOf(m, other); !ok {
			t.Errorf("%s: another guardian lost a session", name)
		}
		if got := m.Count("guardian-a", "tenant-a"); got != MaxRememberedPerGuardian {
			t.Errorf("%s: count %d", name, got)
		}
	}
}

func TestRememberRejectsBadPolicyAndUnsafeFiles(t *testing.T) {
	c := newClock()
	dir := t.TempDir()
	fresh := func() *Manager {
		mgr, err := New(c.now, 30*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return mgr
	}
	good := filepath.Join(dir, "good.json")
	for name, policy := range map[string]struct {
		path string
		ttl  time.Duration
	}{
		"empty path":            {"", month},
		"not longer than short": {good, 30 * time.Minute},
		"zero":                  {good, 0},
		"longer than the limit": {good, MaxRememberedFor + time.Second},
	} {
		if err := fresh().Remember(policy.path, policy.ttl); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	twice := fresh()
	if err := twice.Remember(good, month); err != nil {
		t.Fatal(err)
	}
	if err := twice.Remember(good, month); err == nil {
		t.Error("configured twice")
	}
	digest := strings.Repeat("a", 64)
	entry := func(d, guardian, tenant, expires string) string {
		return `{"digest":"` + d + `","guardian_id":"` + guardian + `","tenant_id":"` + tenant + `","expires":"` + expires + `"}`
	}
	valid := entry(digest, "g", "t", "2026-12-01T00:00:00Z")
	for name, content := range map[string]string{
		"corrupt":          `{"sessions":[`,
		"unknown field":    `{"sessions":[],"extra":1}`,
		"trailing data":    `{"sessions":[]} {}`,
		"short digest":     `{"sessions":[` + entry("abc", "g", "t", "2026-12-01T00:00:00Z") + `]}`,
		"uppercase digest": `{"sessions":[` + entry(strings.Repeat("A", 64), "g", "t", "2026-12-01T00:00:00Z") + `]}`,
		"no guardian":      `{"sessions":[` + entry(digest, "", "t", "2026-12-01T00:00:00Z") + `]}`,
		"no tenant":        `{"sessions":[` + entry(digest, "g", "", "2026-12-01T00:00:00Z") + `]}`,
		"no expiry":        `{"sessions":[{"digest":"` + digest + `","guardian_id":"g","tenant_id":"t"}]}`,
		"duplicate digest": `{"sessions":[` + valid + `,` + valid + `]}`,
	} {
		path := filepath.Join(t.TempDir(), "sessions.json")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if err := fresh().Remember(path, month); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	open := filepath.Join(t.TempDir(), "sessions.json")
	if err := os.WriteFile(open, []byte(`{"sessions":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := fresh().Remember(open, month); err == nil {
		t.Error("world-readable file opened")
	}
	real := filepath.Join(t.TempDir(), "real.json")
	_ = os.WriteFile(real, []byte(`{"sessions":[]}`), 0600)
	link := filepath.Join(t.TempDir(), "link.json")
	_ = os.Symlink(real, link)
	if err := fresh().Remember(link, month); err == nil {
		t.Error("symlink opened")
	}
	if err := fresh().Remember(filepath.Join(t.TempDir(), "missing", "sessions.json"), month); err == nil {
		t.Error("file in a missing directory accepted")
	}
}

// A session is acknowledged with a cookie only after it is on disk.
func TestAFailedWriteIssuesNoRememberedSession(t *testing.T) {
	c := newClock()
	path := filepath.Join(t.TempDir(), "sessions.json")
	mgr := remembering(t, c, path)
	kept := remembered(t, mgr, "guardian-a", "tenant-a")
	mgr.failWrite = true
	w := httptest.NewRecorder()
	if err := mgr.IssueRemembered(w, "guardian-a", "tenant-a"); err == nil {
		t.Fatal("issued without a durable write")
	}
	if len(w.Result().Cookies()) != 0 || mgr.Count("guardian-a", "tenant-a") != 1 {
		t.Fatal("a session exists although the write failed")
	}
	mgr.failWrite = false
	if _, _, _, ok := sessionOf(mgr, kept); !ok {
		t.Fatal("the failed write damaged an existing session")
	}
	if _, _, _, ok := sessionOf(remembering(t, c, path), kept); !ok {
		t.Fatal("the failed write damaged the file")
	}
}

// Ending a session must win, also when the disk does not cooperate: a
// session that was ended is never accepted again after a restart.
func TestAnEndedSessionDoesNotComeBackWhenTheWriteFails(t *testing.T) {
	for name, end := range map[string]func(*Manager, *http.Cookie){
		"sign out": func(m *Manager, cookie *http.Cookie) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			r.AddCookie(cookie)
			m.Revoke(r)
		},
		"sign out everywhere": func(m *Manager, _ *http.Cookie) { m.RevokeAll("guardian-a", "tenant-a") },
	} {
		c := newClock()
		path := filepath.Join(t.TempDir(), "sessions.json")
		mgr := remembering(t, c, path)
		cookie := remembered(t, mgr, "guardian-a", "tenant-a")
		mgr.failWrite = true
		end(mgr, cookie)
		if _, _, _, ok := sessionOf(mgr, cookie); ok {
			t.Errorf("%s: session still accepted", name)
		}
		if _, _, _, ok := sessionOf(remembering(t, c, path), cookie); ok {
			t.Errorf("%s: ended session accepted after a restart", name)
		}
	}
}

func TestConcurrentSignInsAndSignOutsKeepTheFileConsistent(t *testing.T) {
	c := newClock()
	path := filepath.Join(t.TempDir(), "sessions.json")
	mgr := remembering(t, c, path)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			_ = mgr.IssueRemembered(w, "guardian-a", "tenant-a")
		}()
		go func() {
			defer wg.Done()
			mgr.RevokeAll("guardian-a", "tenant-a")
		}()
	}
	wg.Wait()
	survivor := remembered(t, mgr, "guardian-b", "tenant-a")
	restarted := remembering(t, c, path)
	if _, _, _, ok := sessionOf(restarted, survivor); !ok {
		t.Fatal("file unusable after concurrent changes")
	}
	if restarted.Count("guardian-a", "tenant-a") != mgr.Count("guardian-a", "tenant-a") {
		t.Fatal("file and memory disagree")
	}
}

// A shortened policy or an edited file must not grant more than a new
// sign-in would: such an entry is dropped, the others stay.
func TestAStoredSessionBeyondThePolicyIsNotAccepted(t *testing.T) {
	c := newClock()
	path := filepath.Join(t.TempDir(), "sessions.json")
	mgr := remembering(t, c, path)
	kept := remembered(t, mgr, "guardian-a", "tenant-a")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("made-up-secret"))
	forged := `{"digest":"` + hex.EncodeToString(sum[:]) + `","guardian_id":"guardian-a","tenant_id":"tenant-a","expires":"2027-06-01T00:00:00Z"}`
	edited := strings.Replace(string(raw), `{"sessions":[`, `{"sessions":[`+forged+`,`, 1)
	if err := os.WriteFile(path, []byte(edited), 0600); err != nil {
		t.Fatal(err)
	}
	restarted := remembering(t, c, path)
	if _, _, _, ok := sessionOf(restarted, &http.Cookie{Name: CookieName, Value: "made-up-secret"}); ok {
		t.Fatal("a session that outlives the policy was accepted")
	}
	if _, _, _, ok := sessionOf(restarted, kept); !ok {
		t.Fatal("a valid session was dropped with it")
	}
	// A shorter policy ends sessions that were issued under a longer one.
	shorter, err := New(c.now, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := shorter.Remember(path, 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := sessionOf(shorter, kept); ok {
		t.Fatal("a session outlives the shortened policy")
	}
}

func TestLifetimeIsTheOrdinarySessionTime(t *testing.T) {
	mgr, err := New(time.Now, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if mgr.Lifetime() != 30*time.Minute || mgr.RememberedFor() != 0 {
		t.Fatalf("lifetimes: %v %v", mgr.Lifetime(), mgr.RememberedFor())
	}
}

// A session read from the file is as much a remembered session as one that
// was just issued: it survives the next restart too, and its end is durable.
func TestARememberedSessionSurvivesSeveralRestartsAndCanStillBeEnded(t *testing.T) {
	c := newClock()
	path := filepath.Join(t.TempDir(), "sessions.json")
	phone := remembered(t, remembering(t, c, path), "guardian-a", "tenant-a")
	tablet := remembered(t, remembering(t, c, path), "guardian-a", "tenant-a")
	second := remembering(t, c, path)
	third := remembering(t, c, path)
	for _, cookie := range []*http.Cookie{phone, tablet} {
		if _, _, _, ok := sessionOf(third, cookie); !ok {
			t.Fatal("a remembered session was lost by the second restart")
		}
	}
	_ = second
	// Signing out on a manager that only read the session from the file.
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.AddCookie(phone)
	third.Revoke(r)
	fourth := remembering(t, c, path)
	if _, _, _, ok := sessionOf(fourth, phone); ok {
		t.Fatal("a session ended after a restart came back")
	}
	if _, _, _, ok := sessionOf(fourth, tablet); !ok {
		t.Fatal("the other device was signed out")
	}
	if fourth.RevokeAll("guardian-a", "tenant-a") != 1 {
		t.Fatal("sign out everywhere missed a session read from the file")
	}
	if _, _, _, ok := sessionOf(remembering(t, c, path), tablet); ok {
		t.Fatal("a session ended everywhere came back")
	}
}

// Only what the guardian asked to be remembered is written down.
func TestAnOrdinarySessionIsNeverWrittenToTheFile(t *testing.T) {
	c := newClock()
	path := filepath.Join(t.TempDir(), "sessions.json")
	mgr := remembering(t, c, path)
	short := issue(t, mgr, "guardian-a", "tenant-a")
	// A later change rewrites the file while the short session exists.
	long := remembered(t, mgr, "guardian-b", "tenant-a")
	restarted := remembering(t, c, path)
	if _, _, _, ok := sessionOf(restarted, short); ok {
		t.Fatal("an ordinary session survived the restart")
	}
	if _, _, _, ok := sessionOf(restarted, long); !ok {
		t.Fatal("the remembered session was lost")
	}
	raw, _ := os.ReadFile(path)
	if strings.Count(string(raw), `"digest"`) != 1 {
		t.Fatalf("file: %s", raw)
	}
}

func TestTheLimitCountsOnlyThatGuardianInThatFamily(t *testing.T) {
	c := newClock()
	mgr := remembering(t, c, filepath.Join(t.TempDir(), "sessions.json"))
	// The same guardian id in another family is somebody else.
	namesake := remembered(t, mgr, "guardian-a", "tenant-z")
	for i := 0; i < MaxRememberedPerGuardian; i++ {
		c.add(time.Minute)
		remembered(t, mgr, "guardian-a", "tenant-a")
	}
	if _, _, _, ok := sessionOf(mgr, namesake); !ok {
		t.Fatal("a session of another family made room")
	}
	if mgr.Count("guardian-a", "tenant-a") != MaxRememberedPerGuardian || mgr.Count("guardian-a", "tenant-z") != 1 {
		t.Fatal("counts wrong")
	}
}

// A sign-in that could not be stored must not cost the guardian an older one.
func TestAFailedWriteAtTheLimitEndsNoOlderSession(t *testing.T) {
	c := newClock()
	path := filepath.Join(t.TempDir(), "sessions.json")
	mgr := remembering(t, c, path)
	var cookies []*http.Cookie
	for i := 0; i < MaxRememberedPerGuardian; i++ {
		c.add(time.Minute)
		cookies = append(cookies, remembered(t, mgr, "guardian-a", "tenant-a"))
	}
	mgr.failWrite = true
	if err := mgr.IssueRemembered(httptest.NewRecorder(), "guardian-a", "tenant-a"); err == nil {
		t.Fatal("issued without a durable write")
	}
	mgr.failWrite = false
	for i, cookie := range cookies {
		if _, _, _, ok := sessionOf(mgr, cookie); !ok {
			t.Fatalf("session %d was ended by a sign-in that failed", i)
		}
	}
	if mgr.Count("guardian-a", "tenant-a") != MaxRememberedPerGuardian {
		t.Fatalf("count: %d", mgr.Count("guardian-a", "tenant-a"))
	}
}
