package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
	"github.com/pblumer/yapuno/internal/guardiansession"
)

type fakeMailer struct{ link string }

func (m *fakeMailer) SendSignInLink(_ context.Context, _, link string) error {
	m.link = link
	return nil
}

func TestBrowserLoginAndFamilyWithLiveRevocation(t *testing.T) {
	root := t.TempDir()
	store, err := familystore.Open(filepath.Join(root, "families.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Bootstrap(familydashboard.Child{ID: "c", TenantID: "t", DisplayName: "Alex"}, authz.GuardianGrant{GuardianID: "g", ChildID: "c", TenantID: "t", Active: true, CanManageDevices: true, CanApproveContacts: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "guardian.json")
	if err := os.WriteFile(path, []byte(`{"guardians":[{"email":"parent@example.org","guardian_id":"g","tenant_id":"t","active":true}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := guardiandirectory.New(path, store)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := guardiansession.New(time.Now, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	mailer := &fakeMailer{}
	handler, err := newHandler("https://app.example.org", store, dir, mailer, sessions, optionalStores{})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"email": {"parent@example.org"}}
	r := httptest.NewRequest(http.MethodPost, "https://app.example.org/login/request", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK || mailer.link == "" {
		t.Fatalf("login request: %d", w.Code)
	}
	uri, err := url.Parse(mailer.link)
	if err != nil {
		t.Fatal(err)
	}
	token := uri.Query().Get("token")
	form = url.Values{"token": {token}}
	r = httptest.NewRequest(http.MethodPost, "https://app.example.org/login/verify", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("email login: %d", w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure {
		t.Fatalf("missing secure session: %+v", cookies)
	}
	request := httptest.NewRequest(http.MethodGet, "https://app.example.org/family", nil)
	request.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Alex") {
		t.Fatalf("family access %d", w.Code)
	}
	// Operator revocation takes effect on the next HTTP request.
	if err := os.WriteFile(path, []byte(`{"guardians":[{"email":"parent@example.org","guardian_id":"g","tenant_id":"t","active":false}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session accepted: %d", w.Code)
	}
	// Guard against a forwarded or malicious Host header.
	badHost := httptest.NewRequest(http.MethodGet, "https://attacker.example.org/login", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, badHost)
	if w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("bad host: %d", w.Code)
	}
}
