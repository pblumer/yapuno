package familyweb

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
)

func (lw *lostWorld) devicePages(t *testing.T, auth testAuth) http.Handler {
	t.Helper()
	h, err := NewDevicePages(auth, lw.pairing, lw.families, "https://app.example.org", lw.looks)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func deviceGet(h http.Handler, query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/devices"+query, nil))
	return w
}

func devicePost(h http.Handler, action string, form url.Values, contentType string) *httptest.ResponseRecorder {
	if contentType == "" {
		contentType = "application/x-www-form-urlencoded"
	}
	r := httptest.NewRequest(http.MethodPost, "/family/devices/"+action, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// waitingDevice is a device that asked for a code and waits for a guardian.
func (lw *lostWorld) waitingDevice(t *testing.T, name string) (token, code string, key *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if token, code, err = lw.pairing.Request(name, der); err != nil {
		t.Fatal(err)
	}
	return token, code, key
}

func connect(code string) url.Values {
	return url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}, "code": {code}}
}

func TestDevicePagesRequireAuthenticationAndTheGuardiansOwnChild(t *testing.T) {
	lw := newLostWorld(t)
	anonymous := lw.devicePages(t, testAuth{err: errors.New("no session")})
	if w := deviceGet(anonymous, "?child_id=child-a"); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous view: %d", w.Code)
	}
	_, code, _ := lw.waitingDevice(t, "iPhone")
	if w := devicePost(anonymous, "connect", connect(code), ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous connect: %d", w.Code)
	}
	h := lw.devicePages(t, testAuth{session: sessionA})
	for _, query := range []string{"?child_id=child-b", "?child_id=nobody", "", "?child_id=child-a&child_id=child-b", "?child_id=%3Cb%3E"} {
		if w := deviceGet(h, query); w.Code != http.StatusForbidden && w.Code != http.StatusBadRequest {
			t.Errorf("view %q: %d", query, w.Code)
		}
	}
	// A guardian of another family cannot bind a device to this child.
	foreign := url.Values{"csrf_token": {sessionB.CSRFToken}, "child_id": {"child-a"}, "code": {code}}
	if w := devicePost(lw.devicePages(t, testAuth{session: sessionB}), "connect", foreign, ""); w.Code != http.StatusForbidden {
		t.Fatalf("foreign connect: %d", w.Code)
	}
	if devices, _ := lw.pairing.ListDevices("guardian-a", "tenant-a", "child-a"); len(devices) != 0 {
		t.Fatal("a device was bound")
	}
}

func TestTheDevicePageExplainsHowToConnectADevice(t *testing.T) {
	lw := newLostWorld(t)
	if err := lw.looks.Set(appearance.Subject{Kind: appearance.KindGuardian, TenantID: "tenant-a", ID: "guardian-a"},
		appearance.Preference{Skin: "night", Figure: appearance.Default().Figure}); err != nil {
		t.Fatal(err)
	}
	w := deviceGet(lw.devicePages(t, testAuth{session: sessionA}), "?child_id=child-a")
	body := w.Body.String()
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") ||
		strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src") {
		t.Fatalf("view: %d %v", w.Code, w.Header())
	}
	for _, want := range []string{`<html lang="de">`, `data-yp-skin="night"`, "<h1>Geräte von Alex</h1>", `href="/family"`,
		"app.example.org/pair", `action="/family/devices/connect"`, `name="code"`, `inputmode="numeric"`, `autocomplete="off"`,
		`name="csrf_token" value="` + sessionA.CSRFToken + `"`, `name="child_id" value="child-a"`, "Gerät verbinden",
		"selbst auf dem Gerät", "Noch kein Gerät verbunden", `href="/family/devices/lost?child_id=child-a"`, "synthetischen Profilen"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// Nothing of the old flow, and no script.
	for _, absent := range []string{"Fingerabdruck", "QR", "Pairing-Code", "<script", "https://app.example.org/pair"} {
		if strings.Contains(body, absent) {
			t.Errorf("page contains %q", absent)
		}
	}
	style := body[strings.Index(body, "<style>")+7 : strings.Index(body, "</style>")]
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).MatchString(style[strings.Index(style, "body{margin:0}"):]) {
		t.Error("component styles must use skin tokens only")
	}
}

