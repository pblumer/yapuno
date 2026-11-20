package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
	"github.com/pblumer/yapuno/internal/guardianlock"
	"github.com/pblumer/yapuno/internal/guardiansession"
)

// noticeMailer records sign-in links and lock notices per address.
type noticeMailer struct {
	mu      sync.Mutex
	links   map[string]string
	notices []string
	// reportNotices holds "address about child name" for every report notice.
	reportNotices []string
}

func (m *noticeMailer) SendSignInLink(_ context.Context, email, link string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.links == nil {
		m.links = make(map[string]string)
	}
	m.links[email] = link
	return nil
}

func (m *noticeMailer) SendAccessLockNotice(_ context.Context, email string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notices = append(m.notices, email)
	return nil
}

func (m *noticeMailer) SendReportNotice(_ context.Context, email, childName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reportNotices = append(m.reportNotices, email+" about "+childName)
	return nil
}

// reports waits briefly for background mail and returns what was sent, sorted.
func (m *noticeMailer) reports(want int) []string {
	deadline := time.Now().Add(2 * time.Second)
	for {
		m.mu.Lock()
		got := append([]string(nil), m.reportNotices...)
		m.mu.Unlock()
		if len(got) >= want || time.Now().After(deadline) {
			sort.Strings(got)
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (m *noticeMailer) link(email string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.links[email]
}

type securityServer struct {
	t         *testing.T
	root      string
	families  *familystore.Store
	guardians *guardiandirectory.Directory
	sessions  *guardiansession.Manager
	pairing   *devicepairing.Store
	mailer    *noticeMailer
	handler   http.Handler
	lockPath  string
}

const threeGuardians = `{"guardians":[{"email":"anna@example.org","guardian_id":"guardian-a","tenant_id":"tenant-a","active":true},{"email":"ben@example.org","guardian_id":"guardian-a2","tenant_id":"tenant-a","active":true},{"email":"bea-parent@example.org","guardian_id":"guardian-b","tenant_id":"tenant-b","active":true}]}`

func newSecurityServer(t *testing.T, withLocks bool) *securityServer {
	t.Helper()
	s := &securityServer{t: t, root: t.TempDir(), mailer: &noticeMailer{}}
	var err error
	if s.families, err = familystore.Open(filepath.Join(s.root, "families.json")); err != nil {
		t.Fatal(err)
	}
	grant := func(guardian, child, tenant string) authz.GuardianGrant {
		return authz.GuardianGrant{GuardianID: guardian, ChildID: child, TenantID: tenant, Active: true, CanManageDevices: true, CanApproveContacts: true}
	}
	for _, c := range []struct{ id, tenant, guardian, name string }{{"child-a", "tenant-a", "guardian-a", "Alex"}, {"child-b", "tenant-b", "guardian-b", "Bea"}} {
		if err := s.families.Bootstrap(familydashboard.Child{ID: c.id, TenantID: c.tenant, DisplayName: c.name}, grant(c.guardian, c.id, c.tenant)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.families.BootstrapGuardian(grant("guardian-a2", "child-a", "tenant-a")); err != nil {
		t.Fatal(err)
	}
	guardiansPath := filepath.Join(s.root, "guardians.json")
	if err := os.WriteFile(guardiansPath, []byte(threeGuardians), 0600); err != nil {
		t.Fatal(err)
	}
	if s.guardians, err = guardiandirectory.New(guardiansPath, s.families); err != nil {
		t.Fatal(err)
	}
	if s.sessions, err = guardiansession.New(time.Now, 30*time.Minute); err != nil {
		t.Fatal(err)
	}
	if s.pairing, err = devicepairing.Open(filepath.Join(s.root, "pairing.json"), s.families, time.Now); err != nil {
		t.Fatal(err)
	}
	s.lockPath = filepath.Join(s.root, "guardian-locks.json")
	s.start(withLocks)
	return s
}

// start builds a fresh handler, like a process restart with the same files.
func (s *securityServer) start(withLocks bool) {
	s.t.Helper()
	opt := optionalStores{pairing: s.pairing}
	if withLocks {
		locks, err := guardianlock.Open(s.lockPath, time.Now)
		if err != nil {
			s.t.Fatal(err)
		}
		opt.guardianLocks = locks
	}
	handler, err := newHandler("https://app.example.org", s.families, s.guardians, s.mailer, s.sessions, opt)
	if err != nil {
		s.t.Fatal(err)
	}
	s.handler = handler
}

func (s *securityServer) do(method, path string, cookie *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, "https://app.example.org"+path, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, r)
	return w
}

func (s *securityServer) issue(guardian, tenant string) *http.Cookie {
	s.t.Helper()
	w := httptest.NewRecorder()
	if err := s.sessions.IssueVerified(w, guardian, tenant); err != nil {
		s.t.Fatal(err)
	}
	return w.Result().Cookies()[0]
}

func (s *securityServer) signedIn(cookie *http.Cookie) bool {
	return s.do(http.MethodGet, "/family", cookie, nil).Code == http.StatusOK
}

// act posts a security action with the CSRF token shown on the page.
func (s *securityServer) act(cookie *http.Cookie, action string, extra url.Values) *httptest.ResponseRecorder {
	s.t.Helper()
	m := csrfField.FindStringSubmatch(s.do(http.MethodGet, "/family/security", cookie, nil).Body.String())
	if m == nil {
		s.t.Fatal("no CSRF token on the security page")
	}
	form := url.Values{"csrf_token": {m[1]}}
	for k, v := range extra {
		form[k] = v
	}
	return s.do(http.MethodPost, "/family/security/"+action, cookie, form)
}

// useLink requests a sign-in link through the real form and returns its token.
func (s *securityServer) requestLink(email string) string {
	s.t.Helper()
	if w := s.do(http.MethodPost, "/login/request", nil, url.Values{"email": {email}}); w.Code != http.StatusOK {
		s.t.Fatalf("login request: %d", w.Code)
	}
	link := s.mailer.link(email)
	if link == "" {
		return ""
	}
	u, err := url.Parse(link)
	if err != nil {
		s.t.Fatal(err)
	}
	return u.Query().Get("token")
}

func TestGuardianSignOutAndSignOutEverywhereEndToEnd(t *testing.T) {
	s := newSecurityServer(t, false)
	phone, laptop, partner := s.issue("guardian-a", "tenant-a"), s.issue("guardian-a", "tenant-a"), s.issue("guardian-a2", "tenant-a")
	if !strings.Contains(s.do(http.MethodGet, "/family", phone, nil).Body.String(), `href="/family/security"`) {
		t.Fatal("family page does not link to the security page")
	}
	if w := s.do(http.MethodGet, "/family/security", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("security page without session: %d", w.Code)
	}
	if page := s.do(http.MethodGet, "/family/security", phone, nil).Body.String(); !strings.Contains(page, "2 aktive Anmeldungen") || !strings.Contains(page, "nicht eingerichtet") {
		t.Fatal("security page does not show the sessions or the missing lock store")
	}
	w := s.act(phone, "logout", nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?notice=signed-out" {
		t.Fatalf("logout: %d %q", w.Code, w.Header().Get("Location"))
	}
	cleared := w.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != guardiansession.CookieName || cleared[0].MaxAge >= 0 {
		t.Fatalf("cookie not cleared: %+v", cleared)
	}
	if !strings.Contains(s.do(http.MethodGet, "/login?notice=signed-out", nil, nil).Body.String(), "Du bist abgemeldet.") {
		t.Fatal("login page does not confirm the sign-out")
	}
	if s.signedIn(phone) || !s.signedIn(laptop) || !s.signedIn(partner) {
		t.Fatal("sign-out must end exactly this session")
	}
	// A sign-in link is waiting in the mailbox when the guardian signs out everywhere.
	pending := s.requestLink("anna@example.org")
	if pending == "" {
		t.Fatal("precondition: link sent")
	}
	phone = s.issue("guardian-a", "tenant-a")
	if w := s.act(laptop, "logout-all", nil); w.Code != http.StatusBadRequest || !s.signedIn(laptop) {
		t.Fatalf("sign-out everywhere without confirmation: %d", w.Code)
	}
	w = s.act(laptop, "logout-all", url.Values{"confirm": {"yes"}, "guardian_id": {"guardian-a2"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?notice=signed-out-everywhere" {
		t.Fatalf("logout-all: %d %q", w.Code, w.Header().Get("Location"))
	}
	if s.signedIn(phone) || s.signedIn(laptop) {
		t.Fatal("a session survived sign-out everywhere")
	}
	if !s.signedIn(partner) {
		t.Fatal("the other guardian was signed out")
	}
	if w := s.do(http.MethodPost, "/login/verify", nil, url.Values{"token": {pending}}); w.Code != http.StatusForbidden {
		t.Fatalf("link from before sign-out everywhere still works: %d", w.Code)
	}
	// Not a lock: a new session works.
	if !s.signedIn(s.issue("guardian-a", "tenant-a")) {
		t.Fatal("guardian cannot sign in again after sign-out everywhere")
	}
	// Without a lock store there is no lock route.
	if w := s.act(partner, "lock", url.Values{"confirm": {"yes"}}); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("lock without lock store: %d", w.Code)
	}
	if !s.signedIn(partner) {
		t.Fatal("refused lock ended the session")
	}
}

func TestGuardianEmergencyLockEndToEnd(t *testing.T) {
	s := newSecurityServer(t, true)
	phone, laptop := s.issue("guardian-a", "tenant-a"), s.issue("guardian-a", "tenant-a")
	partner, otherFamily := s.issue("guardian-a2", "tenant-a"), s.issue("guardian-b", "tenant-b")
	alexDevice, _ := pairingtest.Pair(t, s.pairing, "guardian-a", "tenant-a", "child-a")
	beaDevice, _ := pairingtest.Pair(t, s.pairing, "guardian-b", "tenant-b", "child-b")
	pending := s.requestLink("anna@example.org")
	if pending == "" {
		t.Fatal("precondition: link sent")
	}
	if w := s.act(phone, "lock", nil); w.Code != http.StatusBadRequest || !s.signedIn(phone) {
		t.Fatalf("lock without confirmation: %d", w.Code)
	}
	// Request fields cannot aim the lock at somebody else.
	w := s.act(phone, "lock", url.Values{"confirm": {"yes"}, "devices": {"yes"}, "guardian_id": {"guardian-b"}, "tenant_id": {"tenant-b"}})
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "Dein Zugang ist gesperrt") {
		t.Fatalf("lock: %d", w.Code)
	}
	if !strings.Contains(body, "wurde eine E-Mail gesendet") || !strings.Contains(body, "1 Kindergerät wurde gesperrt") {
		t.Fatal("confirmation does not report what happened")
	}
	if strings.Contains(body, "example.org") || strings.Contains(body, "guardian-a2") {
		t.Fatal("confirmation discloses the other guardian")
	}
	if len(s.mailer.notices) != 1 || s.mailer.notices[0] != "ben@example.org" {
		t.Fatalf("notice recipients: %v", s.mailer.notices)
	}
	if s.signedIn(phone) || s.signedIn(laptop) {
		t.Fatal("a session of the locked guardian survived")
	}
	if !s.signedIn(partner) || !s.signedIn(otherFamily) {
		t.Fatal("somebody else lost access")
	}
	if _, ok := s.pairing.ActiveDevice("tenant-a", "child-a", alexDevice); ok {
		t.Fatal("child device still active although requested")
	}
	if _, ok := s.pairing.ActiveDevice("tenant-b", "child-b", beaDevice); !ok {
		t.Fatal("another family's child device was locked")
	}
	// No way back in: old link, new link, or a session issued by any path.
	if w := s.do(http.MethodPost, "/login/verify", nil, url.Values{"token": {pending}}); w.Code != http.StatusForbidden {
		t.Fatalf("pending link of the locked guardian works: %d", w.Code)
	}
	s.mailer.links = nil
	generic := s.do(http.MethodPost, "/login/request", nil, url.Values{"email": {"anna@example.org"}})
	unknown := s.do(http.MethodPost, "/login/request", nil, url.Values{"email": {"nobody@example.org"}})
	if s.mailer.link("anna@example.org") != "" {
		t.Fatal("a sign-in link was sent to the locked guardian")
	}
	if generic.Code != unknown.Code || generic.Body.String() != unknown.Body.String() {
		t.Fatal("login response reveals the lock")
	}
	if s.signedIn(s.issue("guardian-a", "tenant-a")) {
		t.Fatal("a freshly issued session of the locked guardian is accepted")
	}
	// The other guardian keeps managing the child.
	if w := s.do(http.MethodGet, "/family/devices/lost?child_id=child-a", partner, nil); w.Code != http.StatusOK {
		t.Fatalf("partner cannot manage devices: %d", w.Code)
	}
	// The lock survives a restart.
	s.start(true)
	if s.signedIn(s.issue("guardian-a", "tenant-a")) || !s.signedIn(partner) {
		t.Fatal("lock lost after restart")
	}
	// Only the operator lifts it, by removing the entry from the private file.
	if err := os.WriteFile(s.lockPath, []byte(`{"locks":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if !s.signedIn(s.issue("guardian-a", "tenant-a")) {
		t.Fatal("access not restored after the operator lifted the lock")
	}
	if len(s.mailer.notices) != 1 {
		t.Fatalf("unexpected further notices: %v", s.mailer.notices)
	}
}

func TestEmergencyLockNeedsAMailerThatCanNotify(t *testing.T) {
	s := newSecurityServer(t, false)
	locks, err := guardianlock.Open(s.lockPath, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newHandler("https://app.example.org", s.families, s.guardians, &fakeMailer{}, s.sessions, optionalStores{guardianLocks: locks}); err == nil {
		t.Fatal("handler built with a lock store but without a way to notify")
	}
}

func TestBrokenLockStoreDeniesGuardiansInsteadOfUnlocking(t *testing.T) {
	s := newSecurityServer(t, true)
	cookie := s.issue("guardian-a2", "tenant-a")
	if !s.signedIn(cookie) {
		t.Fatal("precondition")
	}
	if err := os.Remove(s.lockPath); err != nil {
		t.Fatal(err)
	}
	if s.signedIn(cookie) {
		t.Fatal("missing lock file must deny, not allow")
	}
}
