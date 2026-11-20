package main

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	"github.com/pblumer/yapuno/internal/guardianlock"
	"github.com/pblumer/yapuno/internal/guardianlogin"
	"github.com/pblumer/yapuno/internal/guardiansession"
	"github.com/pblumer/yapuno/internal/messages"
	"github.com/pblumer/yapuno/internal/reports"
)

func TestGuardiansAreEmailedAboutAReport(t *testing.T) {
	root := t.TempDir()
	families, err := familystore.Open(filepath.Join(root, "families.json"))
	if err != nil {
		t.Fatal(err)
	}
	grant := func(guardian, child, tenant string) authz.GuardianGrant {
		return authz.GuardianGrant{GuardianID: guardian, ChildID: child, TenantID: tenant, Active: true, CanManageDevices: true, CanApproveContacts: true}
	}
	for _, c := range []struct{ id, tenant, guardian, name string }{{"child-a", "tenant-a", "guardian-a", "Alex"}, {"child-b", "tenant-b", "guardian-b", "Bea"}} {
		if err := families.Bootstrap(familydashboard.Child{ID: c.id, TenantID: c.tenant, DisplayName: c.name}, grant(c.guardian, c.id, c.tenant)); err != nil {
			t.Fatal(err)
		}
	}
	if err := families.BootstrapGuardian(grant("guardian-a2", "child-a", "tenant-a")); err != nil {
		t.Fatal(err)
	}
	guardiansPath := filepath.Join(root, "guardians.json")
	if err := os.WriteFile(guardiansPath, []byte(threeGuardians), 0600); err != nil {
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
	reportStore, err := reports.Open(filepath.Join(root, "reports.json"), messageStore, families, time.Now)
	if err != nil {
		t.Fatal(err)
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
	alexID, alexKey := pairingtest.Pair(t, pairing, "guardian-a", "tenant-a", "child-a")
	beaID, beaKey := pairingtest.Pair(t, pairing, "guardian-b", "tenant-b", "child-b")
	type server struct {
		handler   http.Handler
		alex, bea *http.Cookie
	}
	n := 0
	start := func(mailer guardianlogin.Sender, opt optionalStores) server {
		opt.pairing, opt.contacts, opt.messages, opt.reports = pairing, contactStore, messageStore, reportStore
		handler, err := newHandler("https://app.example.org", families, guardians, mailer, sessions, opt)
		if err != nil {
			t.Fatal(err)
		}
		s := server{handler: handler}
		s.alex, s.bea = childLogin(t, handler, alexID, alexKey), childLogin(t, handler, beaID, beaKey)
		return s
	}
	request := func(s server, method, path string, cookie *http.Cookie, contentType string, body []byte, header map[string]string) *httptest.ResponseRecorder {
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
		s.handler.ServeHTTP(w, r)
		return w
	}
	csrfOf := func(s server, cookie *http.Cookie) string {
		m := csrfField.FindStringSubmatch(request(s, http.MethodGet, "/child", cookie, "", nil, nil).Body.String())
		if m == nil {
			t.Fatal("no CSRF token on the child start page")
		}
		return m[1]
	}
	// say sends a message from Bea and returns its sequence number as text.
	say := func(s server, body string) string {
		n++
		payload, _ := json.Marshal(map[string]string{"client_id": fmt.Sprintf("notice-e2e-client-%04d", n), "body": body})
		w := request(s, http.MethodPost, "/child/chat/"+chat+"/messages", s.bea, "application/json", payload, map[string]string{"X-CSRF-Token": csrfOf(s, s.bea)})
		var out struct {
			Seq uint64 `json:"seq"`
		}
		if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("send: %d", w.Code)
		}
		return fmt.Sprint(out.Seq)
	}
	report := func(s server, seq string) int {
		return request(s, http.MethodPost, "/child/chat/"+chat+"/report", s.alex, "application/x-www-form-urlencoded",
			[]byte(url.Values{"csrf_token": {csrfOf(s, s.alex)}, "seq": {seq}}.Encode()), nil).Code
	}
	safety := func(s server) string {
		return request(s, http.MethodGet, "/child/chat/"+chat+"/safety", s.alex, "", nil, nil).Body.String()
	}

	// A mailer that cannot send notices: reporting works, and the child is not promised an e-mail.
	plain := start(&fakeMailer{}, optionalStores{})
	if strings.Contains(safety(plain), "E-Mail") {
		t.Fatal("child is promised an e-mail that cannot be sent")
	}
	if code := report(plain, say(plain, "ohne Mitteilung")); code != http.StatusSeeOther {
		t.Fatalf("report without notices: %d", code)
	}

	// With notices: both guardians of Alex are told, with Alex's name only.
	mailer := &noticeMailer{}
	s := start(mailer, optionalStores{})
	if !strings.Contains(safety(s), "Yapuno schreibt deinen Eltern eine E-Mail") {
		t.Fatal("child is not told that the guardians get an e-mail")
	}
	first := say(s, "Das war gemein und geheim")
	if code := report(s, first); code != http.StatusSeeOther {
		t.Fatalf("report: %d", code)
	}
	got := mailer.reports(2)
	if len(got) != 2 || got[0] != "anna@example.org about Alex" || got[1] != "ben@example.org about Alex" {
		t.Fatalf("report notices: %v", got)
	}
	// Reporting the same message again, or another one within the hour, sends nothing more.
	if code := report(s, first); code != http.StatusSeeOther {
		t.Fatalf("repeat: %d", code)
	}
	if code := report(s, say(s, "noch eine")); code != http.StatusSeeOther {
		t.Fatalf("second report: %d", code)
	}
	time.Sleep(150 * time.Millisecond)
	if got := mailer.reports(3); len(got) != 2 {
		t.Fatalf("more than one notice per hour and child: %v", got)
	}
	if len(mailer.notices) != 0 || mailer.link("anna@example.org") != "" {
		t.Fatal("a report triggered other mail")
	}

	// A guardian who locked their access gets no mail: that mailbox may be in the wrong hands.
	locks, err := guardianlock.Open(filepath.Join(root, "guardian-locks.json"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := locks.Lock("guardian-a2", "tenant-a"); err != nil {
		t.Fatal(err)
	}
	careful := &noticeMailer{}
	locked := start(careful, optionalStores{guardianLocks: locks})
	if code := report(locked, say(locked, "nach der Sperre")); code != http.StatusSeeOther {
		t.Fatalf("report with a locked guardian: %d", code)
	}
	if got := careful.reports(1); len(got) != 1 || got[0] != "anna@example.org about Alex" {
		t.Fatalf("notices with a locked guardian: %v", got)
	}
	time.Sleep(100 * time.Millisecond)
	if got := careful.reports(2); len(got) != 1 {
		t.Fatalf("locked guardian was mailed: %v", got)
	}
}

// childLogin signs a paired device in through the real challenge and login.
func childLogin(t *testing.T, handler http.Handler, deviceID string, key *ecdsa.PrivateKey) *http.Cookie {
	t.Helper()
	post := func(path string, payload map[string]string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(payload)
		r := httptest.NewRequest(http.MethodPost, "https://app.example.org"+path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://app.example.org")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	var challenge struct {
		Nonce string `json:"nonce"`
	}
	w := post("/child/session/challenge", map[string]string{"device_id": deviceID})
	if json.Unmarshal(w.Body.Bytes(), &challenge) != nil || challenge.Nonce == "" {
		t.Fatalf("challenge: %d", w.Code)
	}
	proof := base64.RawURLEncoding.EncodeToString(pairingtest.LoginProof(t, key, deviceID, challenge.Nonce))
	w = post("/child/session/login", map[string]string{"device_id": deviceID, "nonce": challenge.Nonce, "signature": proof})
	if w.Code != http.StatusNoContent {
		t.Fatalf("login: %d", w.Code)
	}
	return w.Result().Cookies()[0]
}
