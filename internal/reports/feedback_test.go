package reports

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestChildLearnsThatAGuardianHasSeenTheReport(t *testing.T) {
	w := newWorld(t)
	first := w.say(w.miaP, w.chat, "erste")
	second := w.say(w.miaP, w.chat, "zweite")
	if _, err := w.reports.File(w.leaP, w.chat, first); err != nil {
		t.Fatal(err)
	}
	if got := w.reports.Seen(lea, w.chat); len(got) != 0 {
		t.Fatalf("seen before any guardian looked: %v", got)
	}
	// Listing and counting are not seeing.
	if _, err := w.reports.ForChild("anna", lea.TenantID, lea.ChildID); err != nil {
		t.Fatal(err)
	}
	_ = w.reports.Count("anna", lea.TenantID, lea.ChildID)
	if len(w.reports.Seen(lea, w.chat)) != 0 {
		t.Fatal("listing counted as seeing")
	}
	if err := w.reports.MarkSeen("albert", lea.TenantID, lea.ChildID); err != nil {
		t.Fatal(err)
	}
	// A report filed after the guardian looked is not seen yet.
	if _, err := w.reports.File(w.leaP, w.chat, second); err != nil {
		t.Fatal(err)
	}
	got := w.reports.Seen(lea, w.chat)
	if len(got) != 1 || !got[first] || got[second] {
		t.Fatalf("seen marks: %v", got)
	}
	// Only the reporting child learns this, and only for this conversation.
	if len(w.reports.Seen(mia, w.chat)) != 0 || len(w.reports.Seen(tim, w.chat)) != 0 || len(w.reports.Seen(lea, strings.Repeat("0", 32))) != 0 {
		t.Fatal("seen marks leak to another child or conversation")
	}
	if len(w.reports.Seen(lea, "")) != 0 || len(w.reports.Seen(mia, "")) != 0 {
		t.Fatal("seen marks for an empty conversation")
	}
}

func TestDismissedReportLeavesAMarkerWithoutTextUntilRetentionEnds(t *testing.T) {
	w := newWorld(t)
	seq := w.say(w.miaP, w.chat, "Text der Nachricht")
	if _, err := w.reports.File(w.leaP, w.chat, seq); err != nil {
		t.Fatal(err)
	}
	w.clock.advance(time.Hour)
	list, _ := w.reports.ForChild("anna", lea.TenantID, lea.ChildID)
	id := list[0].ID
	// Dismissed without ever opening the page: dismissing is seeing.
	if err := w.reports.Dismiss("anna", lea.TenantID, lea.ChildID, id); err != nil {
		t.Fatal(err)
	}
	// Guardians no longer have a report.
	if list, err := w.reports.ForChild("albert", lea.TenantID, lea.ChildID); err != nil || len(list) != 0 || w.reports.Count("anna", lea.TenantID, lea.ChildID) != 0 {
		t.Fatalf("dismissed report still listed: %v %v", list, err)
	}
	if err := w.reports.Dismiss("anna", lea.TenantID, lea.ChildID, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dismissing twice: %v", err)
	}
	// The child still sees that it was reported and seen, and cannot report it again.
	if !w.reports.Reported(lea, w.chat)[seq] || !w.reports.Seen(lea, w.chat)[seq] {
		t.Fatal("marker lost after dismissal")
	}
	if created, err := w.reports.File(w.leaP, w.chat, seq); err != nil || created {
		t.Fatalf("dismissed message reported again: %v %v", created, err)
	}
	if w.reports.Count("anna", lea.TenantID, lea.ChildID) != 0 {
		t.Fatal("re-reporting a dismissed message reopened it")
	}
	// What stays on disk: which message and when, never the text or a name.
	raw, _ := os.ReadFile(w.path)
	for _, absent := range []string{"Text der Nachricht", "Mia", "anna", "albert"} {
		if strings.Contains(string(raw), absent) {
			t.Errorf("marker contains %q: %s", absent, raw)
		}
	}
	for _, want := range []string{`"dismissed_at":"2026-11-20T13:00:00Z"`, `"seen_at":"2026-11-20T13:00:00Z"`, `"seq":`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("marker misses %s: %s", want, raw)
		}
	}
	// Never reminded, across a restart, and gone when retention ends.
	reopened, err := Open(w.path, w.messages, w.families, w.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	w.clock.advance(24 * time.Hour)
	if len(reopened.RemindersDue()) != 0 || !reopened.Seen(lea, w.chat)[seq] {
		t.Fatal("marker reminded or lost after restart")
	}
	w.clock.advance(Retention)
	if reopened.Reported(lea, w.chat)[seq] || reopened.Seen(lea, w.chat)[seq] {
		t.Fatal("marker outlived the retention")
	}
	// The next write removes it from disk.
	next := w.say(w.miaP, w.chat, "neu")
	if _, err := reopened.File(w.leaP, w.chat, next); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(w.path); strings.Contains(string(raw), "dismissed_at") {
		t.Fatal("expired marker still on disk")
	}
}

