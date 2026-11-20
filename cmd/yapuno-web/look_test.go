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

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
	"github.com/pblumer/yapuno/internal/guardiansession"
)

func lookServer(t *testing.T, withLooks bool) (http.Handler, *http.Cookie, string) {
	t.Helper()
	root := t.TempDir()
	families, err := familystore.Open(filepath.Join(root, "families.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := families.Bootstrap(familydashboard.Child{ID: "child-a", TenantID: "tenant-a", DisplayName: "Alex"},
		authz.GuardianGrant{GuardianID: "guardian-a", ChildID: "child-a", TenantID: "tenant-a", Active: true, CanManageDevices: true, CanApproveContacts: true}); err != nil {
		t.Fatal(err)
	}
	guardiansPath := filepath.Join(root, "guardians.json")
	if err := os.WriteFile(guardiansPath, []byte(`{"guardians":[{"email":"parent@example.org","guardian_id":"guardian-a","tenant_id":"tenant-a","active":true}]}`), 0600); err != nil {
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
	deps := optionalStores{}
	if withLooks {
		deps.appearance, err = appearance.Open(filepath.Join(root, "appearance.json"))
		if err != nil {
			t.Fatal(err)
		}
	}
	handler, err := newHandler("https://app.example.org", families, guardians, &fakeMailer{}, sessions, deps)
	if err != nil {
		t.Fatal(err)
	}
	issued := httptest.NewRecorder()
	if err := sessions.IssueVerified(issued, "guardian-a", "tenant-a"); err != nil {
		t.Fatal(err)
	}
	return handler, issued.Result().Cookies()[0], guardiansPath
}

func get(h http.Handler, target string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "https://app.example.org"+target, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestLookPageIsOptional(t *testing.T) {
	h, cookie, _ := lookServer(t, false)
	if w := get(h, "/family/look", cookie); w.Code != http.StatusNotFound {
		t.Fatalf("look page served without appearance store: %d", w.Code)
	}
	if strings.Contains(get(h, "/family", cookie).Body.String(), "/family/look") {
		t.Fatal("family page links to a disabled look page")
	}
}

func TestLookEndToEndWithLiveRevocation(t *testing.T) {
	h, cookie, guardiansPath := lookServer(t, true)
	if w := get(h, "/family/look", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated look page: %d", w.Code)
	}
	page := get(h, "/family/look", cookie)
	if page.Code != http.StatusOK {
		t.Fatalf("look page: %d", page.Code)
	}
	m := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if m == nil {
		t.Fatal("no CSRF token on look page")
	}
	form := url.Values{"csrf_token": {m[1]}, "skin": {"vivid"}, "shape": {"flower"}, "color": {"lime"}}
	r := httptest.NewRequest(http.MethodPost, "https://app.example.org/family/look", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("save look: %d", w.Code)
	}
	family := get(h, "/family", cookie).Body.String()
	if !strings.Contains(family, `data-yp-skin="vivid"`) || !strings.Contains(family, `href="/family/look"`) {
		t.Fatal("family page does not apply the saved look")
	}
	if err := os.WriteFile(guardiansPath, []byte(`{"guardians":[{"email":"parent@example.org","guardian_id":"guardian-a","tenant_id":"tenant-a","active":false}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if w := get(h, "/family/look", cookie); w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked guardian can still open look page: %d", w.Code)
	}
}
