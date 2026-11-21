package childweb

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/messages"
	"github.com/pblumer/yapuno/internal/reports"
	"github.com/pblumer/yapuno/internal/skin"
)

type safetyWorld struct {
	*chatWorld
	reports *reports.Store
	n       int
}

func newSafetyWorld(t *testing.T, withReports bool) *safetyWorld {
	t.Helper()
	cw := newChatWorld(t)
	sw := &safetyWorld{chatWorld: cw}
	opt := Options{Contacts: cw.contacts, Looks: cw.looks, Messages: cw.store}
	if withReports {
		var err error
		if sw.reports, err = reports.Open(filepath.Join(t.TempDir(), "reports.json"), cw.store, cw.families, time.Now); err != nil {
			t.Fatal(err)
		}
		opt.Reports = sw.reports
	}
	h, err := New(cw.sessions, cw.families, opt)
	if err != nil {
		t.Fatal(err)
	}
	cw.handler = h
	return sw
}

var (
	alexParty = contacts.Party{TenantID: "tenant-a", ChildID: "child-a"}
	beaParty  = contacts.Party{TenantID: "tenant-b", ChildID: "child-b"}
)

// say stores a message directly and returns its sequence number.
func (sw *safetyWorld) say(t *testing.T, from contacts.Party, body string) uint64 {
	t.Helper()
	sw.n++
	var device string
	for _, d := range mustDevices(t, sw, from) {
		device = d
	}
	m, _, err := sw.store.Send(messages.Principal{Party: from, DeviceID: device}, sw.chat, fmt.Sprintf("safety-client-id-%06d", sw.n), body)
	if err != nil {
		t.Fatal(err)
	}
	return m.Seq
}

func mustDevices(t *testing.T, sw *safetyWorld, p contacts.Party) []string {
	t.Helper()
	guardian := map[string]string{"tenant-a": "guardian-a", "tenant-b": "guardian-b", "tenant-c": "guardian-c"}[p.TenantID]
	list, err := sw.pairing.ListDevices(guardian, p.TenantID, p.ChildID)
	if err != nil || len(list) == 0 {
		t.Fatalf("no device for %+v: %v", p, err)
	}
	var ids []string
	for _, d := range list {
		ids = append(ids, d.ID)
	}
	return ids
}

func (sw *safetyWorld) safety(cookie *http.Cookie, query string) *httptest.ResponseRecorder {
	return sw.get("/child/chat/"+sw.chat+"/safety"+query, cookie)
}

