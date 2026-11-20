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
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
	"github.com/pblumer/yapuno/internal/guardiansession"
	"github.com/pblumer/yapuno/internal/messages"
)

func TestFirstHelloEndToEndAndPurgeOnContactRemoval(t *testing.T) {
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
	logPath := filepath.Join(root, "messages.log")
	messageStore, err := messages.Open(logPath, contactStore, pairing, families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer messageStore.Close()
	// Messaging without contacts or pairing is a configuration error.
	for name, opt := range map[string]optionalStores{
		"no contacts": {pairing: pairing, messages: messageStore},
		"no pairing":  {contacts: contactStore, messages: messageStore},
	} {
		if _, err := newHandler("https://app.example.org", families, guardians, &fakeMailer{}, sessions, opt); err == nil {
			t.Fatalf("%s: handler built", name)
		}
	}
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
	signIn := func(guardian, tenant, child string) *http.Cookie {
		deviceID, key := pairingtest.Pair(t, pairing, guardian, tenant, child)
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
			t.Fatalf("login: %d", w.Code)
		}
		return w.Result().Cookies()[0]
	}
	guardianCookie := func(guardian, tenant string) *http.Cookie {
		w := httptest.NewRecorder()
		if err := sessions.IssueVerified(w, guardian, tenant); err != nil {
			t.Fatal(err)
		}
		return w.Result().Cookies()[0]
	}
	// Both families approve the contact through the stores used by the pages.
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
	alex, bea := signIn("guardian-a", "tenant-a", "child-a"), signIn("guardian-b", "tenant-b", "child-b")
	csrfOf := func(cookie *http.Cookie) string {
		m := csrfField.FindStringSubmatch(do(http.MethodGet, "/child/chat/"+chat, cookie, "", nil, nil).Body.String())
		if m == nil {
			t.Fatal("no CSRF token on chat page")
		}
		return m[1]
	}
	send := func(cookie *http.Cookie, id, body string) int {
		return do(http.MethodPost, "/child/chat/"+chat+"/messages", cookie, "application/json", jsonBody(map[string]string{"client_id": id, "body": body}), map[string]string{"X-CSRF-Token": csrfOf(cookie)}).Code
	}
	if code := send(alex, "first-hello-client-0001", "Hoi Bea, erste Nachricht!"); code != http.StatusCreated {
		t.Fatalf("first hello: %d", code)
	}
	if code := send(bea, "first-hello-client-0002", "Hoi Alex!"); code != http.StatusCreated {
		t.Fatalf("reply: %d", code)
	}
	page := do(http.MethodGet, "/child/chat/"+chat, bea, "", nil, nil).Body.String()
	if !strings.Contains(page, "Hoi Bea, erste Nachricht!") || !strings.Contains(page, "<h1>Alex</h1>") {
		t.Fatal("Bea does not see Alex's message")
	}
	// Guardians have no way to read messages: their session is not accepted.
	for _, g := range []*http.Cookie{guardianCookie("guardian-a", "tenant-a"), guardianCookie("guardian-b", "tenant-b")} {
		if w := do(http.MethodGet, "/child/chat/"+chat+"/messages", g, "", nil, nil); w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), "Hoi") {
			t.Fatalf("guardian session read messages: %d", w.Code)
		}
		if w := do(http.MethodGet, "/child/chat/"+chat, g, "", nil, nil); w.Code != http.StatusSeeOther {
			t.Fatalf("guardian session opened chat page: %d", w.Code)
		}
	}
	raw, _ := os.ReadFile(logPath)
	if !strings.Contains(string(raw), "erste Nachricht") {
		t.Fatal("precondition: message should be on disk")
	}
	// Guardian B ends the contact on the contact page.
	gb := guardianCookie("guardian-b", "tenant-b")
	contactPage := do(http.MethodGet, "/family/contacts?child_id=child-b", gb, "", nil, nil).Body.String()
	csrf := csrfField.FindStringSubmatch(contactPage)
	if csrf == nil {
		t.Fatal("no CSRF token on contact page")
	}
	form := url.Values{"csrf_token": {csrf[1]}, "child_id": {"child-b"}, "link_id": {chat}, "confirm": {"yes"}}
	if w := do(http.MethodPost, "/family/contacts/remove", gb, "application/x-www-form-urlencoded", []byte(form.Encode()), nil); w.Code != http.StatusSeeOther {
		t.Fatalf("remove contact: %d", w.Code)
	}
	for _, child := range []*http.Cookie{alex, bea} {
		if w := do(http.MethodGet, "/child/chat/"+chat+"/messages", child, "", nil, nil); w.Code != http.StatusForbidden {
			t.Fatalf("history after removal: %d", w.Code)
		}
	}
	raw, _ = os.ReadFile(logPath)
	if strings.Contains(string(raw), "erste Nachricht") || strings.Contains(string(raw), "Hoi Alex!") {
		t.Fatal("messages of the ended contact are still on disk")
	}
}