func TestAGuardianConnectsTheDeviceWhoseCodeTheyEnter(t *testing.T) {
	lw := newLostWorld(t)
	h := lw.devicePages(t, testAuth{session: sessionA})
	token, code, key := lw.waitingDevice(t, "iPhone von Alex")
	otherToken, _, _ := lw.waitingDevice(t, "Fremdes Gerät")
	// Typed as people read it: with a space.
	w := devicePost(h, "connect", connect(code[:3]+" "+code[3:]), "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family/devices?child_id=child-a&done=connected" {
		t.Fatalf("connect: %d %q %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	// The device needs a moment to finish, so the page says so and reloads
	// itself once, without the notice.
	just := deviceGet(h, "?child_id=child-a&done=connected").Body.String()
	for _, want := range []string{`role="status"`, "Der Code stimmt", `<meta http-equiv="refresh" content="4;url=/family/devices?child_id=child-a">`} {
		if !strings.Contains(just, want) {
			t.Errorf("page right after the code lacks %q", want)
		}
	}
	if plain := deviceGet(h, "?child_id=child-a").Body.String(); strings.Contains(plain, "http-equiv") {
		t.Error("the plain page reloads itself")
	}
	if locked := deviceGet(h, "?child_id=child-a&done=locked").Body.String(); strings.Contains(locked, "http-equiv") {
		t.Error("the page reloads itself after a lock")
	}
	// The device finishes by itself and appears in the list.
	nonce, err := lw.pairing.Nonce(token)
	if err != nil {
		t.Fatal(err)
	}
	device, err := lw.pairing.Finish(token, pairingtest.Sign(t, key, "yapuno-pair-finish-v1\x00"+nonce))
	if err != nil || device.ChildID != "child-a" || device.TenantID != "tenant-a" {
		t.Fatalf("finish: %+v %v", device, err)
	}
	page := deviceGet(h, "?child_id=child-a&done=connected").Body.String()
	for _, want := range []string{`role="status"`, "iPhone von Alex", `name="device_id" value="` + device.ID + `"`, "Gerät sperren"} {
		if !strings.Contains(page, want) {
			t.Errorf("page after connecting lacks %q", want)
		}
	}
	if strings.Contains(page, "Fremdes Gerät") || strings.Contains(page, code) {
		t.Error("the page shows a waiting device or the code")
	}
	// The other device is still waiting and belongs to nobody.
	if _, err := lw.pairing.Nonce(otherToken); !errors.Is(err, devicepairing.ErrNotReady) {
		t.Fatalf("another waiting device was bound: %v", err)
	}
	// Only known notices are shown.
	if odd := deviceGet(h, "?child_id=child-a&done=%3Cb%3Ex").Body.String(); strings.Contains(odd, `role="status"`) {
		t.Error("query text selects a notice")
	}
}

func TestWrongCodesAreExplainedAndLimited(t *testing.T) {
	lw := newLostWorld(t)
	h := lw.devicePages(t, testAuth{session: sessionA})
	_, code, _ := lw.waitingDevice(t, "iPhone")
	// A typo fails the check digit and is explained as such.
	typo := code[:5] + string('0'+(code[5]-'0'+1)%10)
	w := devicePost(h, "connect", connect(typo), "")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `role="alert"`) || !strings.Contains(w.Body.String(), "vertippt") ||
		!strings.Contains(w.Body.String(), `action="/family/devices/connect"`) {
		t.Fatalf("typo: %d %s", w.Code, w.Body)
	}
	if w := devicePost(h, "connect", connect("<b>12</b>"), ""); w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "<b>12") {
		t.Fatalf("odd input: %d", w.Code)
	}
	// A well-formed code that no waiting device carries any more: another
	// family has just used it.
	_, unknown, _ := lw.waitingDevice(t, "Gerät einer anderen Familie")
	if _, err := lw.pairing.Approve("guardian-c", "tenant-c", "child-c", unknown); err != nil {
		t.Fatal(err)
	}
	w = devicePost(h, "connect", connect(unknown), "")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `role="alert"`) || !strings.Contains(w.Body.String(), "fünf Minuten") {
		t.Fatalf("unknown code: %d %s", w.Code, w.Body)
	}
	for i := 0; i < 10; i++ {
		w = devicePost(h, "connect", connect(unknown), "")
	}
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "zehn Minuten") {
		t.Fatalf("after many unknown codes: %d", w.Code)
	}
	// While limited, even the right code is refused, and nothing is bound.
	if w := devicePost(h, "connect", connect(code), ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("right code while limited: %d", w.Code)
	}
	if devices, _ := lw.pairing.ListDevices("guardian-a", "tenant-a", "child-a"); len(devices) != 0 {
		t.Fatal("a device was bound")
	}
}

