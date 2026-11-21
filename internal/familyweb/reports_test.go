package familyweb

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/messages"
	"github.com/pblumer/yapuno/internal/reports"
)

// cannedMessages answers every authorized read with one message of the peer.
type cannedMessages struct {
	from contacts.Party
	body string
	deny bool
}

func (c *cannedMessages) History(_ messages.Principal, _ string, after uint64, _ int) ([]messages.Message, error) {
	if c.deny {
		return nil, messages.ErrForbidden
	}
	return []messages.Message{{Seq: after + 1, From: c.from, Body: c.body, At: time.Date(2026, 11, 19, 17, 30, 0, 0, time.UTC)}}, nil
}

type reportWorld struct {
	*contactWorld
	reports *reports.Store
	canned  *cannedMessages
	link    string
}

func newReportWorld(t *testing.T) *reportWorld {
	t.Helper()
	cw := newContactWorld(t)
	rw := &reportWorld{contactWorld: cw, canned: &cannedMessages{from: bea}}
	var err error
	if rw.reports, err = reports.Open(filepath.Join(t.TempDir(), "reports.json"), rw.canned, cw.families, time.Now); err != nil {
		t.Fatal(err)
	}
	_, _, rw.link = pendingLink(t, cw)
	if err := cw.store.Confirm("guardian-a", "tenant-a", "child-a", rw.link); err != nil {
		t.Fatal(err)
	}
	return rw
}

func (rw *reportWorld) as(t *testing.T, s Session) http.Handler {
	t.Helper()
	h, err := NewContactPagesWithReports(testAuth{session: s}, rw.store, rw.families, rw.looks, rw.reports)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// report files a report of Alex about a message of Bea with this text.
func (rw *reportWorld) report(t *testing.T, seq uint64, body string) {
	t.Helper()
	rw.canned.body = body
	if created, err := rw.reports.File(messages.Principal{Party: alex, DeviceID: "dev-secret-1"}, rw.link, seq); err != nil || !created {
		t.Fatalf("report: %v %v", created, err)
	}
}

var reportIDPattern = regexp.MustCompile(`name="report_id" value="([0-9a-f]{32})"`)

func TestGuardianSeesExactlyTheReportedMessagesOfTheirChild(t *testing.T) {
	rw := newReportWorld(t)
	rw.report(t, 3, `Gemein <script>alert(1)</script> & "so"`)
	rw.report(t, 5, "Zweite Meldung")
	w := contactGet(rw.as(t, sessionA), "child-a")
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("page: %d", w.Code)
	}
	for _, want := range []string{
		"Meldungen von Alex", "Nachricht von Bea", "Gemein &lt;script&gt;alert(1)&lt;/script&gt; &amp; &#34;so&#34;", "Zweite Meldung",
		`action="/family/contacts/report-done"`, "nicht den übrigen Chat", "19.11.2026", "30 Tagen",
		// Guardians are told what the child learns from their visit.
		"Alex sieht in Yapuno, dass ihr die Meldung gesehen habt",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if len(reportIDPattern.FindAllString(body, -1)) != 2 {
		t.Error("each report needs its own dismiss form")
	}
	for _, absent := range []string{"<script", "tenant-b", "child-b", "dev-secret-1"} {
		if strings.Contains(body, absent) {
			t.Errorf("page contains %q", absent)
		}
	}
	// The reported child's family learns nothing, and neither does a third family.
	for name, s := range map[string]Session{"reported family": sessionB, "third family": sessionC} {
		child := map[string]string{"guardian-b": "child-b", "guardian-c": "child-c"}[s.GuardianID]
		page := contactGet(rw.as(t, s), child).Body.String()
		if strings.Contains(page, "Meldung") || strings.Contains(page, "Gemein") || strings.Contains(page, "report_id") {
			t.Errorf("%s sees a report", name)
		}
	}
	// Asking for another family's child is refused before anything is read.
	if w := contactGet(rw.as(t, sessionB), "child-a"); w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "Gemein") {
		t.Fatalf("foreign child: %d", w.Code)
	}
}

