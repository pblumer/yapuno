package guardiansession

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/familyweb"
)

func TestVerifiedSessionToPersistentFamilyWeb(t *testing.T) {
	path := filepath.Join(t.TempDir(), "family.json")
	store, err := familystore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Bootstrap(
		familydashboard.Child{ID: "child-a", TenantID: "tenant-a", DisplayName: "Alex"},
		authz.GuardianGrant{GuardianID: "guardian-a", ChildID: "child-a", TenantID: "tenant-a", Active: true, CanManageDevices: true, CanApproveContacts: true},
	); err != nil {
		t.Fatal(err)
	}
	mgr, err := New(time.Now, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := familyweb.New(mgr, store)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request returned %d", w.Code)
	}

	// Simulate a trusted guardian identity provider calling IssueVerified.
	loginResponse := httptest.NewRecorder()
	if err := mgr.IssueVerified(loginResponse, "guardian-a", "tenant-a"); err != nil {
		t.Fatal(err)
	}
	cookie := loginResponse.Result().Cookies()[0]
	r := httptest.NewRequest(http.MethodGet, "/family", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Alex") {
		t.Fatalf("trusted session did not load family profile: %d", w.Code)
	}
	identity, err := mgr.Authenticate(r)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"child_id": {"child-a"}, "name": {"Nina"}, "csrf_token": {identity.CSRFToken}}
	r = httptest.NewRequest(http.MethodPost, "/family/rename", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("authenticated mutation failed: %d", w.Code)
	}
	reloaded, err := familystore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cards := reloaded.Cards("guardian-a", "tenant-a")
	if len(cards) != 1 || cards[0].DisplayName != "Nina" {
		t.Fatalf("rename not durable: %+v", cards)
	}
	mgr.Revoke(r)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session still authorized: %d", w.Code)
	}
}
