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

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/skin"
)

type lostWorld struct {
	*contactWorld
	pairing *devicepairing.Store
}

func newLostWorld(t *testing.T) *lostWorld {
	t.Helper()
	cw := newContactWorld(t)
	pairing, err := devicepairing.Open(filepath.Join(t.TempDir(), "pairing.json"), cw.families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return &lostWorld{contactWorld: cw, pairing: pairing}
}

func (lw *lostWorld) pages(t *testing.T, auth testAuth) http.Handler {
	t.Helper()
	h, err := NewLostDevicePages(auth, lw.pairing, lw.families, lw.looks)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (lw *lostWorld) pair(t *testing.T, guardian, tenant, child string) string {
	t.Helper()
	id, _ := pairingtest.Pair(t, lw.pairing, guardian, tenant, child)
	return id
}

func (lw *lostWorld) active(tenant, child, id string) bool {
	_, ok := lw.pairing.ActiveDevice(tenant, child, id)
	return ok
}

func lostGet(h http.Handler, query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/devices/lost"+query, nil))
	return w
}

func lostPost(h http.Handler, form url.Values, contentType string) *httptest.ResponseRecorder {
	if contentType == "" {
		contentType = "application/x-www-form-urlencoded"
	}
	r := httptest.NewRequest(http.MethodPost, "/family/devices/lost", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func lockOne(id string) url.Values {
	return url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}, "scope": {"one"}, "device_id": {id}, "confirm": {"yes"}}
}

func lockAll() url.Values {
	return url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}, "scope": {"all"}, "confirm": {"yes"}}
}

func TestLostDevicePagesRequireAuthentication(t *testing.T) {
	lw := newLostWorld(t)
	id := lw.pair(t, "guardian-a", "tenant-a", "child-a")
	h := lw.pages(t, testAuth{err: errors.New("no session")})
	if w := lostGet(h, "?child_id=child-a"); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET: %d", w.Code)
	}
	for _, form := range []url.Values{lockOne(id), lockAll()} {
		if w := lostPost(h, form, ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("POST: %d", w.Code)
		}
	}
	if !lw.active("tenant-a", "child-a", id) {
		t.Fatal("unauthenticated request locked a device")
	}
}