func TestMarkersDoNotCountAsOpenReports(t *testing.T) {
	w := newWorld(t)
	var seqs []uint64
	for i := 0; i < MaxOpenPerChild; i++ {
		w.clock.advance(time.Minute)
		seq := w.say(w.miaP, w.chat, "Nachricht "+strings.Repeat("x", i+1))
		if _, err := w.reports.File(w.leaP, w.chat, seq); err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, seq)
	}
	list, _ := w.reports.ForChild("anna", lea.TenantID, lea.ChildID)
	for _, r := range list[:5] {
		if err := w.reports.Dismiss("anna", lea.TenantID, lea.ChildID, r.ID); err != nil {
			t.Fatal(err)
		}
	}
	// Five dismissed: exactly five new reports fit, the sixth does not.
	for i := 0; i < 6; i++ {
		w.clock.advance(time.Minute)
		seq := w.say(w.miaP, w.chat, "weitere "+strings.Repeat("y", i+1))
		created, err := w.reports.File(w.leaP, w.chat, seq)
		if i < 5 && (err != nil || !created) {
			t.Fatalf("report %d after making room: %v %v", i, created, err)
		}
		if i == 5 && !errors.Is(err, ErrLimit) {
			t.Fatalf("markers must not free more room than was dismissed: %v", err)
		}
	}
	if got := len(w.reports.Reported(lea, w.chat)); got != MaxOpenPerChild+5 {
		t.Fatalf("reported marks: %d", got)
	}
	// A marker cannot be marked seen again or dismissed through another child's page.
	if err := w.reports.MarkSeen("anna", lea.TenantID, lea.ChildID); err != nil {
		t.Fatal(err)
	}
	if got := len(w.reports.Seen(lea, w.chat)); got != MaxOpenPerChild+5 {
		t.Fatalf("seen after the guardian looked: %d", got)
	}
	_ = seqs
}

func TestOpenRejectsADismissedRecordThatStillHoldsText(t *testing.T) {
	w := newWorld(t)
	seq := w.say(w.miaP, w.chat, "x")
	if _, err := w.reports.File(w.leaP, w.chat, seq); err != nil {
		t.Fatal(err)
	}
	list, _ := w.reports.ForChild("anna", lea.TenantID, lea.ChildID)
	if err := w.reports.Dismiss("anna", lea.TenantID, lea.ChildID, list[0].ID); err != nil {
		t.Fatal(err)
	}
	good, _ := os.ReadFile(w.path)
	if _, err := Open(w.path, w.messages, w.families, w.clock.now); err != nil {
		t.Fatalf("marker file rejected: %v", err)
	}
	for name, broken := range map[string]string{
		"text":       strings.Replace(string(good), `"body":""`, `"body":"heimlich behalten"`, 1),
		"name":       strings.Replace(string(good), `"sender_name":""`, `"sender_name":"Mia"`, 1),
		"never seen": regexp.MustCompile(`"seen_at":"[^"]+",`).ReplaceAllString(string(good), ""),
	} {
		if broken == string(good) {
			t.Fatalf("%s: fixture did not change", name)
		}
		if err := os.WriteFile(w.path, []byte(broken), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(w.path, w.messages, w.families, w.clock.now); err == nil {
			t.Errorf("%s: Open accepted an inconsistent marker", name)
		}
	}
}
