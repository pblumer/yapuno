package main

import (
	"bytes"
	"crypto/ecdsa"
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
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
	"github.com/pblumer/yapuno/internal/guardiansession"
	"github.com/pblumer/yapuno/internal/messages"
)

func TestLostDevicePageNeedsThePairingStore(t *testing.T) {
	s := newContactServer(t, true)
	if w := get(s.handler, "/family/devices/lost?child_id=child-a", s.cookieA); w.Code != http.StatusNotFound {
		t.Fatalf("lost-device page served without pairing store: %d", w.Code)
	}
	if strings.Contains(get(s.handler, "/family", s.cookieA).Body.String(), "/family/devices/lost") {
		t.Fatal("family page links to an unavailable lost-device page")
	}
}

func TestLostDeviceEndToEndLocksEverySessionOfThatChild(t *testing.T) {
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
	pairing, err := devicepairing.Open(filepath.Join(root, "pairing.json"), families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	contactStore, err := contacts.Open(filepath.Join(root, "contacts.json"), families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	messageStore, err := messages.Open(filepath.Join(root, "messages.log"), contactStore, pairing, families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer messageStore.Close()
	handler, err := newHandler("https://app.example.org", families, guardians, &fakeMailer{}, sessions, optionalStores{pairing: pairing, contacts: contactStore, messages: messageStore})
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path string, cookie *http.Cookie, contentType string, body []byte, header map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://app.example.org"+path, bytes.NewReader(body))
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		r.Header.Set("Origin", "https://app.example.org")
		for k, v := range header {
			r.Header.Set(k, v)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	jsonBody := func(v any) []byte { b, _ := json.Marshal(v); return b }
	// login runs the real key login and returns the HTTP status and cookie.
	login := func(deviceID string, key *ecdsa.PrivateKey) (int, *http.Cookie) {
		var challenge struct {
			Nonce string `json:"nonce"`
		}
		w := do(http.MethodPost, "/child/session/challenge", nil, "application/json", jsonBody(map[string]string{"device_id": deviceID}), nil)
		if json.Unmarshal(w.Body.Bytes(), &challenge) != nil || challenge.Nonce == "" {
			t.Fatalf("challenge: %d", w.Code)
		}
		proof := base64.RawURLEncoding.EncodeToString(pairingtest.LoginProof(t, key, deviceID, challenge.Nonce))
		w = do(http.MethodPost, "/child/session/login", nil, "application/json", jsonBody(map[string]string{"device_id": deviceID, "nonce": challenge.Nonce, "signature": proof}), nil)
		if w.Code != http.StatusNoContent {
			return w.Code, nil
		}
		return w.Code, w.Result().Cookies()[0]
	}
	type device struct {
		id     string
		key    *ecdsa.PrivateKey
		cookie *http.Cookie
	}
	paired := func(guardian, tenant, child string) device {
		id, key := pairingtest.Pair(t, pairing, guardian, tenant, child)
		code, cookie := login(id, key)
		if code != http.StatusNoContent {
			t.Fatalf("login of a freshly paired device: %d", code)
		}
		return device{id: id, key: key, cookie: cookie}
	}
	guardianCookie := func(guardian, tenant string) *http.Cookie {
		w := httptest.NewRecorder()
		if err := sessions.IssueVerified(w, guardian, tenant); err != nil {
			t.Fatal(err)
		}
		return w.Result().Cookies()[0]
	}
	code, err := contactStore.Invite("guardian-a", "tenant-a", "child-a")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := contactStore.Redeem("guardian-b", "tenant-b", "child-b", code)
	if err != nil {
		t.Fatal(err)
	}
	if err := contactStore.Confirm("guardian-a", "tenant-a", "child-a", chat); err != nil {
		t.Fatal(err)
	}
	lostPhone, tablet, beaPhone := paired("guardian-a", "tenant-a", "child-a"), paired("guardian-a", "tenant-a", "child-a"), paired("guardian-b", "tenant-b", "child-b")
	history := func(cookie *http.Cookie) *httptest.ResponseRecorder {
		return do(http.MethodGet, "/child/chat/"+chat+"/messages", cookie, "", nil, nil)
	}
	chatCSRF := csrfField.FindStringSubmatch(do(http.MethodGet, "/child/chat/"+chat, lostPhone.cookie, "", nil, nil).Body.String())
	if chatCSRF == nil {
		t.Fatal("no CSRF token on chat page")
	}
	send := func(cookie *http.Cookie, id, body string) int {
		return do(http.MethodPost, "/child/chat/"+chat+"/messages", cookie, "application/json", jsonBody(map[string]string{"client_id": id, "body": body}), map[string]string{"X-CSRF-Token": chatCSRF[1]}).Code
	}
	if code := send(lostPhone.cookie, "lost-device-client-0001", "Bin im Training"); code != http.StatusCreated {
		t.Fatalf("message before the loss: %d", code)
	}
	lock := func(cookie *http.Cookie, child string, extra url.Values) int {
		page := do(http.MethodGet, "/family/devices/lost?child_id="+child, cookie, "", nil, nil).Body.String()
		form := url.Values{"child_id": {child}, "confirm": {"yes"}, "csrf_token": {"csrf-token-not-shown"}}
		if m := csrfField.FindStringSubmatch(page); m != nil {
			form.Set("csrf_token", m[1])
		}
		for k, v := range extra {
			form[k] = v
		}
		return do(http.MethodPost, "/family/devices/lost", cookie, "application/x-www-form-urlencoded", []byte(form.Encode()), nil).Code
	}
	ga, gb := guardianCookie("guardian-a", "tenant-a"), guardianCookie("guardian-b", "tenant-b")
	// The other family, a child session and a signed-out visitor cannot lock Alex's devices.
	if code := lock(gb, "child-a", url.Values{"scope": {"all"}}); code != http.StatusForbidden {
		t.Fatalf("other family locked devices: %d", code)
	}
	if w := do(http.MethodGet, "/family/devices/lost?child_id=child-a", lostPhone.cookie, "", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("child session opened the lost-device page: %d", w.Code)
	}
	if w := do(http.MethodGet, "/family/devices/lost?child_id=child-a", nil, "", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out visitor opened the lost-device page: %d", w.Code)
	}
	if history(lostPhone.cookie).Code != http.StatusOK || history(tablet.cookie).Code != http.StatusOK {
		t.Fatal("a refused request ended Alex's sessions")
	}
	// Guardian A locks only the lost phone: the tablet keeps working.
	page := do(http.MethodGet, "/family/devices/lost?child_id=child-a", ga, "", nil, nil).Body.String()
	if !strings.Contains(page, lostPhone.id) || !strings.Contains(page, tablet.id) || strings.Contains(page, beaPhone.id) {
		t.Fatal("lost-device page must list exactly Alex's devices")
	}
	if code := lock(ga, "child-a", url.Values{"scope": {"one"}, "device_id": {lostPhone.id}}); code != http.StatusSeeOther {
		t.Fatalf("lock one: %d", code)
	}
	if w := history(lostPhone.cookie); w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), "Training") {
		t.Fatalf("locked phone still reads messages: %d", w.Code)
	}
	if code := send(lostPhone.cookie, "lost-device-client-0002", "vom Finder"); code != http.StatusUnauthorized {
		t.Fatalf("locked phone still sends: %d", code)
	}
	if w := do(http.MethodGet, "/child", lostPhone.cookie, "", nil, nil); strings.Contains(w.Body.String(), "Alex") {
		t.Fatal("locked phone still sees the home page")
	}
	if code, _ := login(lostPhone.id, lostPhone.key); code == http.StatusNoContent {
		t.Fatal("locked phone signed in again with its key")
	}
	if history(tablet.cookie).Code != http.StatusOK {
		t.Fatal("locking one device ended the other device's session")
	}
	// Not sure which one it was: lock everything of Alex.
	if code := lock(ga, "child-a", url.Values{"scope": {"all"}}); code != http.StatusSeeOther {
		t.Fatalf("lock all: %d", code)
	}
	if history(tablet.cookie).Code != http.StatusUnauthorized {
		t.Fatal("tablet still reads messages after locking all devices")
	}
	if code, _ := login(tablet.id, tablet.key); code == http.StatusNoContent {
		t.Fatal("locked tablet signed in again with its key")
	}
	// Bea is unaffected and still has the conversation.
	if w := history(beaPhone.cookie); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Bin im Training") || strings.Contains(w.Body.String(), "vom Finder") {
		t.Fatalf("other family's device affected: %d", w.Code)
	}
	// A newly paired device of Alex gets the contact and the history back.
	replacement := paired("guardian-a", "tenant-a", "child-a")
	if w := history(replacement.cookie); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Bin im Training") {
		t.Fatalf("replacement device has no history: %d", w.Code)
	}
}
