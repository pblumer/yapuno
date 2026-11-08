package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
	"github.com/pblumer/yapuno/internal/guardiansession"
)

func TestOptionalGuardianDeviceRoutes(t *testing.T) {
	root := t.TempDir()
	families, err := familystore.Open(filepath.Join(root, "family.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := families.Bootstrap(
		familydashboard.Child{ID: "child-a", TenantID: "tenant-a", DisplayName: "Alex"},
		authz.GuardianGrant{GuardianID: "guardian-a", ChildID: "child-a", TenantID: "tenant-a", Active: true, CanManageDevices: true, CanApproveContacts: true},
	); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "guardians.json")
	if err := os.WriteFile(path, []byte(`{"guardians":[{"email":"parent@example.org","guardian_id":"guardian-a","tenant_id":"tenant-a","active":true}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	guardians, err := guardiandirectory.New(path, families)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := guardiansession.New(time.Now, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	devices, err := devicepairing.Open(filepath.Join(root, "pairing.json"), families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newHandler("https://app.example.org", families, guardians, &fakeMailer{}, sessions, devices)
	if err != nil {
		t.Fatal(err)
	}
	pathURL := "https://app.example.org/family/devices?child_id=child-a"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, pathURL, nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated device access: %d", w.Code)
	}
	issued := httptest.NewRecorder()
	if err := sessions.IssueVerified(issued, "guardian-a", "tenant-a"); err != nil {
		t.Fatal(err)
	}
	cookie := issued.Result().Cookies()[0]
	view := httptest.NewRequest(http.MethodGet, pathURL, nil)
	view.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, view)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Geräte verwalten") {
		t.Fatalf("guardian device page: %d", w.Code)
	}
	form := url.Values{"csrf_token": {"forged"}, "child_id": {"child-a"}}
	request := httptest.NewRequest(http.MethodPost, "https://app.example.org/family/devices/start", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusForbidden {
		t.Fatalf("CSRF accepted: %d", w.Code)
	}
}
