package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
	"github.com/pblumer/yapuno/internal/guardiansession"
)

const twoGuardians = `{"guardians":[{"email":"a@example.org","guardian_id":"guardian-a","tenant_id":"tenant-a","active":true},{"email":"b@example.org","guardian_id":"guardian-b","tenant_id":"tenant-b","active":%t}]}`

type contactServer struct {
	handler       http.Handler
	cookieA       *http.Cookie
	cookieB       *http.Cookie
	contacts      *contacts.Store
	guardiansPath string
}

func newContactServer(t *testing.T, withContacts bool) contactServer {
	t.Helper()
	root := t.TempDir()
	families, err := familystore.Open(filepath.Join(root, "families.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, tenant, guardian, name string }{{"child-a", "tenant-a", "guardian-a", "Alex"}, {"child-b", "tenant-b", "guardian-b", "Bea"}} {
		grant := authz.GuardianGrant{GuardianID: c.guardian, ChildID: c.id, TenantID: c.tenant, Active: true, CanManageDevices: true, CanApproveContacts: true}
		if err := families.Bootstrap(familydashboard.Child{ID: c.id, TenantID: c.tenant, DisplayName: c.name}, grant); err != nil {
			t.Fatal(err)
		}
	}
	guardiansPath := filepath.Join(root, "guardians.json")
	if err := os.WriteFile(guardiansPath, []byte(strings.Replace(twoGuardians, "%t", "true", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	guardians, err := guardiandirectory.New(guardiansPath, families)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := guardiansession.New(time.Now, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s := contactServer{guardiansPath: guardiansPath}
	deps := optionalStores{}
	if withContacts {
		s.contacts, err = contacts.Open(filepath.Join(root, "contacts.json"), families, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		deps.contacts = s.contacts
	}
	s.handler, err = newHandler("https://app.example.org", families, guardians, &fakeMailer{}, sessions, deps)
	if err != nil {
		t.Fatal(err)
	}
	issue := func(guardian, tenant string) *http.Cookie {
		w := httptest.NewRecorder()
		if err := sessions.IssueVerified(w, guardian, tenant); err != nil {
			t.Fatal(err)
		}
		return w.Result().Cookies()[0]
	}
	s.cookieA, s.cookieB = issue("guardian-a", "tenant-a"), issue("guardian-b", "tenant-b")
	return s
}

var csrfField = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

func (s contactServer) csrf(t *testing.T, cookie *http.Cookie, child string) string {
	t.Helper()
	m := csrfField.FindStringSubmatch(get(s.handler, "/family/contacts?child_id="+child, cookie).Body.String())
	if m == nil {
		t.Fatal("no CSRF token on contact page")
	}
	return m[1]
}

func (s contactServer) post(cookie *http.Cookie, action string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "https://app.example.org/family/contacts/"+action, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, r)
	return w
}

func TestContactPagesAreOptional(t *testing.T) {
	s := newContactServer(t, false)
	if w := get(s.handler, "/family/contacts?child_id=child-a", s.cookieA); w.Code != http.StatusNotFound {
		t.Fatalf("contact page served without contact store: %d", w.Code)
	}
	if strings.Contains(get(s.handler, "/family", s.cookieA).Body.String(), "/family/contacts") {
		t.Fatal("family page links to disabled contact pages")
	}
}

func TestCrossFamilyContactEndToEndWithLiveRevocation(t *testing.T) {
	s := newContactServer(t, true)
	alex := contacts.Party{TenantID: "tenant-a", ChildID: "child-a"}
	bea := contacts.Party{TenantID: "tenant-b", ChildID: "child-b"}
	if w := get(s.handler, "/family/contacts?child_id=child-a", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	if !strings.Contains(get(s.handler, "/family", s.cookieA).Body.String(), `href="/family/contacts?child_id=child-a"`) {
		t.Fatal("family page does not link to contacts")
	}
	// Each guardian's session only reaches their own family's child.
	if w := get(s.handler, "/family/contacts?child_id=child-b", s.cookieA); w.Code != http.StatusForbidden {
		t.Fatalf("guardian A opened family B's child: %d", w.Code)
	}
	csrfA, csrfB := s.csrf(t, s.cookieA, "child-a"), s.csrf(t, s.cookieB, "child-b")
	invited := s.post(s.cookieA, "invite", url.Values{"csrf_token": {csrfA}, "child_id": {"child-a"}})
	m := regexp.MustCompile(`<output[^>]*>([A-Z2-7-]+)</output>`).FindStringSubmatch(invited.Body.String())
	if invited.Code != http.StatusOK || m == nil {
		t.Fatalf("invite: %d", invited.Code)
	}
	// A session's CSRF token is not valid for another guardian's session.
	if w := s.post(s.cookieB, "redeem", url.Values{"csrf_token": {csrfA}, "child_id": {"child-b"}, "code": {m[1]}}); w.Code != http.StatusForbidden {
		t.Fatalf("foreign CSRF token accepted: %d", w.Code)
	}
	if w := s.post(s.cookieB, "redeem", url.Values{"csrf_token": {csrfB}, "child_id": {"child-b"}, "code": {m[1]}}); w.Code != http.StatusSeeOther {
		t.Fatalf("redeem: %d", w.Code)
	}
	link := regexp.MustCompile(`name="link_id" value="([0-9a-f]+)"`).FindStringSubmatch(get(s.handler, "/family/contacts?child_id=child-a", s.cookieA).Body.String())
	if link == nil {
		t.Fatal("request not visible to inviting guardian")
	}
	decide := func(cookie *http.Cookie, csrf, child, action string) int {
		return s.post(cookie, action, url.Values{"csrf_token": {csrf}, "child_id": {child}, "link_id": {link[1]}, "confirm": {"yes"}}).Code
	}
	if code := decide(s.cookieB, csrfB, "child-b", "confirm"); code != http.StatusForbidden {
		t.Fatalf("redeeming family confirmed its own request: %d", code)
	}
	if s.contacts.Allowed(alex, bea) {
		t.Fatal("contact active with one approval")
	}
	if code := decide(s.cookieA, csrfA, "child-a", "confirm"); code != http.StatusSeeOther {
		t.Fatalf("confirm: %d", code)
	}
	if !s.contacts.Allowed(alex, bea) {
		t.Fatal("contact not active after both approvals")
	}
	// Operator revokes guardian B: the next request is rejected.
	if err := os.WriteFile(s.guardiansPath, []byte(strings.Replace(twoGuardians, "%t", "false", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if w := get(s.handler, "/family/contacts?child_id=child-b", s.cookieB); w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked guardian still has access: %d", w.Code)
	}
	if code := decide(s.cookieB, csrfB, "child-b", "remove"); code != http.StatusUnauthorized {
		t.Fatalf("revoked guardian removed a contact: %d", code)
	}
	if code := decide(s.cookieA, csrfA, "child-a", "remove"); code != http.StatusSeeOther {
		t.Fatalf("remove: %d", code)
	}
	if s.contacts.Allowed(alex, bea) {
		t.Fatal("contact still active after removal")
	}
}
