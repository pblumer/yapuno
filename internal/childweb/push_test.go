package childweb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/webpush"
)

const appleEndpoint = "https://web.push.apple.com/QGuQyavXutnMH_Fd8Gu7dq3kYs7cV"

type pushWorld struct {
	*world
	push *webpush.Store
	key  *webpush.Key
}

func newPushWorld(t *testing.T) *pushWorld {
	t.Helper()
	w := newWorld(t, false)
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "vapid.pem")
	if err := webpush.GenerateKeyFile(keyPath); err != nil {
		t.Fatal(err)
	}
	key, err := webpush.LoadKey(keyPath, origin)
	if err != nil {
		t.Fatal(err)
	}
	store, err := webpush.Open(filepath.Join(dir, "push.json"), w.pairing)
	if err != nil {
		t.Fatal(err)
	}
	if w.handler, err = New(w.sessions, w.families, Options{Push: Push{Key: key, Subscriptions: store}}); err != nil {
		t.Fatal(err)
	}
	return &pushWorld{world: w, push: store, key: key}
}

var dataCSRF = regexp.MustCompile(`data-csrf="([^"]+)"`)

func (w *pushWorld) csrf(t *testing.T, cookie *http.Cookie) string {
	t.Helper()
	m := dataCSRF.FindStringSubmatch(w.get("/child/notifications", cookie).Body.String())
	if m == nil {
		t.Fatal("no CSRF token on the notifications page")
	}
	return m[1]
}

func (w *pushWorld) subscribe(cookie *http.Cookie, csrf string, payload any, mutate func(*http.Request)) *httptest.ResponseRecorder {
	return w.post("/child/push/subscription", payload, func(r *http.Request) {
		if cookie != nil {
			r.AddCookie(cookie)
		}
		r.Header.Set("X-CSRF-Token", csrf)
		if mutate != nil {
			mutate(r)
		}
	})
}

func TestTheAppCanBeInstalledWithoutRevealingAnything(t *testing.T) {
	// The manifest and icons are public and served with or without push.
	for _, w := range []*world{newWorld(t, false), newPushWorld(t).world} {
		rec := w.get("/child/manifest.webmanifest", nil)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/manifest+json" {
			t.Fatalf("manifest: %d %v", rec.Code, rec.Header())
		}
		var manifest struct {
			Name     string `json:"name"`
			StartURL string `json:"start_url"`
			Scope    string `json:"scope"`
			Display  string `json:"display"`
			Icons    []struct {
				Src   string `json:"src"`
				Sizes string `json:"sizes"`
			} `json:"icons"`
		}
		dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
		if err := dec.Decode(&manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Name != "Yapuno" || manifest.StartURL != "/child" || manifest.Display != "standalone" || manifest.Scope != "/" || len(manifest.Icons) < 2 {
			t.Fatalf("manifest: %+v", manifest)
		}
		for _, icon := range manifest.Icons {
			img := w.get(icon.Src, nil)
			decoded, err := png.Decode(bytes.NewReader(img.Body.Bytes()))
			if img.Code != http.StatusOK || img.Header().Get("Content-Type") != "image/png" || err != nil {
				t.Fatalf("%s: %d %v", icon.Src, img.Code, err)
			}
			if size := decoded.Bounds().Dx(); icon.Sizes != fmt.Sprintf("%dx%d", size, size) || decoded.Bounds().Dy() != size {
				t.Fatalf("%s: %v for %s", icon.Src, decoded.Bounds(), icon.Sizes)
			}
		}
		touch := w.get("/child/apple-touch-icon.png", nil)
		if img, err := png.Decode(bytes.NewReader(touch.Body.Bytes())); err != nil || img.Bounds().Dx() != 180 {
			t.Fatalf("touch icon: %v", err)
		}
		// Every corner is opaque: iOS fills transparency with black.
		img, _ := png.Decode(bytes.NewReader(touch.Body.Bytes()))
		for _, p := range [][2]int{{0, 0}, {179, 0}, {0, 179}, {179, 179}} {
			if _, _, _, a := img.At(p[0], p[1]).RGBA(); a != 0xffff {
				t.Fatalf("transparent corner %v", p)
			}
		}
		shell := w.get("/child", nil)
		// Browsers otherwise ask for /favicon.ico, which does not exist.
		if !strings.Contains(shell.Body.String(), `<link rel="icon" href="/child/icon-192.png" type="image/png">`) {
			t.Fatal("the login shell names no icon")
		}
		if !strings.Contains(shell.Body.String(), `<link rel="manifest" href="/child/manifest.webmanifest">`) || !strings.Contains(shell.Header().Get("Content-Security-Policy"), "manifest-src 'self'") || !strings.Contains(shell.Header().Get("Content-Security-Policy"), "img-src 'self'") {
			t.Fatal("the login shell does not offer the manifest")
		}
	}
}

