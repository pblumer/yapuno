package reports

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/contacts"
)

func parties(list []contacts.Party) []string {
	var out []string
	for _, p := range list {
		out = append(out, p.TenantID+"/"+p.ChildID)
	}
	return out
}

func TestReminderIsDueAfterThreeAndTwentyFourHoursUntilSeen(t *testing.T) {
	w := newWorld(t)
	seq := w.say(w.miaP, w.chat, "ungesehen")
	if _, err := w.reports.File(w.leaP, w.chat, seq); err != nil {
		t.Fatal(err)
	}
	due := func() []string { return parties(w.reports.RemindersDue()) }
	if len(due()) != 0 {
		t.Fatal("reminder due immediately")
	}
	w.clock.advance(3*time.Hour - time.Second)
	if len(due()) != 0 {
		t.Fatal("reminder due before three hours")
	}
	w.clock.advance(time.Second)
	if got := due(); !reflect.DeepEqual(got, []string{"family-a/lea"}) {
		t.Fatalf("after three hours: %v", got)
	}
	// Until it is recorded as sent it stays due; afterwards not until the next stage.
	if err := w.reports.MarkReminded(lea); err != nil {
		t.Fatal(err)
	}
	if len(due()) != 0 {
		t.Fatal("first reminder due twice")
	}
	w.clock.advance(21*time.Hour - time.Second)
	if len(due()) != 0 {
		t.Fatal("second reminder due before twenty-four hours")
	}
	w.clock.advance(time.Second)
	if got := due(); !reflect.DeepEqual(got, []string{"family-a/lea"}) {
		t.Fatalf("after twenty-four hours: %v", got)
	}
	if err := w.reports.MarkReminded(lea); err != nil {
		t.Fatal(err)
	}
	// Two reminders, then silence for the rest of the retention.
	for i := 0; i < 20; i++ {
		w.clock.advance(24 * time.Hour)
		if len(due()) != 0 {
			t.Fatalf("third reminder due after %d more days", i+1)
		}
	}
}

