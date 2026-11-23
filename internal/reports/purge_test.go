package reports

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// Deleting a child removes the reports that child filed. A report another
// child filed about a message of the deleted child belongs to that other
// family and stays until they close it or it expires.
func TestPurgeChildRemovesTheReportsThatChildFiled(t *testing.T) {
	w := newWorld(t)
	fromMia := w.say(w.miaP, w.chat, "Gemein von Mia")
	fromLea := w.say(w.leaP, w.chat, "Gemein von Lea")
	timChat := w.connect("anna", tim, "bruno", mia)
	timP := w.device("anna", tim)
	toTim := w.say(w.miaP, timChat, "Auch gemein zu Tim")
	w.clock.advance(time.Minute)
	for name, file := range map[string]func() (bool, error){
		"lea": func() (bool, error) { return w.reports.File(w.leaP, w.chat, fromMia) },
		"mia": func() (bool, error) { return w.reports.File(w.miaP, w.chat, fromLea) },
		"tim": func() (bool, error) { return w.reports.File(timP, timChat, toTim) },
	} {
		if created, err := file(); err != nil || !created {
			t.Fatalf("%s: %v %v", name, created, err)
		}
	}
	if err := w.reports.PurgeChild(lea.TenantID, lea.ChildID); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(w.path, w.messages, w.families, w.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]*Store{"running": w.reports, "reopened": reopened} {
		if got, err := s.ForChild("anna", lea.TenantID, lea.ChildID); err != nil || len(got) != 0 {
			t.Errorf("%s: reports of the purged child: %+v %v", name, got, err)
		}
		if got, err := s.ForChild("anna", tim.TenantID, tim.ChildID); err != nil || len(got) != 1 {
			t.Errorf("%s: the sibling's report: %+v %v", name, got, err)
		}
		// The other family keeps what their child reported.
		if got, err := s.ForChild("bruno", mia.TenantID, mia.ChildID); err != nil || len(got) != 1 || got[0].Body != "Gemein von Lea" {
			t.Errorf("%s: the other family's report: %+v %v", name, got, err)
		}
	}
	raw, _ := os.ReadFile(w.path)
	if strings.Contains(string(raw), "Gemein von Mia") {
		t.Fatal("the file still holds the copy the purged child reported")
	}
	if err := w.reports.PurgeChild(lea.TenantID, lea.ChildID); err != nil {
		t.Fatalf("second purge: %v", err)
	}
	if err := w.reports.PurgeTenant("family-a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.reports.ForChild("anna", tim.TenantID, tim.ChildID); len(got) != 0 {
		t.Fatalf("reports of the purged family: %+v", got)
	}
	if got, _ := w.reports.ForChild("bruno", mia.TenantID, mia.ChildID); len(got) != 1 {
		t.Fatalf("the other family's report after purging the family: %+v", got)
	}
}

func TestPurgeNeedsACompleteTargetAndADurableWrite(t *testing.T) {
	w := newWorld(t)
	bad := w.say(w.miaP, w.chat, "Gemein")
	if _, err := w.reports.File(w.leaP, w.chat, bad); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"no tenant":     w.reports.PurgeChild("", "lea"),
		"no child":      w.reports.PurgeChild("family-a", ""),
		"tenant, no id": w.reports.PurgeTenant(""),
	} {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	w.reports.failWrite = true
	if err := w.reports.PurgeChild(lea.TenantID, lea.ChildID); err == nil {
		t.Fatal("purge acknowledged without a durable write")
	}
	w.reports.failWrite = false
	if got, _ := w.reports.ForChild("anna", lea.TenantID, lea.ChildID); len(got) != 1 {
		t.Fatalf("a failed or incomplete purge removed a report: %+v", got)
	}
}
