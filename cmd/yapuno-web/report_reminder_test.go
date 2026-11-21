package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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

// movable is a clock the test advances; it starts at the real time so that
// it agrees with the stores that use the real clock.
type movable struct {
	mu     sync.Mutex
	offset time.Duration
}

func (m *movable) now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return time.Now().Add(m.offset)
}

func (m *movable) advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.offset += d
}

func TestUnseenReportIsRemindedUntilAGuardianLooks(t *testing.T) {
	root := t.TempDir()
	clock := &movable{}
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
	reportPath := filepath.Join(root, "reports.json")
	reportStore, err := reports.Open(reportPath, messageStore, families, clock.now)
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
	stores := optionalStores{pairing: pairing, contacts: contactStore, messages: messageStore, reports: reportStore, now: clock.now}

	// Without a mailer that can send notices there is nothing to remind with.
	silent, err := newApp("https://app.example.org", families, guardians, &fakeMailer{}, sessions, stores)
	if err != nil || silent.reminder != nil {
		t.Fatalf("reminder without a notice mailer: %v", err)
	}

	mailer := &noticeMailer{}
	app, err := newApp("https://app.example.org", families, guardians, mailer, sessions, stores)
	if err != nil {
		t.Fatal(err)
	}
	if app.reminder == nil {
		t.Fatal("no reminder although reports and notices are configured")
	}
	alexID, alexKey := pairingtest.Pair(t, pairing, "guardian-a", "tenant-a", "child-a")
	beaID, beaKey := pairingtest.Pair(t, pairing, "guardian-b", "tenant-b", "child-b")
	alex, bea := childLogin(t, app.handler, alexID, alexKey), childLogin(t, app.handler, beaID, beaKey)
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
		app.handler.ServeHTTP(w, r)
		return w
	}
	csrfOf := func(cookie *http.Cookie) string {
		m := csrfField.FindStringSubmatch(do(http.MethodGet, "/child", cookie, "", nil, nil).Body.String())
		if m == nil {
			t.Fatal("no CSRF token on the child start page")
		}
		return m[1]
	}
	n := 0
	reportNew := func(text string) {
		t.Helper()
		n++
		payload, _ := json.Marshal(map[string]string{"client_id": fmt.Sprintf("remind-e2e-client-%04d", n), "body": text})
		w := do(http.MethodPost, "/child/chat/"+chat+"/messages", bea, "application/json", payload, map[string]string{"X-CSRF-Token": csrfOf(bea)})
		var out struct {
			Seq uint64 `json:"seq"`
		}
		if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("send: %d", w.Code)
		}
		form := url.Values{"csrf_token": {csrfOf(alex)}, "seq": {fmt.Sprint(out.Seq)}}
		if w := do(http.MethodPost, "/child/chat/"+chat+"/report", alex, "application/x-www-form-urlencoded", []byte(form.Encode()), nil); w.Code != http.StatusSeeOther {
			t.Fatalf("report: %d", w.Code)
		}
	}
	guardian := func(id string) *http.Cookie {
		w := httptest.NewRecorder()
		if err := sessions.IssueVerified(w, id, "tenant-a"); err != nil {
			t.Fatal(err)
		}
		return w.Result().Cookies()[0]
	}
	ctx := context.Background()

	reportNew("Niemand schaut hin")
	if got := mailer.reports(2); len(got) != 2 {
		t.Fatalf("first notice: %v", got)
	}
	if n := app.reminder.Run(ctx); n != 0 || len(mailer.reminders()) != 0 {
		t.Fatalf("reminder right after the report: %d", n)
	}
	clock.advance(3 * time.Hour)
	if n := app.reminder.Run(ctx); n != 1 {
		t.Fatalf("first reminder: %d", n)
	}
	got := mailer.reminders()
	if len(got) != 2 || got[0] != "anna@example.org about Alex" || got[1] != "ben@example.org about Alex" {
		t.Fatalf("first reminder recipients: %v", got)
	}
	// The overview with its count is not "seen": the second reminder still comes.
	if w := do(http.MethodGet, "/family", guardian("guardian-a"), "", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("overview: %d", w.Code)
	}
	clock.advance(21 * time.Hour)
	if n := app.reminder.Run(ctx); n != 1 || len(mailer.reminders()) != 4 {
		t.Fatalf("second reminder: %d %v", n, mailer.reminders())
	}
	clock.advance(48 * time.Hour)
	if n := app.reminder.Run(ctx); n != 0 {
		t.Fatalf("third reminder: %d", n)
	}

	// A new report; this time the second guardian opens the page that shows it.
	reportNew("Wird angesehen")
	if got := mailer.reports(4); len(got) != 4 {
		t.Fatalf("notice for the second report: %v", got)
	}
	page := do(http.MethodGet, "/family/contacts?child_id=child-a", guardian("guardian-a2"), "", nil, nil)
	if page.Code != http.StatusOK || !bytes.Contains(page.Body.Bytes(), []byte("Wird angesehen")) {
		t.Fatalf("contact page: %d", page.Code)
	}
	for _, step := range []time.Duration{3 * time.Hour, 21 * time.Hour, 24 * time.Hour} {
		clock.advance(step)
		if n := app.reminder.Run(ctx); n != 0 {
			t.Fatalf("reminder although a guardian has seen the report: %d", n)
		}
	}
	if len(mailer.reminders()) != 4 {
		t.Fatalf("reminders after the report was seen: %v", mailer.reminders())
	}
	// Seeing a report does not delete it, and the other family was never mailed.
	if reportStore.Count("guardian-a", "tenant-a", "child-a") == 0 {
		t.Fatal("seeing a report deleted it")
	}
	for _, line := range append(mailer.reports(0), mailer.reminders()...) {
		if bytes.Contains([]byte(line), []byte("bea-parent")) {
			t.Fatalf("the other family was mailed: %s", line)
		}
	}
}

type countingRunner struct{ runs atomic.Int32 }

func (c *countingRunner) Run(context.Context) int { c.runs.Add(1); return 0 }

func TestReminderTimerRunsRepeatedlyAndStopsOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := &countingRunner{}
	done := make(chan struct{})
	go func() { remindEvery(ctx, runner, 5*time.Millisecond); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for runner.runs.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if runner.runs.Load() < 3 {
		t.Fatalf("timer ran %d times", runner.runs.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timer did not stop on shutdown")
	}
	after := runner.runs.Load()
	time.Sleep(30 * time.Millisecond)
	if runner.runs.Load() != after {
		t.Fatal("timer kept running after shutdown")
	}
}
