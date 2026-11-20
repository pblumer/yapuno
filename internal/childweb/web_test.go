package childweb

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
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
	"github.com/pblumer/yapuno/internal/childsession"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/skin"
)

const origin = "https://app.example.org"

type world struct {
	handler  http.Handler
	families *familystore.Store
	pairing  *devicepairing.Store
	contacts *contacts.Store
	looks    *appearance.Store
	sessions *childsession.Manager
}

func newWorld(t *testing.T, withOptional bool) *world {
	t.Helper()
	dir := t.TempDir()
	w := &world{}
	var err error
	if w.families, err = familystore.Open(filepath.Join(dir, "families.json")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, tenant, guardian, name string }{
		{"child-a", "tenant-a", "guardian-a", "Alex"}, {"child-b", "tenant-b", "guardian-b", "Bea"}, {"child-c", "tenant-c", "guardian-c", "Cem"},
	} {
		grant := authz.GuardianGrant{GuardianID: c.guardian, ChildID: c.id, TenantID: c.tenant, Active: true, CanManageDevices: true, CanApproveContacts: true}
		if err := w.families.Bootstrap(familydashboard.Child{ID: c.id, TenantID: c.tenant, DisplayName: c.name}, grant); err != nil {
			t.Fatal(err)
		}
	}
	if w.pairing, err = devicepairing.Open(filepath.Join(dir, "pairing.json"), w.families, time.Now); err != nil {
		t.Fatal(err)
	}
	if w.sessions, err = childsession.New(w.pairing, w.families, time.Now, time.Hour); err != nil {
		t.Fatal(err)
	}
	opt := Options{}
	if withOptional {
		if w.contacts, err = contacts.Open(filepath.Join(dir, "contacts.json"), w.families, time.Now); err != nil {
			t.Fatal(err)
		}
		if w.looks, err = appearance.Open(filepath.Join(dir, "appearance.json")); err != nil {
			t.Fatal(err)
		}
		opt = Options{Contacts: w.contacts, Looks: w.looks}
	}
	if w.handler, err = New(w.sessions, w.families, opt); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *world) post(path string, payload any, mutate func(*http.Request)) *httptest.ResponseRecorder {
	body, _ := json.Marshal(payload)
	r := httptest.NewRequest(http.MethodPost, origin+path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	if mutate != nil {
		mutate(r)
	}
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	return rec
}

func (w *world) get(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, origin+path, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	return rec
}

func (w *world) challenge(t *testing.T, deviceID string) string {
	t.Helper()
	rec := w.post("/child/session/challenge", map[string]string{"device_id": deviceID}, nil)
	var out struct {
		Nonce string `json:"nonce"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Nonce == "" {
		t.Fatalf("challenge: %d %s", rec.Code, rec.Body.String())
	}
	return out.Nonce
}

func (w *world) login(t *testing.T, deviceID string, key *ecdsa.PrivateKey) *http.Cookie {
	t.Helper()
	nonce := w.challenge(t, deviceID)
	sig := base64.RawURLEncoding.EncodeToString(pairingtest.LoginProof(t, key, deviceID, nonce))
	rec := w.post("/child/session/login", map[string]string{"device_id": deviceID, "nonce": nonce, "signature": sig}, nil)
	if rec.Code != http.StatusNoContent || len(rec.Result().Cookies()) != 1 {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	return rec.Result().Cookies()[0]
}

func (w *world) connect(t *testing.T) {
	t.Helper()
	code, err := w.contacts.Invite("guardian-a", "tenant-a", "child-a")
	if err != nil {
		t.Fatal(err)
	}
	id, err := w.contacts.Redeem("guardian-b", "tenant-b", "child-b", code)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.contacts.Confirm("guardian-a", "tenant-a", "child-a", id); err != nil {
		t.Fatal(err)
	}
}

func TestSignedOutVisitorGetsOnlyTheLoginShell(t *testing.T) {
	w := newWorld(t, true)
	rec := w.get("/child", nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `<script src="/child/app.js" defer></script>`) || !strings.Contains(body, `role="status"`) || !strings.Contains(body, "<noscript>") {
		t.Fatalf("shell: %d", rec.Code)
	}
	for _, leak := range []string{"Alex", "Bea", "Cem", "child-a", "tenant-a", "csrf_token"} {
		if strings.Contains(body, leak) {
			t.Fatalf("shell leaks %q", leak)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "connect-src 'self'") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: %v", rec.Header())
	}
	for _, asset := range []string{"/child/app.js", "/child/sig-utils.js"} {
		js := w.get(asset, nil)
		if js.Code != http.StatusOK || !strings.HasPrefix(js.Header().Get("Content-Type"), "text/javascript") || js.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s: %d %v", asset, js.Code, js.Header())
		}
	}
	if w.get("/child/unknown", nil).Code != http.StatusNotFound {
		t.Fatal("unknown child path served")
	}
}

func TestLoginShowsOnlyTheChildsOwnApprovedCrew(t *testing.T) {
	w := newWorld(t, true)
	w.connect(t)
	// A pending request from a third family must not appear.
	code, err := w.contacts.Invite("guardian-a", "tenant-a", "child-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.contacts.Redeem("guardian-c", "tenant-c", "child-c", code); err != nil {
		t.Fatal(err)
	}
	if err := w.looks.Set(appearance.Subject{Kind: appearance.KindChild, TenantID: "tenant-a", ID: "child-a"},
		appearance.Preference{Skin: "vivid", Figure: appearance.Figure{Shape: "ghost", Color: "peach"}}); err != nil {
		t.Fatal(err)
	}
	deviceID, key := pairingtest.Pair(t, w.pairing, "guardian-a", "tenant-a", "child-a")
	cookie := w.login(t, deviceID, key)
	rec := w.get("/child", cookie)
	body := rec.Body.String()
	for _, want := range []string{`<html lang="de">`, `data-yp-skin="vivid"`, "<h1>Hoi Alex!</h1>", "Bea", "beide Familien", `action="/child/session/logout"`, `name="csrf_token"`, "<svg"} {
		if !strings.Contains(body, want) {
			t.Errorf("home page misses %q", want)
		}
	}
	for _, leak := range []string{"Cem", "tenant-b", "child-b", "guardian-", deviceID, cookie.Value, "<script"} {
		if strings.Contains(body, leak) {
			t.Errorf("home page contains %q", leak)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); strings.Contains(csp, "script-src") || !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("home page CSP allows scripts: %q", csp)
	}
	start, end := strings.Index(body, "<style>"), strings.Index(body, "</style>")
	style := body[start:end]
	tokens, err := skin.CSSVariables(skin.Catalog()["vivid"])
	if err != nil {
		t.Fatal(err)
	}
	rest := strings.Replace(style, strings.TrimSuffix(strings.TrimPrefix(tokens, ":root{"), "}"), "", 1)
	if hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).FindAllString(rest, -1); len(hex) != 0 {
		t.Fatalf("hard-coded colors: %v", hex)
	}
	// Bea's device sees Alex, and nothing of Alex's pending request.
	beaDevice, beaKey := pairingtest.Pair(t, w.pairing, "guardian-b", "tenant-b", "child-b")
	beaPage := w.get("/child", w.login(t, beaDevice, beaKey)).Body.String()
	if !strings.Contains(beaPage, "<h1>Hoi Bea!</h1>") || !strings.Contains(beaPage, "Alex") || strings.Contains(beaPage, "Cem") {
		t.Fatal("second child sees the wrong crew")
	}
}

func TestHomeWithoutOptionalStoresAndWithoutContacts(t *testing.T) {
	for _, optional := range []bool{false, true} {
		w := newWorld(t, optional)
		deviceID, key := pairingtest.Pair(t, w.pairing, "guardian-a", "tenant-a", "child-a")
		body := w.get("/child", w.login(t, deviceID, key)).Body.String()
		if !strings.Contains(body, "<h1>Hoi Alex!</h1>") || !strings.Contains(body, `data-yp-skin="classic"`) || !strings.Contains(body, "Noch niemand") {
			t.Fatalf("optional=%v: unexpected home page", optional)
		}
	}
}

func TestLoginFailuresAreUniformAndSetNoCookie(t *testing.T) {
	w := newWorld(t, false)
	deviceID, key := pairingtest.Pair(t, w.pairing, "guardian-a", "tenant-a", "child-a")
	_, otherKey := pairingtest.Pair(t, w.pairing, "guardian-b", "tenant-b", "child-b")
	attempt := func(id string, signer *ecdsa.PrivateKey) *httptest.ResponseRecorder {
		nonce := w.challenge(t, id)
		sig := base64.RawURLEncoding.EncodeToString(pairingtest.LoginProof(t, signer, id, nonce))
		return w.post("/child/session/login", map[string]string{"device_id": id, "nonce": nonce, "signature": sig}, nil)
	}
	wrongKey := attempt(deviceID, otherKey)
	unknown := attempt(strings.Repeat("A", len(deviceID)), key)
	for name, rec := range map[string]*httptest.ResponseRecorder{"wrong key": wrongKey, "unknown device": unknown} {
		if rec.Code != http.StatusUnauthorized || len(rec.Result().Cookies()) != 0 {
			t.Fatalf("%s: %d", name, rec.Code)
		}
	}
	if wrongKey.Body.String() != unknown.Body.String() {
		t.Fatal("responses distinguish an unknown device from a wrong key")
	}
	if rec := w.post("/child/session/login", map[string]string{"device_id": deviceID, "nonce": w.challenge(t, deviceID), "signature": "!!!"}, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("malformed signature: %d", rec.Code)
	}
}

func TestSessionAPIRejectsCrossSiteAndMalformedRequests(t *testing.T) {
	w := newWorld(t, false)
	deviceID, _ := pairingtest.Pair(t, w.pairing, "guardian-a", "tenant-a", "child-a")
	valid := map[string]string{"device_id": deviceID}
	cases := map[string]struct {
		payload any
		mutate  func(*http.Request)
		want    int
	}{
		"no origin":         {valid, func(r *http.Request) { r.Header.Del("Origin") }, http.StatusForbidden},
		"cross origin":      {valid, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, http.StatusForbidden},
		"http origin":       {valid, func(r *http.Request) { r.Header.Set("Origin", "http://app.example.org") }, http.StatusForbidden},
		"form content type": {valid, func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }, http.StatusUnsupportedMediaType},
		"unknown field":     {map[string]string{"device_id": deviceID, "tenant_id": "tenant-b"}, nil, http.StatusBadRequest},
		"malformed device":  {map[string]string{"device_id": "../../x"}, nil, http.StatusBadRequest},
		"missing device":    {map[string]string{}, nil, http.StatusBadRequest},
		"oversized":         {map[string]string{"device_id": strings.Repeat("A", 8192)}, nil, http.StatusBadRequest},
		"not json":          {valid, func(r *http.Request) { r.Body = http.NoBody }, http.StatusBadRequest},
	}
	for name, c := range cases {
		for _, path := range []string{"/child/session/challenge", "/child/session/login"} {
			if rec := w.post(path, c.payload, c.mutate); rec.Code != c.want && !(path == "/child/session/login" && c.want == http.StatusBadRequest && rec.Code == http.StatusUnauthorized) {
				t.Errorf("%s %s: got %d, want %d", path, name, rec.Code, c.want)
			}
		}
	}
	trailing := httptest.NewRequest(http.MethodPost, origin+"/child/session/challenge", strings.NewReader(`{"device_id":"`+deviceID+`"}{"x":1}`))
	trailing.Header.Set("Content-Type", "application/json")
	trailing.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, trailing)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("trailing data: %d", rec.Code)
	}
	if rec := w.get("/child/session/challenge", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET on session API: %d", rec.Code)
	}
}

func TestLogoutNeedsTheSessionsOwnCSRFToken(t *testing.T) {
	w := newWorld(t, false)
	deviceID, key := pairingtest.Pair(t, w.pairing, "guardian-a", "tenant-a", "child-a")
	cookie := w.login(t, deviceID, key)
	other := w.login(t, deviceID, key)
	csrf := func(c *http.Cookie) string {
		m := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(w.get("/child", c).Body.String())
		if m == nil {
			t.Fatal("no CSRF token on home page")
		}
		return m[1]
	}
	logout := func(c *http.Cookie, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, origin+"/child/session/logout", strings.NewReader(url.Values{"csrf_token": {token}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if c != nil {
			r.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		w.handler.ServeHTTP(rec, r)
		return rec
	}
	if rec := logout(nil, csrf(cookie)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("logout without session: %d", rec.Code)
	}
	for name, token := range map[string]string{"empty": "", "forged": "forged", "other session's token": csrf(other)} {
		if rec := logout(cookie, token); rec.Code != http.StatusForbidden {
			t.Fatalf("%s: %d", name, rec.Code)
		}
	}
	if !strings.Contains(w.get("/child", cookie).Body.String(), "Hoi Alex") {
		t.Fatal("rejected logout ended the session")
	}
	rec := logout(cookie, csrf(cookie))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/child?signed_out=1" || len(rec.Result().Cookies()) != 1 || rec.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatalf("logout: %d %v", rec.Code, rec.Header())
	}
	if strings.Contains(w.get("/child", cookie).Body.String(), "Hoi Alex") {
		t.Fatal("session usable after logout")
	}
	if !strings.Contains(w.get("/child", other).Body.String(), "Hoi Alex") {
		t.Fatal("logout ended another session")
	}
}

func TestRevokedDeviceFallsBackToTheShellWithoutData(t *testing.T) {
	w := newWorld(t, true)
	w.connect(t)
	deviceID, key := pairingtest.Pair(t, w.pairing, "guardian-a", "tenant-a", "child-a")
	cookie := w.login(t, deviceID, key)
	if err := w.pairing.Revoke("guardian-a", "tenant-a", "child-a", deviceID); err != nil {
		t.Fatal(err)
	}
	body := w.get("/child", cookie).Body.String()
	if strings.Contains(body, "Alex") || strings.Contains(body, "Bea") || !strings.Contains(body, "/child/app.js") {
		t.Fatal("revoked device still sees child data")
	}
	nonce := w.challenge(t, deviceID)
	sig := base64.RawURLEncoding.EncodeToString(pairingtest.LoginProof(t, key, deviceID, nonce))
	if rec := w.post("/child/session/login", map[string]string{"device_id": deviceID, "nonce": nonce, "signature": sig}, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked device logged in: %d", rec.Code)
	}
}

func TestNewRequiresSessionsAndChildren(t *testing.T) {
	w := newWorld(t, false)
	if _, err := New(nil, w.families, Options{}); err == nil {
		t.Fatal("nil sessions accepted")
	}
	if _, err := New(w.sessions, nil, Options{}); err == nil {
		t.Fatal("nil children accepted")
	}
}