func httpBody(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }

func TestWithoutPushThereIsNoWorkerPageOrRoute(t *testing.T) {
	w := newWorld(t, false)
	deviceID, key := pairingtest.Pair(t, w.pairing, "guardian-a", "tenant-a", "child-a")
	cookie := w.login(t, deviceID, key)
	for _, path := range []string{"/child/notifications", "/child/sw.js", "/child/push.js", "/child/push-utils.js"} {
		if rec := w.get(path, cookie); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	if rec := w.post("/child/push/subscription", map[string]string{"endpoint": appleEndpoint}, func(r *http.Request) { r.AddCookie(cookie) }); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("subscription route: %d", rec.Code)
	}
	if strings.Contains(w.get("/child", cookie).Body.String(), "/child/notifications") {
		t.Error("home links to notifications")
	}
}

func TestTheNotificationsPageExplainsWhatIsSentAndNeedsASession(t *testing.T) {
	w := newPushWorld(t)
	if rec := w.get("/child/notifications", nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/child" {
		t.Fatalf("signed out: %d %v", rec.Code, rec.Header())
	}
	deviceID, key := pairingtest.Pair(t, w.pairing, "guardian-a", "tenant-a", "child-a")
	cookie := w.login(t, deviceID, key)
	if !strings.Contains(w.get("/child", cookie).Body.String(), `href="/child/notifications"`) {
		t.Fatal("home does not link to the notifications page")
	}
	rec := w.get("/child/notifications", cookie)
	body := rec.Body.String()
	for _, want := range []string{
		"<h1>Benachrich&shy;tigungen</h1>", "Du hast eine neue Nachricht.", "Wer geschrieben hat und was, steht nicht drin.",
		"Zum Home-Bildschirm", `data-key="` + w.key.PublicKey() + `"`, `data-subscribed="0"`,
		`<script src="/child/push-utils.js" defer></script>`, `<script src="/child/push.js" defer></script>`, "<noscript>",
		`role="status"`, `<link rel="manifest" href="/child/manifest.webmanifest">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "worker-src 'self'") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %v", rec.Header())
	}
	for _, leak := range []string{"tenant-a", "child-a", deviceID} {
		if strings.Contains(body, leak) {
			t.Errorf("page shows %q", leak)
		}
	}
	for _, asset := range []string{"/child/push.js", "/child/push-utils.js", "/child/sw.js"} {
		js := w.get(asset, nil)
		if js.Code != http.StatusOK || !strings.HasPrefix(js.Header().Get("Content-Type"), "text/javascript") {
			t.Fatalf("%s: %d", asset, js.Code)
		}
	}
	worker := w.get("/child/sw.js", nil)
	if worker.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("worker cache: %q", worker.Header().Get("Cache-Control"))
	}
	// The worker never reads a payload and shows only the fixed sentences
	// of push-utils.js (tested there).
	if js := worker.Body.String(); strings.Contains(js, "event.data") || strings.Contains(js, "e.data") || strings.Contains(js, ".data.") ||
		!strings.Contains(js, "self.YapunoPush.noticeText(kinds)") || !strings.Contains(js, "showNotification") {
		t.Fatal("worker reads a payload or shows something else")
	}
}

func TestOnlyTheSignedInDeviceSubscribesAndOnlyToAKnownPushService(t *testing.T) {
	w := newPushWorld(t)
	alexDevice, alexKey := pairingtest.Pair(t, w.pairing, "guardian-a", "tenant-a", "child-a")
	alex := w.login(t, alexDevice, alexKey)
	beaDevice, beaKey := pairingtest.Pair(t, w.pairing, "guardian-b", "tenant-b", "child-b")
	bea := w.login(t, beaDevice, beaKey)
	token := w.csrf(t, alex)
	alexPhone := webpush.Device{TenantID: "tenant-a", ChildID: "child-a", DeviceID: alexDevice}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"no session":    w.subscribe(nil, token, map[string]string{"endpoint": appleEndpoint}, nil),
		"no CSRF":       w.subscribe(alex, "", map[string]string{"endpoint": appleEndpoint}, nil),
		"other CSRF":    w.subscribe(alex, w.csrf(t, bea), map[string]string{"endpoint": appleEndpoint}, nil),
		"cross site":    w.subscribe(alex, token, map[string]string{"endpoint": appleEndpoint}, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }),
		"form data":     w.subscribe(alex, token, map[string]string{"endpoint": appleEndpoint}, func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }),
		"foreign host":  w.subscribe(alex, token, map[string]string{"endpoint": "https://attacker.example/x"}, nil),
		"unknown field": w.subscribe(alex, token, map[string]any{"endpoint": appleEndpoint, "keys": map[string]string{"p256dh": "x", "auth": "y"}}, nil),
		"device named":  w.subscribe(alex, token, map[string]string{"endpoint": appleEndpoint, "device_id": beaDevice}, nil),
		"empty":         w.subscribe(alex, token, map[string]string{}, nil),
		"oversized":     w.subscribe(alex, token, map[string]string{"endpoint": appleEndpoint + strings.Repeat("a", 9000)}, nil),
		"trailing content": w.post("/child/push/subscription", nil, func(r *http.Request) {
			r.AddCookie(alex)
			r.Header.Set("X-CSRF-Token", token)
			r.Body = httpBody(`{"endpoint":"` + appleEndpoint + `"}{}`)
		}),
	} {
		if rec.Code < 400 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if w.push.Subscribed(alexPhone) {
		t.Fatal("a refused request subscribed")
	}
	if rec := w.subscribe(alex, token, map[string]string{"endpoint": appleEndpoint}, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("subscribe: %d %s", rec.Code, rec.Body.String())
	}
	if !w.push.Subscribed(alexPhone) || w.push.Subscribed(webpush.Device{TenantID: "tenant-b", ChildID: "child-b", DeviceID: beaDevice}) {
		t.Fatal("subscription stored for the wrong device")
	}
	if !strings.Contains(w.get("/child/notifications", alex).Body.String(), `data-subscribed="1"`) {
		t.Fatal("page does not know the device is subscribed")
	}
	// Turning it off needs the same protections.
	unsubscribe := func(cookie *http.Cookie, csrf string) int {
		return w.post("/child/push/unsubscribe", map[string]string{}, func(r *http.Request) {
			if cookie != nil {
				r.AddCookie(cookie)
			}
			r.Header.Set("X-CSRF-Token", csrf)
		}).Code
	}
	if unsubscribe(nil, token) < 400 || unsubscribe(alex, "") < 400 || unsubscribe(bea, token) < 400 {
		t.Fatal("unprotected unsubscribe")
	}
	if !w.push.Subscribed(alexPhone) {
		t.Fatal("refused unsubscribe removed the subscription")
	}
	if code := unsubscribe(alex, token); code != http.StatusNoContent || w.push.Subscribed(alexPhone) {
		t.Fatalf("unsubscribe: %d", code)
	}
}

func TestSigningOutEndsTheDevicesNotifications(t *testing.T) {
	w := newPushWorld(t)
	deviceID, key := pairingtest.Pair(t, w.pairing, "guardian-a", "tenant-a", "child-a")
	cookie := w.login(t, deviceID, key)
	if rec := w.subscribe(cookie, w.csrf(t, cookie), map[string]string{"endpoint": appleEndpoint}, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("subscribe: %d", rec.Code)
	}
	m := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(w.get("/child", cookie).Body.String())
	r := httptest.NewRequest(http.MethodPost, origin+"/child/session/logout", strings.NewReader(url.Values{"csrf_token": {m[1]}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("logout: %d", rec.Code)
	}
	if w.push.Subscribed(webpush.Device{TenantID: "tenant-a", ChildID: "child-a", DeviceID: deviceID}) {
		t.Fatal("subscription survived signing out")
	}
}

func TestPushNeedsBothKeyAndSubscriptions(t *testing.T) {
	w := newPushWorld(t)
	for _, p := range []Push{{Key: w.key}, {Subscriptions: w.push}} {
		if _, err := New(w.sessions, w.families, Options{Push: p}); err == nil {
			t.Errorf("built with %+v", p)
		}
	}
}
