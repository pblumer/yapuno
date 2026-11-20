package familyweb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/guardianlock"
	"github.com/pblumer/yapuno/internal/skin"
)

// securityFakes record every call in order, with the identity it was made for.
type securityFakes struct {
	calls    []string
	count    int
	report   guardianlock.Report
	lockErr  error
	deviceOK func(child string) error
}

func (f *securityFakes) Count(g, t string) int { return f.count }
func (f *securityFakes) Revoke(*http.Request)  { f.calls = append(f.calls, "revoke-this") }
func (f *securityFakes) RevokeAll(g, t string) int {
	f.calls = append(f.calls, "revoke-all "+g+"/"+t)
	return f.count
}
func (f *securityFakes) Clear(w http.ResponseWriter) {
	f.calls = append(f.calls, "clear-cookie")
	w.Header().Add("Set-Cookie", "cleared=1")
}
func (f *securityFakes) Cancel(g, t string) int {
	f.calls = append(f.calls, "cancel-links "+g+"/"+t)
	return 0
}
func (f *securityFakes) Lock(_ context.Context, g, t string) (guardianlock.Report, error) {
	f.calls = append(f.calls, "lock "+g+"/"+t)
	return f.report, f.lockErr
}
func (f *securityFakes) RevokeAllDevices(g, t, child string) (int, error) {
	f.calls = append(f.calls, "devices "+g+"/"+t+"/"+child)
	if f.deviceOK != nil {
		if err := f.deviceOK(child); err != nil {
			return 0, err
		}
	}
	return 2, nil
}

type fakeDeviceLocker struct{ f *securityFakes }

func (d fakeDeviceLocker) RevokeAll(g, t, child string) (int, error) {
	return d.f.RevokeAllDevices(g, t, child)
}

type securityWorld struct {
	*contactWorld
	fakes *securityFakes
}

func newSecurityWorld(t *testing.T) *securityWorld {
	t.Helper()
	cw := newContactWorld(t)
	// Alex has a sibling; guardian A manages both.
	grant := authz.GuardianGrant{GuardianID: "guardian-a", ChildID: "child-a2", TenantID: "tenant-a", Active: true, CanManageDevices: true, CanApproveContacts: true}
	if err := cw.families.Bootstrap(familydashboard.Child{ID: "child-a2", TenantID: "tenant-a", DisplayName: "Ada"}, grant); err != nil {
		t.Fatal(err)
	}
	return &securityWorld{contactWorld: cw, fakes: &securityFakes{count: 1}}
}

func (sw *securityWorld) pages(t *testing.T, auth testAuth, withLock, withDevices bool) http.Handler {
	t.Helper()
	opt := SecurityOptions{Looks: sw.looks}
	if withLock {
		opt.Locker = sw.fakes
	}
	if withDevices {
		opt.Devices = fakeDeviceLocker{sw.fakes}
	}
	h, err := NewSecurityPages(auth, sw.fakes, sw.fakes, sw.families, opt)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func securityGet(h http.Handler) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/security", nil))
	return w
}

