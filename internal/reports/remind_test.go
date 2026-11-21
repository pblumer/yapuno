package reports

import (
	"context"
	"testing"
	"time"
)

// remindWorld wires the real stores of newWorld to an announcer with fakes
// for addresses and mail, sharing the world's clock.
func remindWorld(t *testing.T) (*world, *Notifying, *Reminder, *fakeNoticeMailer) {
	t.Helper()
	w := newWorld(t)
	mailer := &fakeNoticeMailer{}
	addresses := fakeAddresses{"family-a/anna": "anna@example.org", "family-a/albert": "albert@example.org", "family-b/bruno": "bruno@example.org"}
	a, err := NewAnnouncer(w.families, addresses, mailer, w.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	notifying, err := WithNotices(w.reports, a)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewReminder(w.reports, a)
	if err != nil {
		t.Fatal(err)
	}
	return w, notifying, r, mailer
}

func TestUnseenReportIsRemindedTwiceThenNeverAgain(t *testing.T) {
	w, notifying, reminder, mailer := remindWorld(t)
	seq := w.say(w.miaP, w.chat, "bitte ansehen")
	if _, err := notifying.File(w.leaP, w.chat, seq); err != nil {
		t.Fatal(err)
	}
	notifying.announcer.Wait()
	if len(mailer.list()) != 2 || len(mailer.reminded()) != 0 {
		t.Fatalf("first notice: %v, reminders: %v", mailer.list(), mailer.reminded())
	}
	ctx := context.Background()
	if n := reminder.Run(ctx); n != 0 {
		t.Fatalf("reminder right after the report: %d", n)
	}
	w.clock.advance(3 * time.Hour)
	if n := reminder.Run(ctx); n != 1 {
		t.Fatalf("first reminder: %d", n)
	}
	got := mailer.reminded()
	if len(got) != 2 || got[0] != (sentNotice{"albert@example.org", "Lea"}) || got[1] != (sentNotice{"anna@example.org", "Lea"}) {
		t.Fatalf("first reminder recipients: %+v", got)
	}
	// The timer fires every few minutes: nothing more until the next stage.
	for i := 0; i < 12; i++ {
		w.clock.advance(5 * time.Minute)
		reminder.Run(ctx)
	}
	if len(mailer.reminded()) != 2 {
		t.Fatalf("repeated first reminder: %v", mailer.reminded())
	}
	w.clock.advance(20 * time.Hour)
	if n := reminder.Run(ctx); n != 1 || len(mailer.reminded()) != 4 {
		t.Fatalf("second reminder: %d %v", n, mailer.reminded())
	}
	for i := 0; i < 10; i++ {
		w.clock.advance(24 * time.Hour)
		reminder.Run(ctx)
	}
	if len(mailer.reminded()) != 4 || len(mailer.list()) != 2 {
		t.Fatalf("more than two reminders: %v", mailer.reminded())
	}
}

func TestNoReminderOnceAGuardianHasSeenOrDismissedTheReport(t *testing.T) {
	w, notifying, reminder, mailer := remindWorld(t)
	seen := w.say(w.miaP, w.chat, "gesehen")
	if _, err := notifying.File(w.leaP, w.chat, seen); err != nil {
		t.Fatal(err)
	}
	other := w.connect("anna", tim, "bruno", mia)
	timP := w.device("anna", tim)
	gone := w.say(w.miaP, other, "erledigt")
	if _, err := notifying.File(timP, other, gone); err != nil {
		t.Fatal(err)
	}
	notifying.announcer.Wait()
	if err := w.reports.MarkSeen("albert", lea.TenantID, lea.ChildID); err != nil {
		t.Fatal(err)
	}
	list, _ := w.reports.ForChild("anna", tim.TenantID, tim.ChildID)
	if err := w.reports.Dismiss("anna", tim.TenantID, tim.ChildID, list[0].ID); err != nil {
		t.Fatal(err)
	}
	for _, step := range []time.Duration{3 * time.Hour, 21 * time.Hour, 24 * time.Hour} {
		w.clock.advance(step)
		if n := reminder.Run(context.Background()); n != 0 {
			t.Fatalf("reminder for a seen or dismissed report: %d", n)
		}
	}
	if len(mailer.reminded()) != 0 {
		t.Fatalf("reminders: %v", mailer.reminded())
	}
}

func TestReminderRespectsTheHourlyLimitAndWaitsItsTurn(t *testing.T) {
	w, notifying, reminder, mailer := remindWorld(t)
	first := w.say(w.miaP, w.chat, "erste")
	if _, err := notifying.File(w.leaP, w.chat, first); err != nil {
		t.Fatal(err)
	}
	notifying.announcer.Wait()
	// Ten minutes before the first reminder is due, Lea reports again: that
	// report is announced, so the reminder must wait for the hour to pass.
	w.clock.advance(2*time.Hour + 50*time.Minute)
	second := w.say(w.miaP, w.chat, "zweite")
	if _, err := notifying.File(w.leaP, w.chat, second); err != nil {
		t.Fatal(err)
	}
	notifying.announcer.Wait()
	if len(mailer.list()) != 4 {
		t.Fatalf("second report not announced: %v", mailer.list())
	}
	w.clock.advance(10 * time.Minute)
	if n := reminder.Run(context.Background()); n != 0 || len(mailer.reminded()) != 0 {
		t.Fatalf("reminder within the hour of another mail: %d", n)
	}
	w.clock.advance(50 * time.Minute)
	if n := reminder.Run(context.Background()); n != 1 || len(mailer.reminded()) != 2 {
		t.Fatalf("deferred reminder not sent after the hour: %d %v", n, mailer.reminded())
	}
	// And a reminder in turn holds back the notice for a report filed right after it.
	third := w.say(w.miaP, w.chat, "dritte")
	if _, err := notifying.File(w.leaP, w.chat, third); err != nil {
		t.Fatal(err)
	}
	notifying.announcer.Wait()
	if len(mailer.list()) != 4 {
		t.Fatalf("more than one mail per hour and child: %v", mailer.list())
	}
}

func TestFailedReminderIsRetriedAtTheNextRun(t *testing.T) {
	w, notifying, reminder, mailer := remindWorld(t)
	seq := w.say(w.miaP, w.chat, "x")
	if _, err := notifying.File(w.leaP, w.chat, seq); err != nil {
		t.Fatal(err)
	}
	notifying.announcer.Wait()
	w.clock.advance(3 * time.Hour)
	mailer.fail = map[string]bool{"anna@example.org": true, "albert@example.org": true}
	if n := reminder.Run(context.Background()); n != 0 {
		t.Fatalf("failed reminder counted: %d", n)
	}
	mailer.fail = nil
	w.clock.advance(5 * time.Minute)
	if n := reminder.Run(context.Background()); n != 1 || len(mailer.reminded()) != 2 {
		t.Fatalf("retry: %d %v", n, mailer.reminded())
	}
	// If the reminder went out but could not be recorded, the hourly limit
	// still prevents a mail at every run.
	w.clock.advance(21 * time.Hour)
	w.reports.failWrite = true
	reminder.Run(context.Background())
	w.clock.advance(5 * time.Minute)
	reminder.Run(context.Background())
	w.reports.failWrite = false
	if got := len(mailer.reminded()); got != 4 {
		t.Fatalf("unrecorded reminder repeated within the hour: %d mails", got)
	}
}

func TestReminderRunsDoNotOverlapAndStopWithTheContext(t *testing.T) {
	w, notifying, reminder, mailer := remindWorld(t)
	seq := w.say(w.miaP, w.chat, "x")
	if _, err := notifying.File(w.leaP, w.chat, seq); err != nil {
		t.Fatal(err)
	}
	notifying.announcer.Wait()
	w.clock.advance(3 * time.Hour)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if n := reminder.Run(cancelled); n != 0 || len(mailer.reminded()) != 0 {
		t.Fatalf("run after shutdown sent mail: %d", n)
	}
	done := make(chan int, 8)
	for i := 0; i < 8; i++ {
		go func() { done <- reminder.Run(context.Background()) }()
	}
	total := 0
	for i := 0; i < 8; i++ {
		total += <-done
	}
	if total != 1 || len(mailer.reminded()) != 2 {
		t.Fatalf("concurrent runs reminded %d times: %v", total, mailer.reminded())
	}
	if _, err := NewReminder(nil, notifying.announcer); err == nil {
		t.Fatal("reminder built without a store")
	}
	if _, err := NewReminder(w.reports, nil); err == nil {
		t.Fatal("reminder built without an announcer")
	}
}