func TestGuardianDismissesAReport(t *testing.T) {
	rw := newReportWorld(t)
	rw.report(t, 3, "weg damit")
	a := rw.as(t, sessionA)
	id := reportIDPattern.FindStringSubmatch(contactGet(a, "child-a").Body.String())[1]
	done := func(s Session, child, report string, confirm ...string) *httptest.ResponseRecorder {
		form := url.Values{"csrf_token": {s.CSRFToken}, "child_id": {child}, "report_id": {report}}
		if len(confirm) > 0 {
			form["confirm"] = confirm
		}
		return contactPost(rw.as(t, s), "report-done", form)
	}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"no confirmation":        done(sessionA, "child-a", id),
		"confirmation not yes":   done(sessionA, "child-a", id, "on"),
		"duplicate confirmation": done(sessionA, "child-a", id, "yes", "yes"),
		"malformed id":           done(sessionA, "child-a", "../x", "yes"),
		"other family":           done(sessionB, "child-b", id, "yes"),
		"other family as ours":   done(sessionB, "child-a", id, "yes"),
		"unknown report":         done(sessionA, "child-a", strings.Repeat("0", 32), "yes"),
	} {
		if w.Code < 400 {
			t.Errorf("%s: accepted with %d", name, w.Code)
		}
	}
	if w := done(sessionA, "child-a", "../x", "yes"); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed report id must be refused before the store is asked: %d", w.Code)
	}
	if rw.reports.Count("guardian-a", "tenant-a", "child-a") != 1 {
		t.Fatal("a refused dismissal removed the report")
	}
	w := done(sessionA, "child-a", id, "yes")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family/contacts?child_id=child-a&notice=report-done" {
		t.Fatalf("dismiss: %d %q", w.Code, w.Header().Get("Location"))
	}
	after := contactGet(a, "child-a")
	if strings.Contains(after.Body.String(), "weg damit") || strings.Contains(after.Body.String(), "Meldungen von Alex") {
		t.Fatal("dismissed report still shown")
	}
	r := httptest.NewRequest(http.MethodGet, "/family/contacts?child_id=child-a&notice=report-done", nil)
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, r)
	if !strings.Contains(rec.Body.String(), `role="status"`) || !strings.Contains(rec.Body.String(), "Meldung geschlossen") || !strings.Contains(rec.Body.String(), "Kopie der Nachricht ist gelöscht") {
		t.Fatal("dismissal not confirmed, or confirmed with more than is true")
	}
}

func TestGuardianLiftsOnlyTheBlockOfTheirOwnChild(t *testing.T) {
	rw := newReportWorld(t)
	if err := rw.store.Block(alex, rw.link); err != nil {
		t.Fatal(err)
	}
	pageA := contactGet(rw.as(t, sessionA), "child-a").Body.String()
	if !strings.Contains(pageA, "Von Alex blockiert") || !strings.Contains(pageA, `action="/family/contacts/unblock"`) || !strings.Contains(pageA, "Bea") {
		t.Fatal("blocking child's guardian cannot see or lift the block")
	}
	if strings.Contains(pageA, "Von beiden Familien freigegeben") {
		t.Fatal("blocked contact still listed as active")
	}
	// The other family sees a neutral pause, no accusation and no way to lift it.
	pageB := contactGet(rw.as(t, sessionB), "child-b").Body.String()
	if !strings.Contains(pageB, "Pausierte Kontakte") || !strings.Contains(pageB, "Alex") || strings.Contains(strings.ToLower(pageB), "blockiert") || strings.Contains(pageB, "/family/contacts/unblock") {
		t.Fatal("other family's page is wrong")
	}
	if !strings.Contains(pageB, `action="/family/contacts/remove"`) {
		t.Fatal("other family must still be able to end the contact")
	}
	lift := func(s Session, child string, confirm ...string) *httptest.ResponseRecorder {
		form := url.Values{"csrf_token": {s.CSRFToken}, "child_id": {child}, "link_id": {rw.link}}
		if len(confirm) > 0 {
			form["confirm"] = confirm
		}
		return contactPost(rw.as(t, s), "unblock", form)
	}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"other family":         lift(sessionB, "child-b", "yes"),
		"other family as ours": lift(sessionB, "child-a", "yes"),
		"third family":         lift(sessionC, "child-c", "yes"),
		"no confirmation":      lift(sessionA, "child-a"),
		"confirmation not yes": lift(sessionA, "child-a", "on"),
	} {
		if w.Code < 400 {
			t.Errorf("%s: accepted with %d", name, w.Code)
		}
		if rw.store.Allowed(alex, bea) {
			t.Fatalf("%s lifted the block", name)
		}
	}
	w := lift(sessionA, "child-a", "yes")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family/contacts?child_id=child-a&notice=unblocked" {
		t.Fatalf("unblock: %d %q", w.Code, w.Header().Get("Location"))
	}
	if !rw.store.Allowed(alex, bea) {
		t.Fatal("contact not restored")
	}
	if page := contactGet(rw.as(t, sessionA), "child-a").Body.String(); strings.Contains(page, "Von Alex blockiert") || !strings.Contains(page, "Von beiden Familien freigegeben") {
		t.Fatal("page after lifting the block is wrong")
	}
}

func TestContactPagesWorkWithoutAReportStore(t *testing.T) {
	rw := newReportWorld(t)
	rw.report(t, 3, "unsichtbar ohne Speicher")
	h := rw.contactWorld.as(t, testAuth{session: sessionA})
	if page := contactGet(h, "child-a").Body.String(); strings.Contains(page, "unsichtbar") || strings.Contains(page, "Meldungen von") {
		t.Fatal("reports shown without a report store")
	}
	form := url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}, "report_id": {strings.Repeat("0", 32)}, "confirm": {"yes"}}
	if w := contactPost(h, "report-done", form); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("report route without report store: %d", w.Code)
	}
	if _, err := NewContactPagesWithReports(testAuth{session: sessionA}, rw.store, rw.families, rw.looks, nil); err == nil {
		t.Fatal("constructor accepted a missing report store")
	}
}