func securityPost(h http.Handler, action string, form url.Values, contentType string) *httptest.ResponseRecorder {
	if contentType == "" {
		contentType = "application/x-www-form-urlencoded"
	}
	r := httptest.NewRequest(http.MethodPost, "/family/security/"+action, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func csrfOnly() url.Values { return url.Values{"csrf_token": {sessionA.CSRFToken}} }
func confirmed() url.Values {
	return url.Values{"csrf_token": {sessionA.CSRFToken}, "confirm": {"yes"}}
}
func joined(c []string) string { return strings.Join(c, " | ") }

func TestSecurityPagesRequireAuthentication(t *testing.T) {
	sw := newSecurityWorld(t)
	h := sw.pages(t, testAuth{err: errors.New("no session")}, true, true)
	if w := securityGet(h); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET: %d", w.Code)
	}
	for _, action := range []string{"logout", "logout-all", "lock"} {
		if w := securityPost(h, action, confirmed(), ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: %d", action, w.Code)
		}
	}
	if len(sw.fakes.calls) != 0 {
		t.Fatalf("unauthenticated request had effects: %v", sw.fakes.calls)
	}
}

func TestSecurityPageExplainsEveryOption(t *testing.T) {
	sw := newSecurityWorld(t)
	if err := sw.looks.Set(appearance.Subject{Kind: appearance.KindGuardian, TenantID: "tenant-a", ID: "guardian-a"},
		appearance.Preference{Skin: "night", Figure: appearance.Default().Figure}); err != nil {
		t.Fatal(err)
	}
	sw.fakes.count = 3
	w := securityGet(sw.pages(t, testAuth{session: sessionA}, true, true))
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("page: %d", w.Code)
	}
	for _, want := range []string{
		`<html lang="de">`, `data-yp-skin="night"`, "<h1>Sicherheit</h1>", `href="/family"`, "3 aktive Anmeldungen",
		`action="/family/security/logout"`, `action="/family/security/logout-all"`, `action="/family/security/lock"`,
		`name="csrf_token" value="` + sessionA.CSRFToken + `"`, `type="checkbox" name="confirm" value="yes" required`,
		`type="checkbox" name="devices" value="yes"`, "E-Mail-Postfach", "Betreiber", "Elternteile",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	// The optional device checkbox must never be required.
	if strings.Contains(body, `name="devices" value="yes" required`) {
		t.Error("device option is mandatory")
	}
	for _, absent := range []string{"<script", "guardian-a", "tenant-a", "guardian-b", "@"} {
		if strings.Contains(strings.Replace(body, "@font-face", "", -1), absent) {
			t.Errorf("page contains %q", absent)
		}
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "script-src") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: %v", w.Header())
	}
	style := body[strings.Index(body, "<style>"):strings.Index(body, "</style>")]
	tokens, err := skin.Declarations(skin.Catalog()["night"])
	if err != nil {
		t.Fatal(err)
	}
	if hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).FindAllString(strings.Replace(style, tokens, "", 1), -1); len(hex) != 0 {
		t.Fatalf("hard-coded colors: %v", hex)
	}
	if len(sw.fakes.calls) != 0 {
		t.Fatalf("viewing the page had effects: %v", sw.fakes.calls)
	}
	sw.fakes.count = 1
	one := securityGet(sw.pages(t, testAuth{session: sessionA}, true, true)).Body.String()
	if !strings.Contains(one, "1 aktive Anmeldung.") || strings.Contains(one, "1 aktive Anmeldungen") {
		t.Error("singular wording wrong")
	}
}

func TestSecurityPageOffersOnlyWhatIsConfigured(t *testing.T) {
	sw := newSecurityWorld(t)
	noLock := securityGet(sw.pages(t, testAuth{session: sessionA}, false, true)).Body.String()
	if strings.Contains(noLock, "/family/security/lock") || strings.Contains(noLock, `name="devices"`) || !strings.Contains(noLock, "nicht eingerichtet") {
		t.Error("lock offered or not explained without a lock store")
	}
	if !strings.Contains(noLock, `action="/family/security/logout-all"`) {
		t.Error("sign-out everywhere must not depend on the lock store")
	}
	if w := securityPost(sw.pages(t, testAuth{session: sessionA}, false, true), "lock", confirmed(), ""); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Errorf("lock route without lock store: %d", w.Code)
	}
	noDevices := securityGet(sw.pages(t, testAuth{session: sessionA}, true, false)).Body.String()
	if !strings.Contains(noDevices, "/family/security/lock") || strings.Contains(noDevices, `name="devices"`) {
		t.Error("device option offered without device store")
	}
	if len(sw.fakes.calls) != 0 {
		t.Fatalf("effects: %v", sw.fakes.calls)
	}
}