func TestLostDevicePageListsActiveDevicesOfThatChildOnly(t *testing.T) {
	lw := newLostWorld(t)
	first := lw.pair(t, "guardian-a", "tenant-a", "child-a")
	second := lw.pair(t, "guardian-a", "tenant-a", "child-a")
	foreign := lw.pair(t, "guardian-b", "tenant-b", "child-b")
	if err := lw.pairing.Revoke("guardian-a", "tenant-a", "child-a", second); err != nil {
		t.Fatal(err)
	}
	if err := lw.looks.Set(appearance.Subject{Kind: appearance.KindGuardian, TenantID: "tenant-a", ID: "guardian-a"},
		appearance.Preference{Skin: "night", Figure: appearance.Default().Figure}); err != nil {
		t.Fatal(err)
	}
	w := lostGet(lw.pages(t, testAuth{session: sessionA}), "?child_id=child-a")
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("page: %d", w.Code)
	}
	for _, want := range []string{
		`<html lang="de">`, `data-yp-skin="night"`, "<h1>Gerät von Alex verloren?</h1>", `href="/family"`, "Test phone",
		`name="csrf_token" value="` + sessionA.CSRFToken + `"`, `name="device_id" value="` + first + `"`,
		`name="scope" value="one"`, `name="scope" value="all"`, `type="checkbox" name="confirm" value="yes" required`,
		"Kopplungs-Code", "offline",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, absent := range []string{second, foreign, "tenant-b", "child-b", "Bea", "<script"} {
		if strings.Contains(body, absent) {
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
	// No devices: the page says so and offers no lock form.
	empty := lostGet(lw.pages(t, testAuth{session: sessionC}), "?child_id=child-c").Body.String()
	if !strings.Contains(empty, "kein Gerät verbunden") || strings.Contains(empty, `name="scope"`) {
		t.Fatal("empty state wrong")
	}
}

func TestGuardianLocksOneOrAllDevices(t *testing.T) {
	lw := newLostWorld(t)
	h := lw.pages(t, testAuth{session: sessionA})
	first := lw.pair(t, "guardian-a", "tenant-a", "child-a")
	second := lw.pair(t, "guardian-a", "tenant-a", "child-a")
	third := lw.pair(t, "guardian-a", "tenant-a", "child-a")
	foreign := lw.pair(t, "guardian-b", "tenant-b", "child-b")
	w := lostPost(h, lockOne(first), "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family/devices/lost?child_id=child-a&done=one" {
		t.Fatalf("lock one: %d %q", w.Code, w.Header().Get("Location"))
	}
	if lw.active("tenant-a", "child-a", first) || !lw.active("tenant-a", "child-a", second) || !lw.active("tenant-a", "child-a", third) {
		t.Fatal("exactly the chosen device must be locked")
	}
	if done := lostGet(h, "?child_id=child-a&done=one").Body.String(); !strings.Contains(done, `role="status"`) || !strings.Contains(done, "gesperrt") {
		t.Fatal("no confirmation after locking")
	}
	w = lostPost(h, lockAll(), "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family/devices/lost?child_id=child-a&done=all" {
		t.Fatalf("lock all: %d %q", w.Code, w.Header().Get("Location"))
	}
	if lw.active("tenant-a", "child-a", second) || lw.active("tenant-a", "child-a", third) {
		t.Fatal("devices still active after locking all")
	}
	if !lw.active("tenant-b", "child-b", foreign) {
		t.Fatal("another family's device was locked")
	}
	after := lostGet(h, "?child_id=child-a&done=all").Body.String()
	if !strings.Contains(after, `role="status"`) || !strings.Contains(after, "kein Gerät verbunden") || strings.Contains(after, `name="scope"`) {
		t.Fatal("page after locking all is wrong")
	}
	// Arbitrary text in the query is never shown.
	if forged := lostGet(h, "?child_id=child-a&done=%3Cb%3Ehacked").Body.String(); strings.Contains(forged, "hacked") || strings.Contains(forged, `role="status"`) {
		t.Fatal("arbitrary notice rendered")
	}
}

func TestLostDeviceUsesSessionIdentityAndChildScope(t *testing.T) {
	lw := newLostWorld(t)
	own := lw.pair(t, "guardian-a", "tenant-a", "child-a")
	foreign := lw.pair(t, "guardian-b", "tenant-b", "child-b")
	intruder := lw.pages(t, testAuth{session: sessionC})
	if w := lostGet(intruder, "?child_id=child-a"); w.Code != http.StatusForbidden {
		t.Fatalf("foreign page: %d", w.Code)
	}
	for name, form := range map[string]url.Values{
		"lock one of another family": {"child_id": {"child-a"}, "scope": {"one"}, "device_id": {own}, "confirm": {"yes"}},
		"lock all of another family": {"child_id": {"child-a"}, "scope": {"all"}, "confirm": {"yes"}},
	} {
		form.Set("csrf_token", sessionC.CSRFToken)
		form.Set("guardian_id", "guardian-a")
		form.Set("tenant_id", "tenant-a")
		if w := lostPost(intruder, form, ""); w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	// A guardian cannot lock another family's device by naming it under their own child.
	h := lw.pages(t, testAuth{session: sessionA})
	if w := lostPost(h, lockOne(foreign), ""); w.Code != http.StatusBadRequest {
		t.Fatalf("foreign device under own child: %d", w.Code)
	}
	if !lw.active("tenant-a", "child-a", own) || !lw.active("tenant-b", "child-b", foreign) {
		t.Fatal("a rejected request locked a device")
	}
}

func TestLostDeviceRejectsInvalidSubmissions(t *testing.T) {
	lw := newLostWorld(t)
	h := lw.pages(t, testAuth{session: sessionA})
	id := lw.pair(t, "guardian-a", "tenant-a", "child-a")
	cases := map[string]struct {
		form        func() url.Values
		contentType string
		want        int
	}{
		"wrong content type":   {func() url.Values { return lockAll() }, "text/plain", http.StatusUnsupportedMediaType},
		"missing csrf":         {func() url.Values { f := lockAll(); f.Del("csrf_token"); return f }, "", http.StatusBadRequest},
		"wrong csrf":           {func() url.Values { f := lockAll(); f.Set("csrf_token", sessionB.CSRFToken); return f }, "", http.StatusForbidden},
		"no confirmation":      {func() url.Values { f := lockAll(); f.Del("confirm"); return f }, "", http.StatusBadRequest},
		"confirmation not yes": {func() url.Values { f := lockOne(id); f.Set("confirm", "on"); return f }, "", http.StatusBadRequest},
		"missing scope":        {func() url.Values { f := lockAll(); f.Del("scope"); return f }, "", http.StatusBadRequest},
		"unknown scope":        {func() url.Values { f := lockAll(); f.Set("scope", "everything"); return f }, "", http.StatusBadRequest},
		"duplicate scope":      {func() url.Values { f := lockOne(id); f.Add("scope", "all"); return f }, "", http.StatusBadRequest},
		"one without device":   {func() url.Values { f := lockOne(id); f.Del("device_id"); return f }, "", http.StatusBadRequest},
		"all with a device":    {func() url.Values { f := lockAll(); f.Set("device_id", id); return f }, "", http.StatusBadRequest},
		"malformed device":     {func() url.Values { return lockOne("../../etc/passwd") }, "", http.StatusBadRequest},
		"unknown device":       {func() url.Values { return lockOne(strings.Repeat("A", 22)) }, "", http.StatusBadRequest},
		"invalid child":        {func() url.Values { f := lockAll(); f.Set("child_id", "<script>"); return f }, "", http.StatusBadRequest},
		"oversized body":       {func() url.Values { f := lockAll(); f.Set("padding", strings.Repeat("x", 8192)); return f }, "", http.StatusBadRequest},
	}
	for name, c := range cases {
		if w := lostPost(h, c.form(), c.contentType); w.Code != c.want {
			t.Errorf("%s: got %d, want %d", name, w.Code, c.want)
		}
	}
	if !lw.active("tenant-a", "child-a", id) {
		t.Fatal("a rejected submission locked the device")
	}
	if w := lostGet(h, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("page without child: %d", w.Code)
	}
}

func TestFamilyPageLinksToLostDeviceWhenDevicePagesAreEnabled(t *testing.T) {
	lw := newLostWorld(t)
	with, err := NewWithOptions(testAuth{session: sessionA}, lw.families, Options{DevicePages: true})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	with.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family", nil))
	if !strings.Contains(w.Body.String(), `href="/family/devices/lost?child_id=child-a"`) {
		t.Fatal("no lost-device link on the family page")
	}
	without, err := New(testAuth{session: sessionA}, lw.families)
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	without.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family", nil))
	if strings.Contains(w.Body.String(), "/family/devices/lost") {
		t.Fatal("lost-device link without device pages")
	}
}

func TestDevicePageLinksToLostDevice(t *testing.T) {
	lw := newLostWorld(t)
	h, err := NewDevicePages(testAuth{session: sessionA}, lw.pairing)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/devices?child_id=child-a", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `href="/family/devices/lost?child_id=child-a"`) {
		t.Fatalf("no lost-device link on the device page: %d", w.Code)
	}
}

type recordingLostStore struct{ calls int }

func (s *recordingLostStore) ListDevices(string, string, string) ([]devicepairing.Device, error) {
	return nil, nil
}
func (s *recordingLostStore) Revoke(string, string, string, string) error { s.calls++; return nil }
func (s *recordingLostStore) RevokeAll(string, string, string) (int, error) {
	s.calls++
	return 0, nil
}

func TestMalformedLockRequestsNeverReachTheStore(t *testing.T) {
	lw := newLostWorld(t)
	store := &recordingLostStore{}
	h, err := NewLostDevicePages(testAuth{session: sessionA}, store, lw.families, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "short", strings.Repeat("A", 23), strings.Repeat("A", 21) + "/", "<script>alert(1)</script>"} {
		if w := lostPost(h, lockOne(id), ""); w.Code != http.StatusBadRequest {
			t.Errorf("device id %q: %d", id, w.Code)
		}
	}
	unconfirmed := lockAll()
	unconfirmed.Set("confirm", "no")
	if w := lostPost(h, unconfirmed, ""); w.Code != http.StatusBadRequest {
		t.Errorf("unconfirmed: %d", w.Code)
	}
	if store.calls != 0 {
		t.Fatalf("store reached %d times by malformed requests", store.calls)
	}
	if w := lostPost(h, lockOne(strings.Repeat("A", 22)), ""); w.Code != http.StatusSeeOther || store.calls != 1 {
		t.Fatalf("well-formed request: %d, calls %d", w.Code, store.calls)
	}
	for _, missing := range []func() (http.Handler, error){
		func() (http.Handler, error) { return NewLostDevicePages(nil, store, lw.families, nil) },
		func() (http.Handler, error) {
			return NewLostDevicePages(testAuth{session: sessionA}, nil, lw.families, nil)
		},
		func() (http.Handler, error) { return NewLostDevicePages(testAuth{session: sessionA}, store, nil, nil) },
	} {
		if _, err := missing(); err == nil {
			t.Fatal("handler built without a required dependency")
		}
	}
}

func TestLostDevicePageNamesAndListsExactlyTheRequestedSibling(t *testing.T) {
	lw := newLostWorld(t)
	grant := authz.GuardianGrant{GuardianID: "guardian-a", ChildID: "child-a2", TenantID: "tenant-a", Active: true, CanManageDevices: true, CanApproveContacts: true}
	if err := lw.families.Bootstrap(familydashboard.Child{ID: "child-a2", TenantID: "tenant-a", DisplayName: "Ada"}, grant); err != nil {
		t.Fatal(err)
	}
	alexDevice := lw.pair(t, "guardian-a", "tenant-a", "child-a")
	adaDevice := lw.pair(t, "guardian-a", "tenant-a", "child-a2")
	h := lw.pages(t, testAuth{session: sessionA})
	ada := lostGet(h, "?child_id=child-a2").Body.String()
	if !strings.Contains(ada, "<h1>Gerät von Ada verloren?</h1>") || !strings.Contains(ada, adaDevice) || strings.Contains(ada, alexDevice) || strings.Contains(ada, "Alex") {
		t.Fatal("sibling page shows the wrong child or device")
	}
	alex := lostGet(h, "?child_id=child-a").Body.String()
	if !strings.Contains(alex, "<h1>Gerät von Alex verloren?</h1>") || !strings.Contains(alex, alexDevice) || strings.Contains(alex, adaDevice) || strings.Contains(alex, "Ada") {
		t.Fatal("page shows the wrong child or device")
	}
	// Locking all of Ada's devices leaves Alex's device alone.
	form := lockAll()
	form.Set("child_id", "child-a2")
	if w := lostPost(h, form, ""); w.Code != http.StatusSeeOther {
		t.Fatalf("lock sibling: %d", w.Code)
	}
	if lw.active("tenant-a", "child-a2", adaDevice) || !lw.active("tenant-a", "child-a", alexDevice) {
		t.Fatal("locking one sibling must not affect the other")
	}
}

func TestLostDevicePageChecksTheChildEvenIfTheStoreWouldNot(t *testing.T) {
	lw := newLostWorld(t)
	h, err := NewLostDevicePages(testAuth{session: sessionA}, &recordingLostStore{}, lw.families, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range []string{"child-b", "child-unknown"} {
		if w := lostGet(h, "?child_id="+child); w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "verloren") {
			t.Fatalf("%s: page rendered for a child outside the session's family: %d", child, w.Code)
		}
	}
	if w := lostGet(h, "?child_id=child-a"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `data-yp-skin="classic"`) {
		t.Fatalf("own child without a look store: %d", w.Code)
	}
}
