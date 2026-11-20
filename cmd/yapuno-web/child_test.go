package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
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
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
	"github.com/pblumer/yapuno/internal/guardiansession"
)

func TestChildPagesNeedThePairingStore(t *testing.T) {
	s := newContactServer(t, true)
	if w := get(s.handler, "/child", nil); w.Code != http.StatusNotFound {
		t.Fatalf("child pages served without pairing store: %d", w.Code)
	}
}

func TestChildDeviceLoginEndToEndUntilGuardianRevokes(t *testing.T) {
	root := t.TempDir()
	families, err := familystore.Open(filepath.Join(root, "families.json"))
	if err != nil {
		t.Fatal(err)
	}
	grant := authz.GuardianGrant{GuardianID: "guardian-a", ChildID: "child-a", TenantID: "tenant-a", Active: true, CanManageDevices: true, CanApproveContacts: true}
	if err := families.Bootstrap(familydashboard.Child{ID: "child-a", TenantID: "tenant-a", DisplayName: "Alex"}, grant); err != nil {
		t.Fatal(err)
	}
	guardiansPath := filepath.Join(root, "guardians.json")
	if err := os.WriteFile(guardiansPath, []byte(`{"guardians":[{"email":"a@example.org","guardian_id":"guardian-a","tenant_id":"tenant-a","active":true}]}`), 0600); err != nil {
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
	pairing, err := devicepairing.Open(filepath.Join(root, "pairing.json"), families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newHandler("https://app.example.org", families, guardians, &fakeMailer{}, sessions, optionalStores{pairing: pairing})
	if err != nil {
		t.Fatal(err)
	}
	api := func(path string, payload map[string]string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(payload)
		r := httptest.NewRequest(http.MethodPost, "https://app.example.org"+path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://app.example.org")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	shell := get(handler, "/child", nil)
	if shell.Code != http.StatusOK || strings.Contains(shell.Body.String(), "Alex") || !strings.Contains(shell.Body.String(), "/child/app.js") {
		t.Fatalf("signed-out child page: %d", shell.Code)
	}
	deviceID, key := pairingtest.Pair(t, pairing, "guardian-a", "tenant-a", "child-a")
	var challenge struct {
		Nonce string `json:"nonce"`
	}
	if w := api("/child/session/challenge", map[string]string{"device_id": deviceID}); w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &challenge) != nil {
		t.Fatalf("challenge: %d", w.Code)
	}
	proof := base64.RawURLEncoding.EncodeToString(pairingtest.LoginProof(t, key, deviceID, challenge.Nonce))
	login := api("/child/session/login", map[string]string{"device_id": deviceID, "nonce": challenge.Nonce, "signature": proof})
	if login.Code != http.StatusNoContent || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login: %d", login.Code)
	}
	childCookie := login.Result().Cookies()[0]
	if !strings.Contains(get(handler, "/child", childCookie).Body.String(), "Hoi Alex!") {
		t.Fatal("authenticated child does not see the home page")
	}
	// A child session is not a guardian session, and vice versa.
	if w := get(handler, "/family", childCookie); w.Code != http.StatusUnauthorized {
		t.Fatalf("child session opened the guardian area: %d", w.Code)
	}
	issued := httptest.NewRecorder()
	if err := sessions.IssueVerified(issued, "guardian-a", "tenant-a"); err != nil {
		t.Fatal(err)
	}
	guardianCookie := issued.Result().Cookies()[0]
	if strings.Contains(get(handler, "/child", guardianCookie).Body.String(), "Hoi Alex!") {
		t.Fatal("guardian session was accepted as a child session")
	}
	// The guardian revokes the device through the real device page.
	devicePage := get(handler, "/family/devices?child_id=child-a", guardianCookie).Body.String()
	csrf := csrfField.FindStringSubmatch(devicePage)
	if csrf == nil {
		t.Fatal("no CSRF token on device page")
	}
	form := url.Values{"csrf_token": {csrf[1]}, "child_id": {"child-a"}, "device_id": {deviceID}, "confirm": {"yes"}}
	r := httptest.NewRequest(http.MethodPost, "https://app.example.org/family/devices/revoke", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(guardianCookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("revoke: %d", w.Code)
	}
	if strings.Contains(get(handler, "/child", childCookie).Body.String(), "Alex") {
		t.Fatal("revoked device still sees the child's home page")
	}
}