func (sw *safetyWorld) act(cookie *http.Cookie, action string, form url.Values, contentType string) *httptest.ResponseRecorder {
	if contentType == "" {
		contentType = "application/x-www-form-urlencoded"
	}
	r := httptest.NewRequest(http.MethodPost, origin+"/child/chat/"+sw.chat+"/"+action, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", contentType)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	sw.handler.ServeHTTP(rec, r)
	return rec
}

func (sw *safetyWorld) token(t *testing.T, cookie *http.Cookie) string {
	t.Helper()
	m := csrfInput.FindStringSubmatch(sw.get("/child", cookie).Body.String())
	if m == nil {
		t.Fatal("no CSRF token on the home page")
	}
	return m[1]
}

func (sw *safetyWorld) reportsOf(t *testing.T, guardian string, p contacts.Party) []reports.Report {
	t.Helper()
	list, err := sw.reports.ForChild(guardian, p.TenantID, p.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestChatPageLinksToSafetyPage(t *testing.T) {
	sw := newSafetyWorld(t, true)
	for _, cookie := range []*http.Cookie{sw.alex, sw.bea} {
		body := sw.page(cookie).Body.String()
		if !strings.Contains(body, `href="/child/chat/`+sw.chat+`/safety"`) || !strings.Contains(body, `aria-label="Melden oder blockieren"`) {
			t.Fatal("chat page does not offer reporting and blocking")
		}
	}
}

func TestSafetyPageOffersOnlyTheOtherChildsMessages(t *testing.T) {
	sw := newSafetyWorld(t, true)
	if err := sw.looks.Set(appearance.Subject{Kind: appearance.KindChild, TenantID: "tenant-a", ID: "child-a"},
		appearance.Preference{Skin: "night", Figure: appearance.Default().Figure}); err != nil {
		t.Fatal(err)
	}
	sw.say(t, alexParty, "Eigene Nachricht von Alex")
	first := sw.say(t, beaParty, "Erste von Bea")
	hostile := sw.say(t, beaParty, `<img src=x onerror=alert(1)> & "Zitat"`)
	w := sw.safety(sw.alex, "")
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("safety page: %d", w.Code)
	}
	for _, want := range []string{
		`<html lang="de">`, `data-yp-skin="night"`, "<h1>Melden oder blockieren</h1>", `href="/child/chat/` + sw.chat + `"`,
		"Erste von Bea", "&lt;img src=x onerror=alert(1)&gt; &amp; &#34;Zitat&#34;",
		fmt.Sprintf(`type="radio" name="seq" value="%d"`, first), fmt.Sprintf(`name="seq" value="%d"`, hostile),
		`action="/child/chat/` + sw.chat + `/report"`, `action="/child/chat/` + sw.chat + `/block"`,
		`name="csrf_token" value="`, `type="checkbox" name="block" value="yes"`, `type="checkbox" name="confirm" value="yes" required`,
		"genau diese eine Nachricht", "Nur deine Eltern", "Bea",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Index(body, "Erste von Bea") < strings.Index(body, "onerror") {
		t.Error("newest message must come first")
	}
	for _, absent := range []string{"Eigene Nachricht von Alex", "<img", "<script", "tenant-b", "child-b", "tenant-a", "child-a", `name="block" value="yes" required`} {
		if strings.Contains(body, absent) {
			t.Errorf("page contains %q", absent)
		}
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "script-src") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: %v", w.Header())
	}
	style := body[strings.Index(body, "<style>"):strings.Index(body, "</style>")]
	tokens, err := skin.CSSVariables(skin.Catalog()["night"])
	if err != nil {
		t.Fatal(err)
	}
	decls := strings.TrimSuffix(strings.TrimPrefix(tokens, ":root{"), "}")
	if hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).FindAllString(strings.Replace(style, decls, "", 1), -1); len(hex) != 0 {
		t.Fatalf("hard-coded colors: %v", hex)
	}
	// Bea sees her side: Alex's message, not her own.
	bea := sw.safety(sw.bea, "").Body.String()
	if !strings.Contains(bea, "Eigene Nachricht von Alex") || strings.Contains(bea, "Erste von Bea") || !strings.Contains(bea, "Alex") {
		t.Fatal("the other child's safety page is wrong")
	}
	// Nothing to report yet: said plainly, and blocking is still offered.
	empty := newSafetyWorld(t, true)
	page := empty.safety(empty.alex, "").Body.String()
	if !strings.Contains(page, "noch nichts geschrieben") || strings.Contains(page, `name="seq"`) || !strings.Contains(page, "/block") {
		t.Fatal("empty state wrong")
	}
}

func TestSafetyPageIsOnlyForParticipantsWithASession(t *testing.T) {
	sw := newSafetyWorld(t, true)
	sw.say(t, beaParty, "nur für Alex und Bea")
	for name, cookie := range map[string]*http.Cookie{"signed out": nil, "outsider": sw.cem} {
		w := sw.safety(cookie, "")
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/child" || strings.Contains(w.Body.String(), "Bea") {
			t.Errorf("%s: %d %q", name, w.Code, w.Header().Get("Location"))
		}
	}
	if w := sw.get("/child/chat/not-a-conversation/safety", sw.alex); w.Code != http.StatusNotFound {
		t.Fatalf("malformed id: %d", w.Code)
	}
	csrf := sw.token(t, sw.cem)
	for _, action := range []string{"report", "block"} {
		form := url.Values{"csrf_token": {csrf}, "seq": {"1"}, "confirm": {"yes"}}
		if w := sw.act(sw.cem, action, form, ""); w.Code != http.StatusForbidden {
			t.Errorf("outsider %s: %d", action, w.Code)
		}
		if w := sw.act(nil, action, form, ""); w.Code != http.StatusUnauthorized {
			t.Errorf("signed-out %s: %d", action, w.Code)
		}
	}
	if !sw.contacts.Allowed(alexParty, beaParty) || len(sw.reportsOf(t, "guardian-a", alexParty)) != 0 {
		t.Fatal("a refused request had effects")
	}
}