func TestSeenReportIsNeverRemindedAndOnlyOwnGuardiansCanMarkIt(t *testing.T) {
	w := newWorld(t)
	seq := w.say(w.miaP, w.chat, "wird gesehen")
	if _, err := w.reports.File(w.leaP, w.chat, seq); err != nil {
		t.Fatal(err)
	}
	if err := w.families.BootstrapGuardian(authz.GuardianGrant{GuardianID: "dora", ChildID: "tim", TenantID: "family-a", Active: true, CanManageDevices: true, CanApproveContacts: true}); err != nil {
		t.Fatal(err)
	}
	w.clock.advance(4 * time.Hour)
	for name, c := range map[string][3]string{
		"other family":        {"bruno", lea.TenantID, lea.ChildID},
		"guardian of sibling": {"dora", lea.TenantID, lea.ChildID},
		"unknown guardian":    {"nobody", lea.TenantID, lea.ChildID},
		"empty guardian":      {"", lea.TenantID, lea.ChildID},
	} {
		if err := w.reports.MarkSeen(c[0], c[1], c[2]); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Looking at one's own child or at the sibling's page does not mark Lea's report.
	if err := w.reports.MarkSeen("bruno", mia.TenantID, mia.ChildID); err != nil {
		t.Fatal(err)
	}
	if err := w.reports.MarkSeen("anna", tim.TenantID, tim.ChildID); err != nil {
		t.Fatal(err)
	}
	// Listing or counting is not seeing either.
	if _, err := w.reports.ForChild("anna", lea.TenantID, lea.ChildID); err != nil {
		t.Fatal(err)
	}
	_ = w.reports.Count("anna", lea.TenantID, lea.ChildID)
	if len(w.reports.RemindersDue()) != 1 {
		t.Fatal("a refused or unrelated view stopped the reminder")
	}
	// Either equal guardian seeing it stops reminders for good.
	if err := w.reports.MarkSeen("albert", lea.TenantID, lea.ChildID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if len(w.reports.RemindersDue()) != 0 {
			t.Fatal("reminder due although a guardian has seen the report")
		}
		w.clock.advance(24 * time.Hour)
	}
	// The report itself stays until dismissed.
	if w.reports.Count("anna", lea.TenantID, lea.ChildID) != 1 {
		t.Fatal("seeing a report must not delete it")
	}
	// The time is stored, not who looked.
	raw, _ := os.ReadFile(w.path)
	if !strings.Contains(string(raw), `"seen_at":"2026-11-20T16:00:00Z"`) || strings.Contains(string(raw), "albert") {
		t.Fatalf("stored document: %s", raw)
	}
}

func TestReminderBookkeepingIsPerChildAndPerReport(t *testing.T) {
	w := newWorld(t)
	first := w.say(w.miaP, w.chat, "erste")
	if _, err := w.reports.File(w.leaP, w.chat, first); err != nil {
		t.Fatal(err)
	}
	other := w.connect("anna", tim, "bruno", mia)
	timP := w.device("anna", tim)
	w.clock.advance(2 * time.Hour)
	second := w.say(w.miaP, w.chat, "zweite, zwei Stunden später")
	if _, err := w.reports.File(w.leaP, w.chat, second); err != nil {
		t.Fatal(err)
	}
	forTim := w.say(w.miaP, other, "an Tim")
	if _, err := w.reports.File(timP, other, forTim); err != nil {
		t.Fatal(err)
	}
	w.clock.advance(time.Hour)
	// Only Lea's first report is three hours old.
	if got := parties(w.reports.RemindersDue()); !reflect.DeepEqual(got, []string{"family-a/lea"}) {
		t.Fatalf("due: %v", got)
	}
	if err := w.reports.MarkReminded(lea); err != nil {
		t.Fatal(err)
	}
	// Two hours later the second report of Lea and Tim's report reach three hours.
	w.clock.advance(2 * time.Hour)
	if got := parties(w.reports.RemindersDue()); !reflect.DeepEqual(got, []string{"family-a/lea", "family-a/tim"}) {
		t.Fatalf("due: %v", got)
	}
	if err := w.reports.MarkReminded(tim); err != nil {
		t.Fatal(err)
	}
	if got := parties(w.reports.RemindersDue()); !reflect.DeepEqual(got, []string{"family-a/lea"}) {
		t.Fatalf("marking Tim must not mark Lea: %v", got)
	}
	// A new report after a guardian looked is unseen again.
	if err := w.reports.MarkSeen("anna", lea.TenantID, lea.ChildID); err != nil {
		t.Fatal(err)
	}
	third := w.say(w.miaP, w.chat, "dritte, nach dem Ansehen")
	if _, err := w.reports.File(w.leaP, w.chat, third); err != nil {
		t.Fatal(err)
	}
	w.clock.advance(3 * time.Hour)
	if got := parties(w.reports.RemindersDue()); !reflect.DeepEqual(got, []string{"family-a/lea"}) {
		t.Fatalf("new report after an earlier view: %v", got)
	}
}

func TestOldUnseenReportGetsOneReminderNotTwoAtOnce(t *testing.T) {
	w := newWorld(t)
	seq := w.say(w.miaP, w.chat, "lange unbemerkt")
	if _, err := w.reports.File(w.leaP, w.chat, seq); err != nil {
		t.Fatal(err)
	}
	// The server was down, or the feature is new: the report is two days old.
	w.clock.advance(48 * time.Hour)
	if len(w.reports.RemindersDue()) != 1 {
		t.Fatal("old unseen report not due")
	}
	if err := w.reports.MarkReminded(lea); err != nil {
		t.Fatal(err)
	}
	if len(w.reports.RemindersDue()) != 0 {
		t.Fatal("both reminders sent in a row for an old report")
	}
}

func TestSeenAndReminderStateIsDurableAndEndsWithTheReport(t *testing.T) {
	w := newWorld(t)
	a := w.say(w.miaP, w.chat, "a")
	b := w.say(w.miaP, w.chat, "b")
	for _, seq := range []uint64{a, b} {
		if _, err := w.reports.File(w.leaP, w.chat, seq); err != nil {
			t.Fatal(err)
		}
	}
	w.clock.advance(3 * time.Hour)
	if err := w.reports.MarkReminded(lea); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(w.path, w.messages, w.families, w.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.RemindersDue()) != 0 {
		t.Fatal("reminder state lost on restart")
	}
	if err := reopened.MarkSeen("anna", lea.TenantID, lea.ChildID); err != nil {
		t.Fatal(err)
	}
	again, err := Open(w.path, w.messages, w.families, w.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	w.clock.advance(24 * time.Hour)
	if len(again.RemindersDue()) != 0 {
		t.Fatal("seen state lost on restart")
	}
	// Seeing again writes nothing new.
	before, _ := os.ReadFile(w.path)
	if err := again.MarkSeen("anna", lea.TenantID, lea.ChildID); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(w.path); string(after) != string(before) {
		t.Fatal("a repeated view rewrote the store")
	}
	// Dismissed and expired reports are never due.
	fresh := w.say(w.miaP, w.chat, "c")
	if _, err := again.File(w.leaP, w.chat, fresh); err != nil {
		t.Fatal(err)
	}
	w.clock.advance(3 * time.Hour)
	list, _ := again.ForChild("anna", lea.TenantID, lea.ChildID)
	if err := again.Dismiss("anna", lea.TenantID, lea.ChildID, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(again.RemindersDue()) != 0 {
		t.Fatal("dismissed report still due")
	}
}

func TestFailedWriteKeepsReminderAndSeenStateUnchanged(t *testing.T) {
	w := newWorld(t)
	seq := w.say(w.miaP, w.chat, "x")
	if _, err := w.reports.File(w.leaP, w.chat, seq); err != nil {
		t.Fatal(err)
	}
	w.clock.advance(3 * time.Hour)
	w.reports.failWrite = true
	if err := w.reports.MarkReminded(lea); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("MarkReminded: %v", err)
	}
	if err := w.reports.MarkSeen("anna", lea.TenantID, lea.ChildID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("MarkSeen: %v", err)
	}
	w.reports.failWrite = false
	if len(w.reports.RemindersDue()) != 1 {
		t.Fatal("state changed although the write failed")
	}
}

func TestOpenRejectsImpossibleReminderCounts(t *testing.T) {
	w := newWorld(t)
	seq := w.say(w.miaP, w.chat, "x")
	if _, err := w.reports.File(w.leaP, w.chat, seq); err != nil {
		t.Fatal(err)
	}
	good, _ := os.ReadFile(w.path)
	for _, bad := range []string{`"reminders":3`, `"reminders":-1`} {
		broken := strings.Replace(string(good), `"reported_at"`, bad+`,"reported_at"`, 1)
		if err := os.WriteFile(w.path, []byte(broken), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(w.path, w.messages, w.families, w.clock.now); err == nil {
			t.Errorf("Open accepted %s", bad)
		}
	}
}

func TestExpiredReportIsNeverReminded(t *testing.T) {
	w := newWorld(t)
	seq := w.say(w.miaP, w.chat, "zu alt")
	if _, err := w.reports.File(w.leaP, w.chat, seq); err != nil {
		t.Fatal(err)
	}
	// Nobody was reminded, for example because the server was not running.
	w.clock.advance(Retention)
	if got := w.reports.RemindersDue(); len(got) != 0 {
		t.Fatalf("reminder for a report that is no longer shown: %v", got)
	}
	if err := w.reports.MarkSeen("anna", lea.TenantID, lea.ChildID); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(w.path); strings.Contains(string(raw), "seen_at") {
		t.Fatal("an expired report was marked as seen")
	}
}
