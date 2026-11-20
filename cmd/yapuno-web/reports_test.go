package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
	"github.com/pblumer/yapuno/internal/guardiansession"
	"github.com/pblumer/yapuno/internal/messages"
	"github.com/pblumer/yapuno/internal/reports"
)

var (
	seqChoice   = regexp.MustCompile(`type="radio" name="seq" value="(\d+)" required><span><span class="yp-quote">([^<]*)</span>`)
	reportField = regexp.MustCompile(`name="report_id" value="([0-9a-f]{32})"`)
)

func TestReportAndBlockEndToEnd(t *testing.T) {
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
	logPath, reportPath := filepath.Join(root, "messages.log"), filepath.Join(root, "reports.json")
	messageStore, err := messages.Open(logPath, contactStore, pairing, families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer messageStore.Close()
	reportStore, err := reports.Open(reportPath, messageStore, families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	// Reports copy messages, so they are a configuration error without messaging.
	if _, err := newHandler("https://app.example.org", families, guardians, &fakeMailer{}, sessions, optionalStores{pairing: pairing, contacts: contactStore, reports: reportStore}); err == nil {
		t.Fatal("handler built with reports but without messaging")
	}
	if _, err := newHandler("https://app.example.org", families, guardians, &fakeMailer{}, sessions, optionalStores{reports: reportStore}); err == nil {
		t.Fatal("handler built with reports alone")
	}
	handler, err := newHandler("https://app.example.org", families, guardians, &fakeMailer{}, sessions, optionalStores{pairing: pairing, contacts: contactStore, messages: messageStore, reports: reportStore})
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
	page := func(path string, cookie *http.Cookie) string {
		return do(http.MethodGet, path, cookie, "", nil, nil).Body.String()
	}
	post := func(path string, cookie *http.Cookie, form url.Values) *httptest.ResponseRecorder {
		return do(http.MethodPost, path, cookie, "application/x-www-form-urlencoded", []byte(form.Encode()), nil)
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
	csrfOn := func(path string, cookie *http.Cookie) string {
		m := csrfField.FindStringSubmatch(page(path, cookie))
		if m == nil {
			t.Fatalf("no CSRF token on %s", path)
		}
		return m[1]
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
	alex, bea := signIn("guardian-a", "tenant-a", "child-a"), signIn("guardian-b", "tenant-b", "child-b")
	ga, gb := guardianCookie("guardian-a", "tenant-a"), guardianCookie("guardian-b", "tenant-b")
	n := 0
	send := func(cookie *http.Cookie, body string) int {
		n++
		return do(http.MethodPost, "/child/chat/"+chat+"/messages", cookie, "application/json",
			jsonBody(map[string]string{"client_id": fmt.Sprintf("report-e2e-client-%04d", n), "body": body}),
			map[string]string{"X-CSRF-Token": csrfOn("/child", cookie)}).Code
	}
	history := func(cookie *http.Cookie) *httptest.ResponseRecorder {
		return do(http.MethodGet, "/child/chat/"+chat+"/messages", cookie, "", nil, nil)
	}
	for _, m := range []struct {
		from *http.Cookie
		text string
	}{{alex, "Privat von Alex"}, {bea, "Das war gemein"}, {bea, "Auch privat"}, {bea, "Zweite gemeine Nachricht"}} {
		if code := send(m.from, m.text); code != http.StatusCreated {
			t.Fatalf("send %q: %d", m.text, code)
		}
	}
	safetyPath := "/child/chat/" + chat + "/safety"
	choose := func(text string) string {
		for _, m := range seqChoice.FindAllStringSubmatch(page(safetyPath, alex), -1) {
			if m[2] == text {
				return m[1]
			}
		}
		t.Fatalf("message %q is not offered for reporting", text)
		return ""
	}
	if strings.Contains(page("/family", ga), "Meldung") {
		t.Fatal("report hint before any report")
	}
	// Alex reports two messages through the real page.
	for _, text := range []string{"Das war gemein", "Zweite gemeine Nachricht"} {
		w := post("/child/chat/"+chat+"/report", alex, url.Values{"csrf_token": {csrfOn("/child", alex)}, "seq": {choose(text)}})
		if w.Code != http.StatusSeeOther {
			t.Fatalf("report %q: %d", text, w.Code)
		}
	}
	// Alex's guardian sees exactly the two reported messages; nobody else sees anything.
	if !strings.Contains(page("/family", ga), "2 offene Meldungen") {
		t.Fatal("family overview does not point to the reports")
	}
	contactsA := page("/family/contacts?child_id=child-a", ga)
	if !strings.Contains(contactsA, "Das war gemein") || !strings.Contains(contactsA, "Zweite gemeine Nachricht") || !strings.Contains(contactsA, "Nachricht von Bea") {
		t.Fatal("guardian does not see the reported messages")
	}
	if strings.Contains(contactsA, "Privat von Alex") || strings.Contains(contactsA, "Auch privat") {
		t.Fatal("guardian sees messages that were not reported")
	}
	if p := page("/family/contacts?child_id=child-b", gb); strings.Contains(p, "gemein") || strings.Contains(p, "Meldung") || strings.Contains(page("/family", gb), "Meldung") {
		t.Fatal("the reported child's family learns of the report")
	}
	if w := do(http.MethodGet, "/family/contacts?child_id=child-a", gb, "", nil, nil); w.Code != http.StatusForbidden {
		t.Fatalf("other family opened the page with the reports: %d", w.Code)
	}
	for _, guardian := range []*http.Cookie{ga, gb} {
		if w := history(guardian); w.Code != http.StatusUnauthorized {
			t.Fatalf("a report gave a guardian access to the chat: %d", w.Code)
		}
	}
	// Alex blocks Bea.
	if w := post("/child/chat/"+chat+"/block", alex, url.Values{"csrf_token": {csrfOn("/child", alex)}, "confirm": {"yes"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("block: %d", w.Code)
	}
	for name, child := range map[string]*http.Cookie{"blocker": alex, "blocked": bea} {
		if history(child).Code != http.StatusForbidden || send(child, "trotzdem") != http.StatusForbidden {
			t.Fatalf("%s can still read or send", name)
		}
	}
	if p := page("/child", bea); strings.Contains(p, "Alex") || strings.Contains(strings.ToLower(p), "blockiert") {
		t.Fatal("blocked child's start page reveals the block")
	}
	if p := page("/child", alex); !strings.Contains(p, "Blockiert") || !strings.Contains(p, "Bea") {
		t.Fatal("blocking child does not see whom they blocked")
	}
	// The other family cannot lift it; Alex's guardian can.
	unblock := func(cookie *http.Cookie, child string) int {
		return post("/family/contacts/unblock", cookie, url.Values{"csrf_token": {csrfOn("/family/contacts?child_id="+child, cookie)}, "child_id": {child}, "link_id": {chat}, "confirm": {"yes"}}).Code
	}
	if p := page("/family/contacts?child_id=child-b", gb); !strings.Contains(p, "Pausierte Kontakte") || strings.Contains(p, "/family/contacts/unblock") {
		t.Fatal("other family's contact page is wrong")
	}
	if code := unblock(gb, "child-b"); code < 400 || history(bea).Code != http.StatusForbidden {
		t.Fatalf("other family lifted the block: %d", code)
	}
	if code := unblock(ga, "child-a"); code != http.StatusSeeOther {
		t.Fatalf("unblock: %d", code)
	}
	if w := history(bea); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Das war gemein") {
		t.Fatalf("conversation not restored after the block was lifted: %d", w.Code)
	}
	// The guardian dismisses one report: its copy leaves the disk.
	ids := reportField.FindAllStringSubmatch(page("/family/contacts?child_id=child-a", ga), -1)
	if len(ids) != 2 {
		t.Fatalf("report forms: %d", len(ids))
	}
	dismiss := url.Values{"csrf_token": {csrfOn("/family/contacts?child_id=child-a", ga)}, "child_id": {"child-a"}, "report_id": {ids[0][1]}, "confirm": {"yes"}}
	if w := post("/family/contacts/report-done", gb, url.Values{"csrf_token": {csrfOn("/family/contacts?child_id=child-b", gb)}, "child_id": {"child-b"}, "report_id": {ids[0][1]}, "confirm": {"yes"}}); w.Code < 400 {
		t.Fatalf("other family dismissed the report: %d", w.Code)
	}
	if w := post("/family/contacts/report-done", ga, dismiss); w.Code != http.StatusSeeOther {
		t.Fatalf("dismiss: %d", w.Code)
	}
	raw, _ := os.ReadFile(reportPath)
	if strings.Contains(string(raw), "Zweite gemeine Nachricht") || !strings.Contains(string(raw), "Das war gemein") {
		t.Fatalf("report file after dismissal: newest report must be gone, the other kept")
	}
	if strings.Contains(string(raw), "Privat von Alex") || strings.Contains(string(raw), "Auch privat") {
		t.Fatal("report file holds messages that were never reported")
	}
	// The other family ends the contact: the chat is purged, the remaining report stays with Alex's guardian.
	remove := url.Values{"csrf_token": {csrfOn("/family/contacts?child_id=child-b", gb)}, "child_id": {"child-b"}, "link_id": {chat}, "confirm": {"yes"}}
	if w := post("/family/contacts/remove", gb, remove); w.Code != http.StatusSeeOther {
		t.Fatalf("remove: %d", w.Code)
	}
	if log, _ := os.ReadFile(logPath); strings.Contains(string(log), "gemein") {
		t.Fatal("messages of the ended contact are still in the message log")
	}
	if p := page("/family/contacts?child_id=child-a", ga); !strings.Contains(p, "Das war gemein") || !strings.Contains(page("/family", ga), "1 offene Meldung") {
		t.Fatal("the report did not outlive the contact")
	}
}

func TestSafetyPageNeedsMessaging(t *testing.T) {
	s := newContactServer(t, true)
	if w := get(s.handler, "/child/chat/"+strings.Repeat("0", 32)+"/safety", nil); w.Code != http.StatusNotFound {
		t.Fatalf("safety page without messaging: %d", w.Code)
	}
}
