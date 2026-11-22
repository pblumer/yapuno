package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
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
	handler, err := newHandler("https://app.example.org", families, guardians, &fakeMailer{}, sessions, optionalStores{pairing: devices})
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
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Geräte von Alex") || !strings.Contains(w.Body.String(), "app.example.org/pair") {
		t.Fatalf("guardian device page: %d", w.Code)
	}
	form := url.Values{"csrf_token": {"forged"}, "child_id": {"child-a"}, "code": {"123456"}}
	request := httptest.NewRequest(http.MethodPost, "https://app.example.org/family/devices/connect", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusForbidden {
		t.Fatalf("CSRF accepted: %d", w.Code)
	}
	for _, pairPath := range []string{"/pair", "/pair/app.js"} {
		anonymous := httptest.NewRequest(http.MethodGet, "https://app.example.org"+pairPath, nil)
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, anonymous)
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("child pairing route %s missing or cacheable: %d", pairPath, w.Code)
		}
	}

	// The whole pairing through the real routes: the device asks for a code
	// without any session, the guardian enters it, the device finishes and
	// signs in as that child.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	api := func(path string, payload map[string]string) (int, map[string]any) {
		body, _ := json.Marshal(payload)
		r := httptest.NewRequest(http.MethodPost, "https://app.example.org"+path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		// What an iPhone sends from these pages.
		r.Header.Set("Origin", "null")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	status, asked := api("/pair/request", map[string]string{"name": "iPhone", "public_key": base64.RawURLEncoding.EncodeToString(der)})
	if status != http.StatusOK {
		t.Fatalf("device request: %d", status)
	}
	token, code := asked["token"].(string), asked["code"].(string)
	if status, _ := api("/pair/nonce", map[string]string{"token": token}); status != http.StatusAccepted {
		t.Fatalf("device before approval: %d", status)
	}
	page := httptest.NewRequest(http.MethodGet, pathURL, nil)
	page.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, page)
	csrf := csrfField.FindStringSubmatch(w.Body.String())
	if csrf == nil {
		t.Fatal("no CSRF token on the device page")
	}
	form = url.Values{"csrf_token": {csrf[1]}, "child_id": {"child-a"}, "code": {code}}
	request = httptest.NewRequest(http.MethodPost, "https://app.example.org/family/devices/connect", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("guardian enters the code: %d %s", w.Code, w.Body)
	}
	status, ready := api("/pair/nonce", map[string]string{"token": token})
	if status != http.StatusOK {
		t.Fatalf("device after approval: %d", status)
	}
	proof := base64.RawURLEncoding.EncodeToString(pairingtest.Sign(t, key, "yapuno-pair-finish-v1\x00"+ready["nonce"].(string)))
	status, done := api("/pair/finish", map[string]string{"token": token, "signature": proof})
	if status != http.StatusOK {
		t.Fatalf("device finish: %d", status)
	}
	child := childLogin(t, handler, done["device_id"].(string), key)
	home := httptest.NewRequest(http.MethodGet, "https://app.example.org/child", nil)
	home.AddCookie(child)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, home)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Hoi Alex!") {
		t.Fatalf("paired device does not sign in as the child: %d", w.Code)
	}
	// Nothing of the old flow is served any more.
	for _, gone := range []string{"/pair/scan", "/family/devices/start", "/family/devices/status", "/family/devices/approve", "/family/devices/qr-render.js"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			r := httptest.NewRequest(method, "https://app.example.org"+gone, strings.NewReader("csrf_token="+csrf[1]+"&child_id=child-a"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.AddCookie(cookie)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, r)
			if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s still served: %d", method, gone, rec.Code)
			}
		}
	}
}