type failingReports struct{ ReportStore }

func (failingReports) ForChild(string, string, string) ([]reports.Report, error) {
	return nil, errors.New("disk")
}

func TestContactPageFailsVisiblyWhenReportsCannotBeRead(t *testing.T) {
	rw := newReportWorld(t)
	h, err := NewContactPagesWithReports(testAuth{session: sessionA}, rw.store, rw.families, rw.looks, failingReports{rw.reports})
	if err != nil {
		t.Fatal(err)
	}
	if w := contactGet(h, "child-a"); w.Code != http.StatusInternalServerError {
		t.Fatalf("unreadable reports must not look like 'no reports': %d", w.Code)
	}
}

func TestFamilyPageShowsOpenReportsPerChild(t *testing.T) {
	rw := newReportWorld(t)
	page := func(s Session) string {
		h, err := NewWithOptions(testAuth{session: s}, rw.families, Options{ContactPages: true, Reports: rw.reports})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family", nil))
		return w.Body.String()
	}
	if strings.Contains(page(sessionA), "Meldung") {
		t.Fatal("report hint without a report")
	}
	rw.report(t, 3, "eins")
	if p := page(sessionA); !strings.Contains(p, "1 offene Meldung") || strings.Contains(p, "eins") {
		t.Fatal("family page must show the count but never the content")
	}
	rw.report(t, 4, "zwei")
	if !strings.Contains(page(sessionA), "2 offene Meldungen") {
		t.Fatal("plural count missing")
	}
	if strings.Contains(page(sessionB), "Meldung") {
		t.Fatal("the reported child's family sees a hint")
	}
}

// seeingReports records MarkSeen calls and can make them fail.
type seeingReports struct {
	ReportStore
	seen []string
	fail bool
}

func (s *seeingReports) MarkSeen(guardianID, tenantID, childID string) error {
	s.seen = append(s.seen, guardianID+"/"+tenantID+"/"+childID)
	if s.fail {
		return errors.New("disk")
	}
	return s.ReportStore.MarkSeen(guardianID, tenantID, childID)
}

func TestOpeningTheContactPageMarksTheChildsReportsAsSeen(t *testing.T) {
	rw := newReportWorld(t)
	spy := &seeingReports{ReportStore: rw.reports}
	as := func(s Session) http.Handler {
		h, err := NewContactPagesWithReports(testAuth{session: s}, rw.store, rw.families, rw.looks, spy)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	// No report yet: nothing to mark, nothing written.
	contactGet(as(sessionA), "child-a")
	if len(spy.seen) != 0 {
		t.Fatalf("marked without a report: %v", spy.seen)
	}
	rw.report(t, 3, "bitte ansehen")
	// Pages that do not show the report do not mark it: the other family's
	// page, a refused page, and the family overview with its count.
	contactGet(as(sessionB), "child-b")
	if w := contactGet(as(sessionB), "child-a"); w.Code != http.StatusForbidden {
		t.Fatalf("foreign page: %d", w.Code)
	}
	overview, err := NewWithOptions(testAuth{session: sessionA}, rw.families, Options{ContactPages: true, Reports: rw.reports})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	overview.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/family", nil))
	if !strings.Contains(rec.Body.String(), "1 offene Meldung") {
		t.Fatal("precondition: overview shows the count")
	}
	if len(spy.seen) != 0 {
		t.Fatalf("marked by a page that does not show the report: %v", spy.seen)
	}
	// The guardian opens the page with the report: marked for exactly this child, with the session's identity.
	w := contactGet(as(sessionA), "child-a")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "bitte ansehen") {
		t.Fatalf("page: %d", w.Code)
	}
	if len(spy.seen) != 1 || spy.seen[0] != "guardian-a/tenant-a/child-a" {
		t.Fatalf("seen calls: %v", spy.seen)
	}
	// The report is still there afterwards.
	if rw.reports.Count("guardian-a", "tenant-a", "child-a") != 1 {
		t.Fatal("viewing deleted the report")
	}
}

func TestContactPageStillShowsReportsWhenSeenCannotBeRecorded(t *testing.T) {
	rw := newReportWorld(t)
	rw.report(t, 3, "trotzdem sichtbar")
	spy := &seeingReports{ReportStore: rw.reports, fail: true}
	h, err := NewContactPagesWithReports(testAuth{session: sessionA}, rw.store, rw.families, rw.looks, spy)
	if err != nil {
		t.Fatal(err)
	}
	w := contactGet(h, "child-a")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "trotzdem sichtbar") || len(spy.seen) != 1 {
		t.Fatalf("page with failing seen mark: %d %v", w.Code, spy.seen)
	}
}
