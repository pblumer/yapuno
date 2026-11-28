package childweb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/webpush"
)

type fakeNotices struct {
	mu    sync.Mutex
	kinds map[webpush.Device]webpush.Kind
	asked []webpush.Device
}

func (f *fakeNotices) Take(d webpush.Device) webpush.Kind {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, d)
	k := f.kinds[d]
	delete(f.kinds, d)
	return k
}

func TestTheDeviceAsksWhatItWasToldAbout(t *testing.T) {
	w := newPushWorld(t)
	notices := &fakeNotices{kinds: map[webpush.Device]webpush.Kind{}}
	var err error
	if w.handler, err = New(w.sessions, w.families, Options{Push: Push{Key: w.key, Subscriptions: w.push, Notices: notices}}); err != nil {
		t.Fatal(err)
	}
	alexDevice, alexKey := pairingtest.Pair(t, w.pairing, "guardian-a", "tenant-a", "child-a")
	alex := w.login(t, alexDevice, alexKey)
	beaDevice, _ := pairingtest.Pair(t, w.pairing, "guardian-b", "tenant-b", "child-b")
	alexPhone := webpush.Device{TenantID: "tenant-a", ChildID: "child-a", DeviceID: alexDevice}
	notices.kinds[alexPhone] = webpush.KindTurn | webpush.KindInvite
	notices.kinds[webpush.Device{TenantID: "tenant-b", ChildID: "child-b", DeviceID: beaDevice}] = webpush.KindMessage
	ask := func(cookie *http.Cookie, mutate func(*http.Request)) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, origin+"/child/push/notice", nil)
		r.Header.Set("Origin", origin)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if mutate != nil {
			mutate(r)
		}
		rec := httptest.NewRecorder()
		w.handler.ServeHTTP(rec, r)
		return rec
	}
	rec := ask(alex, nil)
	var got map[string]bool
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("ask: %d %s", rec.Code, rec.Body.String())
	}
	if len(got) != 3 || !got["turn"] || !got["invite"] || got["message"] {
		t.Fatalf("kinds: %v", got)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable")
	}
	// Only the session's own device was asked about, never another child's.
	for _, d := range notices.asked {
		if d != alexPhone {
			t.Fatalf("asked about %+v", d)
		}
	}
	if body := ask(alex, nil).Body.String(); strings.Contains(body, "true") {
		t.Fatalf("second answer: %s", body)
	}
	if rec := ask(nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", rec.Code)
	}
	if rec := ask(alex, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }); rec.Code != http.StatusForbidden {
		t.Fatalf("cross origin: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, origin+"/child/push/notice", nil)
	r.AddCookie(alex)
	w.handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
	// The worker may load its helpers and ask its own origin, nothing else.
	worker := w.get("/child/sw.js", nil)
	csp := worker.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "connect-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("worker CSP lacks %q: %s", want, csp)
		}
	}
	js := worker.Body.String()
	for _, want := range []string{`importScripts("/child/sig-utils.js", "/child/push-utils.js")`, "/child/push/notice", "noticeText"} {
		if !strings.Contains(js, want) {
			t.Errorf("worker lacks %q", want)
		}
	}
}

func TestNoNoticeRouteWithoutNotices(t *testing.T) {
	w := newPushWorld(t)
	r := httptest.NewRequest(http.MethodPost, origin+"/child/push/notice", nil)
	r.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("route without notices: %d", rec.Code)
	}
}