func TestConnectRejectsInvalidSubmissions(t *testing.T) {
	lw := newLostWorld(t)
	h := lw.devicePages(t, testAuth{session: sessionA})
	_, code, _ := lw.waitingDevice(t, "iPhone")
	cases := []struct {
		name, contentType string
		form              url.Values
		want              int
	}{
		{"no csrf", "", url.Values{"child_id": {"child-a"}, "code": {code}}, http.StatusBadRequest},
		{"wrong csrf", "", url.Values{"csrf_token": {"wrong"}, "child_id": {"child-a"}, "code": {code}}, http.StatusForbidden},
		{"json", "application/json", connect(code), http.StatusUnsupportedMediaType},
		{"no code", "", url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}}, http.StatusBadRequest},
		{"two codes", "", url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}, "code": {code, code}}, http.StatusBadRequest},
		{"no child", "", url.Values{"csrf_token": {sessionA.CSRFToken}, "code": {code}}, http.StatusBadRequest},
		{"two children", "", url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a", "child-b"}, "code": {code}}, http.StatusBadRequest},
		{"foreign child", "", url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-b"}, "code": {code}}, http.StatusForbidden},
	}
	for _, c := range cases {
		if w := devicePost(h, "connect", c.form, c.contentType); w.Code != c.want {
			t.Errorf("%s: %d, want %d", c.name, w.Code, c.want)
		}
	}
	// None of them used the code up.
	if w := devicePost(h, "connect", connect(code), ""); w.Code != http.StatusSeeOther {
		t.Fatalf("the code after invalid submissions: %d", w.Code)
	}
}

func TestLockingADeviceNeedsConfirmation(t *testing.T) {
	lw := newLostWorld(t)
	h := lw.devicePages(t, testAuth{session: sessionA})
	id := lw.pair(t, "guardian-a", "tenant-a", "child-a")
	form := url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}, "device_id": {id}, "confirm": {"no"}}
	if w := devicePost(h, "revoke", form, ""); w.Code != http.StatusBadRequest || !lw.active("tenant-a", "child-a", id) {
		t.Fatalf("unconfirmed lock: %d", w.Code)
	}
	form.Set("confirm", "yes")
	foreign := url.Values{"csrf_token": {sessionB.CSRFToken}, "child_id": {"child-a"}, "device_id": {id}, "confirm": {"yes"}}
	if w := devicePost(lw.devicePages(t, testAuth{session: sessionB}), "revoke", foreign, ""); w.Code != http.StatusForbidden || !lw.active("tenant-a", "child-a", id) {
		t.Fatalf("foreign lock: %d", w.Code)
	}
	w := devicePost(h, "revoke", form, "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family/devices?child_id=child-a&done=locked" || lw.active("tenant-a", "child-a", id) {
		t.Fatalf("lock: %d %q", w.Code, w.Header().Get("Location"))
	}
	page := deviceGet(h, "?child_id=child-a&done=locked").Body.String()
	if !strings.Contains(page, "gesperrt") || strings.Contains(page, `name="device_id" value="`+id+`"`) {
		t.Fatal("locked device still offers a lock")
	}
	form.Set("device_id", "unknown")
	if w := devicePost(h, "revoke", form, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown device: %d", w.Code)
	}
}

func TestDevicePagesNeedTheirDependencies(t *testing.T) {
	lw := newLostWorld(t)
	auth := testAuth{session: sessionA}
	if _, err := NewDevicePages(nil, lw.pairing, lw.families, "https://app.example.org", nil); err == nil {
		t.Error("nil authenticator accepted")
	}
	if _, err := NewDevicePages(auth, nil, lw.families, "https://app.example.org", nil); err == nil {
		t.Error("nil store accepted")
	}
	if _, err := NewDevicePages(auth, lw.pairing, nil, "https://app.example.org", nil); err == nil {
		t.Error("nil families accepted")
	}
	for _, origin := range []string{"", "http://app.example.org", "https://app.example.org/x", "https://user@app.example.org"} {
		if _, err := NewDevicePages(auth, lw.pairing, lw.families, origin, nil); err == nil {
			t.Errorf("origin %q accepted", origin)
		}
	}
	h, err := NewDevicePages(auth, lw.pairing, lw.families, "https://app.example.org", nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := deviceGet(h, "?child_id=child-a"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `data-yp-skin="classic"`) {
		t.Fatalf("without looks: %d", w.Code)
	}
}