func TestChildReportsOneMessageAndOnlyOwnGuardiansSeeIt(t *testing.T) {
	sw := newSafetyWorld(t, true)
	sw.say(t, alexParty, "Hoi")
	bad := sw.say(t, beaParty, "Das war gemein")
	sw.say(t, beaParty, "Das bleibt privat")
	csrf := sw.token(t, sw.alex)
	// Identity fields in the form are ignored.
	form := url.Values{"csrf_token": {csrf}, "seq": {fmt.Sprint(bad)}, "child_id": {"child-b"}, "tenant_id": {"tenant-b"}}
	w := sw.act(sw.alex, "report", form, "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/child/chat/"+sw.chat+"/safety?done=reported" {
		t.Fatalf("report: %d %q", w.Code, w.Header().Get("Location"))
	}
	list := sw.reportsOf(t, "guardian-a", alexParty)
	if len(list) != 1 || list[0].Body != "Das war gemein" || list[0].From != "Bea" {
		t.Fatalf("guardian view: %+v", list)
	}
	if len(sw.reportsOf(t, "guardian-b", beaParty)) != 0 {
		t.Fatal("the reported child's family sees the report")
	}
	if !sw.contacts.Allowed(alexParty, beaParty) {
		t.Fatal("reporting alone must not block")
	}
	done := sw.safety(sw.alex, "?done=reported").Body.String()
	if !strings.Contains(done, `role="status"`) || !strings.Contains(done, "Gemeldet") || !strings.Contains(done, "Schon gemeldet") {
		t.Fatal("report not confirmed to the child")
	}
	if strings.Contains(done, fmt.Sprintf(`name="seq" value="%d"`, bad)) {
		t.Fatal("an already reported message can be chosen again")
	}
	// Nothing tells Bea: not her chat, not her safety page, not her home page.
	for _, path := range []string{"/child", "/child/chat/" + sw.chat} {
		if body := strings.ToLower(sw.get(path, sw.bea).Body.String()); strings.Contains(body, "gemeldet") {
			t.Errorf("%s tells the reported child", path)
		}
	}
	if body := sw.get("/child/chat/"+sw.chat+"/safety", sw.bea).Body.String(); strings.Contains(body, "Schon gemeldet") || strings.Contains(body, `role="status"`) {
		t.Error("the reported child's safety page reveals the report")
	}
	// Arbitrary notice text is never rendered.
	if forged := sw.safety(sw.alex, "?done=%3Cb%3Ehacked").Body.String(); strings.Contains(forged, "hacked") || strings.Contains(forged, `role="status"`) {
		t.Fatal("arbitrary notice rendered")
	}
}

func TestChildBlocksAndTheChatEndsForBoth(t *testing.T) {
	sw := newSafetyWorld(t, true)
	sw.say(t, beaParty, "vorher")
	csrf := sw.token(t, sw.alex)
	if w := sw.act(sw.alex, "block", url.Values{"csrf_token": {csrf}}, ""); w.Code != http.StatusBadRequest || !sw.contacts.Allowed(alexParty, beaParty) {
		t.Fatalf("block without confirmation: %d", w.Code)
	}
	w := sw.act(sw.alex, "block", url.Values{"csrf_token": {csrf}, "confirm": {"yes"}}, "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/child?done=blocked" {
		t.Fatalf("block: %d %q", w.Code, w.Header().Get("Location"))
	}
	if sw.contacts.Allowed(alexParty, beaParty) {
		t.Fatal("contact still allowed after blocking")
	}
	home := sw.get("/child?done=blocked", sw.alex).Body.String()
	if !strings.Contains(home, `role="status"`) || !strings.Contains(home, "Blockiert") || !strings.Contains(home, "Bea") || !strings.Contains(home, "Nur deine Eltern") {
		t.Fatal("blocking child's home page does not show the block")
	}
	if strings.Contains(home, `href="/child/chat/`+sw.chat) {
		t.Fatal("blocked contact still links to the chat")
	}
	// Bea sees no chat, no name and no hint that she was blocked.
	beaHome := sw.get("/child", sw.bea).Body.String()
	if strings.Contains(beaHome, "Alex") || strings.Contains(strings.ToLower(beaHome), "blockiert") {
		t.Fatal("blocked child's home page reveals the block")
	}
	for name, cookie := range map[string]*http.Cookie{"blocker": sw.alex, "blocked": sw.bea} {
		if p := sw.page(cookie); p.Code != http.StatusSeeOther || strings.Contains(p.Body.String(), "vorher") {
			t.Errorf("%s still opens the chat: %d", name, p.Code)
		}
		if s := sw.safety(cookie, ""); s.Code != http.StatusSeeOther || strings.Contains(s.Body.String(), "vorher") {
			t.Errorf("%s still opens the safety page: %d", name, s.Code)
		}
		if rec, _ := sw.apiHistory(cookie, sw.chat, ""); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "vorher") {
			t.Errorf("%s still reads history: %d", name, rec.Code)
		}
		token := sw.token(t, cookie)
		if rec := sw.apiSend(apiOptions{cookie: cookie, csrf: token}, map[string]string{"client_id": clientID(900), "body": "trotzdem"}); rec.Code != http.StatusForbidden {
			t.Errorf("%s still sends: %d", name, rec.Code)
		}
	}
	// Only fixed notices on the home page.
	if forged := sw.get("/child?done=%3Cb%3Ehacked", sw.alex).Body.String(); strings.Contains(forged, "hacked") || strings.Contains(forged, `role="status"`) {
		t.Fatal("arbitrary notice rendered on the home page")
	}
	// The guardian lifts the block and the conversation is back, with its history.
	if err := sw.contacts.Unblock("guardian-a", "tenant-a", "child-a", sw.chat); err != nil {
		t.Fatal(err)
	}
	if p := sw.page(sw.bea); p.Code != http.StatusOK || !strings.Contains(p.Body.String(), "vorher") {
		t.Fatal("chat not restored after the guardian lifted the block")
	}
}

func TestReportAndBlockInOneStep(t *testing.T) {
	sw := newSafetyWorld(t, true)
	bad := sw.say(t, beaParty, "melden und weg")
	csrf := sw.token(t, sw.alex)
	w := sw.act(sw.alex, "report", url.Values{"csrf_token": {csrf}, "seq": {fmt.Sprint(bad)}, "block": {"yes"}}, "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/child?done=reported-blocked" {
		t.Fatalf("report and block: %d %q", w.Code, w.Header().Get("Location"))
	}
	if list := sw.reportsOf(t, "guardian-a", alexParty); len(list) != 1 || list[0].Body != "melden und weg" {
		t.Fatalf("report missing: %+v", list)
	}
	if sw.contacts.Allowed(alexParty, beaParty) {
		t.Fatal("not blocked")
	}
	if home := sw.get("/child?done=reported-blocked", sw.alex).Body.String(); !strings.Contains(home, "Gemeldet und blockiert") {
		t.Fatal("combined action not confirmed")
	}
}

func TestReportLimitIsExplainedAndBlockingStillWorks(t *testing.T) {
	sw := newSafetyWorld(t, true)
	sw.reports = nil
	limited := &limitedReports{}
	h, err := New(sw.sessions, sw.families, Options{Contacts: sw.contacts, Looks: sw.looks, Messages: sw.store, Reports: limited})
	if err != nil {
		t.Fatal(err)
	}
	sw.handler = h
	seq := sw.say(t, beaParty, "zu viel")
	csrf := sw.token(t, sw.alex)
	for name, c := range map[string]struct {
		err    error
		status int
		text   string
	}{
		"limit":       {reports.ErrLimit, http.StatusTooManyRequests, "schon viele Nachrichten gemeldet"},
		"unavailable": {reports.ErrUnavailable, http.StatusServiceUnavailable, "konnte nicht gespeichert werden"},
	} {
		limited.err = c.err
		w := sw.act(sw.alex, "report", url.Values{"csrf_token": {csrf}, "seq": {fmt.Sprint(seq)}}, "")
		if w.Code != c.status || !strings.Contains(w.Body.String(), `role="alert"`) || !strings.Contains(w.Body.String(), c.text) || !strings.Contains(w.Body.String(), "Eltern") {
			t.Errorf("%s: %d", name, w.Code)
		}
		if !sw.contacts.Allowed(alexParty, beaParty) {
			t.Fatalf("%s: blocked without being asked", name)
		}
	}
	// With the block ticked the child is protected even though the report failed, and is told so.
	w := sw.act(sw.alex, "report", url.Values{"csrf_token": {csrf}, "seq": {fmt.Sprint(seq)}, "block": {"yes"}}, "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/child?done=blocked-unreported" {
		t.Fatalf("report failed with block: %d %q", w.Code, w.Header().Get("Location"))
	}
	if sw.contacts.Allowed(alexParty, beaParty) {
		t.Fatal("not blocked although asked")
	}
	if home := sw.get("/child?done=blocked-unreported", sw.alex).Body.String(); !strings.Contains(home, "Blockiert") || !strings.Contains(home, "nicht gespeichert") {
		t.Fatal("partial outcome not explained")
	}
}

// limitedReports fails every report with a fixed error.
type limitedReports struct{ err error }

func (l *limitedReports) File(messages.Principal, string, uint64) (bool, error) { return false, l.err }
func (l *limitedReports) Reported(contacts.Party, string) map[uint64]bool       { return nil }
func (l *limitedReports) Seen(contacts.Party, string) map[uint64]bool           { return nil }

func TestSafetyActionsRejectInvalidSubmissions(t *testing.T) {
	sw := newSafetyWorld(t, true)
	own := sw.say(t, alexParty, "meine")
	theirs := sw.say(t, beaParty, "ihre")
	csrf := sw.token(t, sw.alex)
	beaCSRF := sw.token(t, sw.bea)
	report := func() url.Values { return url.Values{"csrf_token": {csrf}, "seq": {fmt.Sprint(theirs)}} }
	block := func() url.Values { return url.Values{"csrf_token": {csrf}, "confirm": {"yes"}} }
	cases := map[string]struct {
		action      string
		form        url.Values
		contentType string
		want        int
	}{
		"report wrong content type": {"report", report(), "application/json", http.StatusUnsupportedMediaType},
		"report missing csrf":       {"report", url.Values{"seq": {fmt.Sprint(theirs)}}, "", http.StatusBadRequest},
		"report foreign csrf":       {"report", url.Values{"csrf_token": {beaCSRF}, "seq": {fmt.Sprint(theirs)}}, "", http.StatusForbidden},
		"report missing seq":        {"report", url.Values{"csrf_token": {csrf}}, "", http.StatusBadRequest},
		"report duplicate seq":      {"report", url.Values{"csrf_token": {csrf}, "seq": {fmt.Sprint(theirs), "1"}}, "", http.StatusBadRequest},
		"report text seq":           {"report", url.Values{"csrf_token": {csrf}, "seq": {"abc"}}, "", http.StatusBadRequest},
		"report negative seq":       {"report", url.Values{"csrf_token": {csrf}, "seq": {"-1"}}, "", http.StatusBadRequest},
		"report seq zero":           {"report", url.Values{"csrf_token": {csrf}, "seq": {"0"}}, "", http.StatusBadRequest},
		"report unknown seq":        {"report", url.Values{"csrf_token": {csrf}, "seq": {"999"}}, "", http.StatusBadRequest},
		"report own message":        {"report", url.Values{"csrf_token": {csrf}, "seq": {fmt.Sprint(own)}}, "", http.StatusBadRequest},
		"report odd block value":    {"report", url.Values{"csrf_token": {csrf}, "seq": {fmt.Sprint(theirs)}, "block": {"maybe"}}, "", http.StatusBadRequest},
		"report duplicate block":    {"report", url.Values{"csrf_token": {csrf}, "seq": {fmt.Sprint(theirs)}, "block": {"yes", "yes"}}, "", http.StatusBadRequest},
		"report oversized":          {"report", url.Values{"csrf_token": {csrf}, "seq": {fmt.Sprint(theirs)}, "pad": {strings.Repeat("x", 8192)}}, "", http.StatusBadRequest},
		"block wrong content type":  {"block", block(), "text/plain", http.StatusUnsupportedMediaType},
		"block missing csrf":        {"block", url.Values{"confirm": {"yes"}}, "", http.StatusBadRequest},
		"block foreign csrf":        {"block", url.Values{"csrf_token": {beaCSRF}, "confirm": {"yes"}}, "", http.StatusForbidden},
		"block confirm not yes":     {"block", url.Values{"csrf_token": {csrf}, "confirm": {"on"}}, "", http.StatusBadRequest},
		"block duplicate confirm":   {"block", url.Values{"csrf_token": {csrf}, "confirm": {"yes", "yes"}}, "", http.StatusBadRequest},
	}
	for name, c := range cases {
		if w := sw.act(sw.alex, c.action, c.form, c.contentType); w.Code != c.want {
			t.Errorf("%s: got %d, want %d", name, w.Code, c.want)
		}
	}
	if !sw.contacts.Allowed(alexParty, beaParty) || len(sw.reportsOf(t, "guardian-a", alexParty)) != 0 {
		t.Fatal("a rejected submission had effects")
	}
	if w := sw.get("/child/chat/"+sw.chat+"/report", sw.alex); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET on an action: %d", w.Code)
	}
}

func TestBlockingWorksWithoutAReportStore(t *testing.T) {
	sw := newSafetyWorld(t, false)
	seq := sw.say(t, beaParty, "ohne Meldungen")
	page := sw.safety(sw.alex, "").Body.String()
	if strings.Contains(page, "/report") || strings.Contains(page, `name="seq"`) || strings.Contains(page, "melden") || !strings.Contains(page, "/block") {
		t.Fatal("page without report store is wrong")
	}
	if !strings.Contains(sw.page(sw.alex).Body.String(), `aria-label="Blockieren"`) {
		t.Fatal("chat link must not promise reporting without a report store")
	}
	csrf := sw.token(t, sw.alex)
	if w := sw.act(sw.alex, "report", url.Values{"csrf_token": {csrf}, "seq": {fmt.Sprint(seq)}}, ""); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("report route without report store: %d", w.Code)
	}
	if w := sw.act(sw.alex, "block", url.Values{"csrf_token": {csrf}, "confirm": {"yes"}}, ""); w.Code != http.StatusSeeOther || sw.contacts.Allowed(alexParty, beaParty) {
		t.Fatalf("block without report store: %d", w.Code)
	}
}

func TestReportsNeedMessaging(t *testing.T) {
	sw := newSafetyWorld(t, true)
	if _, err := New(sw.sessions, sw.families, Options{Contacts: sw.contacts, Reports: sw.reports}); err == nil {
		t.Fatal("reports enabled without messaging")
	}
}

func TestChildIsToldAboutTheEmailOnlyWhenOneIsSent(t *testing.T) {
	sw := newSafetyWorld(t, true)
	sw.say(t, beaParty, "irgendwas")
	quiet := sw.safety(sw.alex, "").Body.String()
	if strings.Contains(quiet, "E-Mail") {
		t.Fatal("page promises an e-mail although none is configured")
	}
	h, err := New(sw.sessions, sw.families, Options{Contacts: sw.contacts, Looks: sw.looks, Messages: sw.store, Reports: sw.reports, ReportNotices: true})
	if err != nil {
		t.Fatal(err)
	}
	sw.handler = h
	loud := sw.safety(sw.alex, "").Body.String()
	for _, want := range []string{"Yapuno schreibt deinen Eltern eine E-Mail", "erinnert sie", "steht nicht in der E-Mail", "Sag es ihnen am besten auch selbst"} {
		if !strings.Contains(loud, want) {
			t.Errorf("missing %q", want)
		}
	}
	// A notice without a report store makes no sense.
	if _, err := New(sw.sessions, sw.families, Options{Contacts: sw.contacts, Messages: sw.store, ReportNotices: true}); err == nil {
		t.Fatal("report notices enabled without reports")
	}
}

const seenText = "Deine Eltern haben die Meldung gesehen"

func TestChildSeesOnlyOnTheSafetyPageThatGuardiansHaveSeenTheReport(t *testing.T) {
	sw := newSafetyWorld(t, true)
	sw.say(t, alexParty, "eigene")
	bad := sw.say(t, beaParty, "Das war gemein")
	other := sw.say(t, beaParty, "bleibt privat")
	csrf := sw.token(t, sw.alex)
	if w := sw.act(sw.alex, "report", url.Values{"csrf_token": {csrf}, "seq": {fmt.Sprint(bad)}}, ""); w.Code != http.StatusSeeOther {
		t.Fatalf("report: %d", w.Code)
	}
	before := sw.safety(sw.alex, "").Body.String()
	if !strings.Contains(before, "Schon gemeldet") || strings.Contains(before, seenText) {
		t.Fatal("page claims the guardians looked before they did")
	}
	if err := sw.reports.MarkSeen("guardian-a", "tenant-a", "child-a"); err != nil {
		t.Fatal(err)
	}
	after := sw.safety(sw.alex, "").Body.String()
	if strings.Count(after, seenText) != 1 || strings.Contains(after, "Schon gemeldet") {
		t.Fatal("page does not show that the guardians have seen the report")
	}
	if strings.Contains(after, fmt.Sprintf(`name="seq" value="%d"`, bad)) || !strings.Contains(after, fmt.Sprintf(`name="seq" value="%d"`, other)) {
		t.Fatal("seen report selectable again, or another message not selectable")
	}
	// Nowhere else: somebody looking at the start page or the chat learns nothing.
	for _, path := range []string{"/child", "/child/chat/" + sw.chat} {
		if body := sw.get(path, sw.alex).Body.String(); strings.Contains(body, "gesehen") || strings.Contains(strings.ToLower(body), "gemeldet") {
			t.Errorf("%s shows report feedback", path)
		}
	}
	// And never to the other child.
	for _, path := range []string{"/child", "/child/chat/" + sw.chat, "/child/chat/" + sw.chat + "/safety"} {
		if body := sw.get(path, sw.bea).Body.String(); strings.Contains(body, seenText) || strings.Contains(body, "Schon gemeldet") {
			t.Errorf("%s tells the reported child", path)
		}
	}
	// The guardian dismisses the report: the child keeps the feedback and cannot report the message again.
	list := sw.reportsOf(t, "guardian-a", alexParty)
	if err := sw.reports.Dismiss("guardian-a", "tenant-a", "child-a", list[0].ID); err != nil {
		t.Fatal(err)
	}
	dismissed := sw.safety(sw.alex, "").Body.String()
	if strings.Count(dismissed, seenText) != 1 || strings.Contains(dismissed, fmt.Sprintf(`name="seq" value="%d"`, bad)) {
		t.Fatal("feedback lost after the guardian dismissed the report")
	}
	// A stale or hand-made request for that message is not confirmed as a new report.
	w := sw.act(sw.alex, "report", url.Values{"csrf_token": {csrf}, "seq": {fmt.Sprint(bad)}}, "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/child/chat/"+sw.chat+"/safety" {
		t.Fatalf("repeat after dismissal: %d %q", w.Code, w.Header().Get("Location"))
	}
	if len(sw.reportsOf(t, "guardian-a", alexParty)) != 0 {
		t.Fatal("a dismissed report was reopened")
	}
	// With the block ticked, the child is blocked and told only that.
	w = sw.act(sw.alex, "report", url.Values{"csrf_token": {csrf}, "seq": {fmt.Sprint(bad)}, "block": {"yes"}}, "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/child?done=blocked" {
		t.Fatalf("repeat with block: %d %q", w.Code, w.Header().Get("Location"))
	}
	if sw.contacts.Allowed(alexParty, beaParty) {
		t.Fatal("not blocked although asked")
	}
}

func TestRepeatedOpenReportIsNotConfirmedTwice(t *testing.T) {
	sw := newSafetyWorld(t, true)
	bad := sw.say(t, beaParty, "einmal reicht")
	csrf := sw.token(t, sw.alex)
	form := url.Values{"csrf_token": {csrf}, "seq": {fmt.Sprint(bad)}}
	if w := sw.act(sw.alex, "report", form, ""); w.Header().Get("Location") != "/child/chat/"+sw.chat+"/safety?done=reported" {
		t.Fatalf("first report: %q", w.Header().Get("Location"))
	}
	if w := sw.act(sw.alex, "report", form, ""); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/child/chat/"+sw.chat+"/safety" {
		t.Fatalf("second report: %d %q", w.Code, w.Header().Get("Location"))
	}
	if len(sw.reportsOf(t, "guardian-a", alexParty)) != 1 {
		t.Fatal("duplicate report stored")
	}
}

func TestReportedMessageStaysVisibleWhenNewerMessagesFillTheList(t *testing.T) {
	old := reportChoices
	reportChoices = 2
	defer func() { reportChoices = old }()
	sw := newSafetyWorld(t, true)
	first := sw.say(t, beaParty, "alte gemeldete Nachricht")
	unreported := sw.say(t, beaParty, "alte, nicht gemeldete")
	csrf := sw.token(t, sw.alex)
	if w := sw.act(sw.alex, "report", url.Values{"csrf_token": {csrf}, "seq": {fmt.Sprint(first)}}, ""); w.Code != http.StatusSeeOther {
		t.Fatalf("report: %d", w.Code)
	}
	sw.say(t, beaParty, "neuer eins")
	sw.say(t, beaParty, "neuer zwei")
	if err := sw.reports.MarkSeen("guardian-a", "tenant-a", "child-a"); err != nil {
		t.Fatal(err)
	}
	page := sw.safety(sw.alex, "").Body.String()
	if !strings.Contains(page, "alte gemeldete Nachricht") || strings.Count(page, seenText) != 1 {
		t.Fatal("feedback for an older reported message is hidden")
	}
	if strings.Contains(page, "alte, nicht gemeldete") || strings.Contains(page, fmt.Sprintf(`name="seq" value="%d"`, unreported)) {
		t.Fatal("list grew beyond its limit for unreported messages")
	}
	if !strings.Contains(page, "neuer eins") || !strings.Contains(page, "neuer zwei") {
		t.Fatal("newest messages missing")
	}
}