func TestSignOutEndsOnlyThisSession(t *testing.T) {
	sw := newSecurityWorld(t)
	w := securityPost(sw.pages(t, testAuth{session: sessionA}, true, true), "logout", csrfOnly(), "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?notice=signed-out" {
		t.Fatalf("logout: %d %q", w.Code, w.Header().Get("Location"))
	}
	if got := joined(sw.fakes.calls); got != "revoke-this | clear-cookie" {
		t.Fatalf("calls: %s", got)
	}
	if w.Header().Get("Set-Cookie") == "" {
		t.Fatal("cookie not cleared")
	}
}

func TestSignOutEverywhereEndsSessionsAndUnusedLinks(t *testing.T) {
	sw := newSecurityWorld(t)
	h := sw.pages(t, testAuth{session: sessionA}, true, true)
	form := confirmed()
	form.Set("guardian_id", "guardian-b")
	form.Set("tenant_id", "tenant-b")
	w := securityPost(h, "logout-all", form, "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?notice=signed-out-everywhere" {
		t.Fatalf("logout-all: %d %q", w.Code, w.Header().Get("Location"))
	}
	if got := joined(sw.fakes.calls); got != "revoke-all guardian-a/tenant-a | cancel-links guardian-a/tenant-a | clear-cookie" {
		t.Fatalf("calls: %s", got)
	}
}

func TestEmergencyLockLocksThenEndsEverythingAndReportsHonestly(t *testing.T) {
	cases := map[string]struct {
		report  guardianlock.Report
		want    string
		notWant string
	}{
		"sole guardian":    {guardianlock.Report{}, "kein anderer Elternteil", "E-Mail gesendet"},
		"all told":         {guardianlock.Report{CoGuardians: 2, Notified: 2}, "E-Mail gesendet", "selbst"},
		"some not told":    {guardianlock.Report{CoGuardians: 2, Notified: 1}, "Informiere sie bitte selbst", "kein anderer Elternteil"},
		"nobody told":      {guardianlock.Report{CoGuardians: 1, Notified: 0}, "Informiere ihn bitte selbst", "wurde eine E-Mail gesendet"},
		"one told":         {guardianlock.Report{CoGuardians: 1, Notified: 1}, "Dem anderen Elternteil deiner Kinder wurde", "selbst"},
		"race: was locked": {guardianlock.Report{CoGuardians: 1, AlreadyLocked: true}, "Dein Zugang ist gesperrt", "E-Mail gesendet"},
	}
	for name, c := range cases {
		sw := newSecurityWorld(t)
		sw.fakes.report = c.report
		form := confirmed()
		form.Set("guardian_id", "guardian-b")
		form.Set("tenant_id", "tenant-b")
		w := securityPost(sw.pages(t, testAuth{session: sessionA}, true, true), "lock", form, "")
		body := w.Body.String()
		if w.Code != http.StatusOK || !strings.Contains(body, "<h1>Dein Zugang ist gesperrt</h1>") {
			t.Fatalf("%s: %d", name, w.Code)
		}
		if got := joined(sw.fakes.calls); got != "lock guardian-a/tenant-a | revoke-all guardian-a/tenant-a | cancel-links guardian-a/tenant-a | clear-cookie" {
			t.Fatalf("%s: calls: %s", name, got)
		}
		if !strings.Contains(body, c.want) || strings.Contains(body, c.notWant) {
			t.Errorf("%s: wording wrong (want %q, not %q)", name, c.want, c.notWant)
		}
		for _, want := range []string{"Betreiber", "Passwort", `data-yp-skin="classic"`, `role="status"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q", name, want)
			}
		}
		// The confirmation is a dead end on purpose: no form, no session data.
		for _, absent := range []string{"<form", sessionA.CSRFToken, "<script", "Kindergerät"} {
			if strings.Contains(body, absent) {
				t.Errorf("%s: confirmation contains %q", name, absent)
			}
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Set-Cookie") == "" {
			t.Errorf("%s: headers: %v", name, w.Header())
		}
	}
}

func TestEmergencyLockCanAlsoLockTheChildrensDevicesFirst(t *testing.T) {
	sw := newSecurityWorld(t)
	form := confirmed()
	form.Set("devices", "yes")
	w := securityPost(sw.pages(t, testAuth{session: sessionA}, true, true), "lock", form, "")
	if w.Code != http.StatusOK {
		t.Fatalf("lock with devices: %d", w.Code)
	}
	// Devices of exactly this guardian's children, before the access lock.
	want := "devices guardian-a/tenant-a/child-a | devices guardian-a/tenant-a/child-a2 | lock guardian-a/tenant-a | revoke-all guardian-a/tenant-a | cancel-links guardian-a/tenant-a | clear-cookie"
	if got := joined(sw.fakes.calls); got != want {
		t.Fatalf("calls: %s", got)
	}
	if body := w.Body.String(); !strings.Contains(body, "4 Kindergeräte") {
		t.Error("confirmation does not report the locked child devices")
	}
}

func TestFailuresLeaveTheGuardianAbleToRetry(t *testing.T) {
	// A device store failure stops before the access lock.
	sw := newSecurityWorld(t)
	sw.fakes.deviceOK = func(child string) error {
		if child == "child-a2" {
			return errors.New("disk full")
		}
		return nil
	}
	form := confirmed()
	form.Set("devices", "yes")
	w := securityPost(sw.pages(t, testAuth{session: sessionA}, true, true), "lock", form, "")
	if w.Code != http.StatusInternalServerError || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("device failure: %d", w.Code)
	}
	if got := joined(sw.fakes.calls); got != "devices guardian-a/tenant-a/child-a | devices guardian-a/tenant-a/child-a2" {
		t.Fatalf("calls after device failure: %s", got)
	}
	// A lock store failure keeps the session, so the guardian can try again.
	sw = newSecurityWorld(t)
	sw.fakes.lockErr = guardianlock.ErrUnavailable
	w = securityPost(sw.pages(t, testAuth{session: sessionA}, true, true), "lock", confirmed(), "")
	if w.Code != http.StatusInternalServerError || w.Header().Get("Set-Cookie") != "" || strings.Contains(w.Body.String(), "gesperrt") {
		t.Fatalf("lock failure: %d", w.Code)
	}
	if got := joined(sw.fakes.calls); got != "lock guardian-a/tenant-a" {
		t.Fatalf("calls after lock failure: %s", got)
	}
}

func TestSecurityActionsRejectInvalidSubmissions(t *testing.T) {
	type tc struct {
		action      string
		form        func() url.Values
		contentType string
		want        int
	}
	withDevices := func(v ...string) func() url.Values {
		return func() url.Values { f := confirmed(); f["devices"] = v; return f }
	}
	cases := map[string]tc{
		"logout wrong content type":  {"logout", csrfOnly, "text/plain", http.StatusUnsupportedMediaType},
		"logout missing csrf":        {"logout", func() url.Values { return url.Values{} }, "", http.StatusBadRequest},
		"logout foreign csrf":        {"logout", func() url.Values { return url.Values{"csrf_token": {sessionB.CSRFToken}} }, "", http.StatusForbidden},
		"logout duplicate csrf":      {"logout", func() url.Values { return url.Values{"csrf_token": {sessionA.CSRFToken, sessionA.CSRFToken}} }, "", http.StatusBadRequest},
		"logout-all unconfirmed":     {"logout-all", csrfOnly, "", http.StatusBadRequest},
		"logout-all confirm not yes": {"logout-all", func() url.Values { f := confirmed(); f.Set("confirm", "on"); return f }, "", http.StatusBadRequest},
		"logout-all foreign csrf":    {"logout-all", func() url.Values { f := confirmed(); f.Set("csrf_token", sessionB.CSRFToken); return f }, "", http.StatusForbidden},
		"lock unconfirmed":           {"lock", csrfOnly, "", http.StatusBadRequest},
		"lock confirm not yes":       {"lock", func() url.Values { f := confirmed(); f.Set("confirm", "no"); return f }, "", http.StatusBadRequest},
		"lock duplicate confirm":     {"lock", func() url.Values { f := confirmed(); f.Add("confirm", "yes"); return f }, "", http.StatusBadRequest},
		"lock foreign csrf":          {"lock", func() url.Values { f := confirmed(); f.Set("csrf_token", sessionB.CSRFToken); return f }, "", http.StatusForbidden},
		"lock wrong content type":    {"lock", confirmed, "application/json", http.StatusUnsupportedMediaType},
		"lock devices odd value":     {"lock", withDevices("maybe"), "", http.StatusBadRequest},
		"lock devices duplicate":     {"lock", withDevices("yes", "yes"), "", http.StatusBadRequest},
		"lock oversized":             {"lock", func() url.Values { f := confirmed(); f.Set("padding", strings.Repeat("x", 8192)); return f }, "", http.StatusBadRequest},
		"get on action":              {"lock", nil, "", http.StatusMethodNotAllowed},
	}
	for name, c := range cases {
		sw := newSecurityWorld(t)
		h := sw.pages(t, testAuth{session: sessionA}, true, true)
		var w *httptest.ResponseRecorder
		if c.form == nil {
			w = httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/security/"+c.action, nil))
		} else {
			w = securityPost(h, c.action, c.form(), c.contentType)
		}
		if w.Code != c.want {
			t.Errorf("%s: got %d, want %d", name, w.Code, c.want)
		}
		if len(sw.fakes.calls) != 0 {
			t.Errorf("%s: rejected request had effects: %v", name, sw.fakes.calls)
		}
	}
	// Asking for child devices where they cannot be locked is refused, not ignored.
	sw := newSecurityWorld(t)
	if w := securityPost(sw.pages(t, testAuth{session: sessionA}, true, false), "lock", withDevices("yes")(), ""); w.Code != http.StatusBadRequest || len(sw.fakes.calls) != 0 {
		t.Fatalf("devices without device store: %d %v", w.Code, sw.fakes.calls)
	}
}

func TestSecurityPagesNeedTheirDependencies(t *testing.T) {
	sw := newSecurityWorld(t)
	auth := testAuth{session: sessionA}
	for _, build := range []func() (http.Handler, error){
		func() (http.Handler, error) {
			return NewSecurityPages(nil, sw.fakes, sw.fakes, sw.families, SecurityOptions{})
		},
		func() (http.Handler, error) {
			return NewSecurityPages(auth, nil, sw.fakes, sw.families, SecurityOptions{})
		},
		func() (http.Handler, error) {
			return NewSecurityPages(auth, sw.fakes, nil, sw.families, SecurityOptions{})
		},
		func() (http.Handler, error) {
			return NewSecurityPages(auth, sw.fakes, sw.fakes, nil, SecurityOptions{})
		},
	} {
		if _, err := build(); err == nil {
			t.Fatal("handler built without a required dependency")
		}
	}
	// Without a look store the classic skin is used.
	h, err := NewSecurityPages(auth, sw.fakes, sw.fakes, sw.families, SecurityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if w := securityGet(h); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `data-yp-skin="classic"`) {
		t.Fatalf("page without look store: %d", w.Code)
	}
}

func TestFamilyPageLinksToSecurityWhenEnabled(t *testing.T) {
	sw := newSecurityWorld(t)
	with, err := NewWithOptions(testAuth{session: sessionA}, sw.families, Options{SecurityPages: true})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	with.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family", nil))
	if !strings.Contains(w.Body.String(), `href="/family/security"`) {
		t.Fatal("no security link on the family page")
	}
	without, err := New(testAuth{session: sessionA}, sw.families)
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	without.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family", nil))
	if strings.Contains(w.Body.String(), "/family/security") {
		t.Fatal("security link without security pages")
	}
}

func TestEmergencyLockSkipsChildrenWhoseDevicesTheGuardianMayNotManage(t *testing.T) {
	sw := newSecurityWorld(t)
	// A legacy grant without device management for a third child.
	partial := authz.GuardianGrant{GuardianID: "guardian-a", ChildID: "child-a3", TenantID: "tenant-a", Active: true, CanApproveContacts: true}
	if err := sw.families.Bootstrap(familydashboard.Child{ID: "child-a3", TenantID: "tenant-a", DisplayName: "Ari"}, partial); err != nil {
		t.Fatal(err)
	}
	form := confirmed()
	form.Set("devices", "yes")
	if w := securityPost(sw.pages(t, testAuth{session: sessionA}, true, true), "lock", form, ""); w.Code != http.StatusOK {
		t.Fatalf("lock: %d", w.Code)
	}
	got := joined(sw.fakes.calls)
	if strings.Contains(got, "child-a3") || !strings.Contains(got, "devices guardian-a/tenant-a/child-a |") || !strings.Contains(got, "child-a2") {
		t.Fatalf("calls: %s", got)
	}
}
