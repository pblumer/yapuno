package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/webpush"
)

const (
	beaEndpoint  = "https://web.push.apple.com/bea-phone-endpoint"
	alexEndpoint = "https://fcm.googleapis.com/fcm/send/alex-phone-endpoint"
)

// pushRecorder stands in for the push services and records every push.
type pushRecorder struct {
	mu     sync.Mutex
	status int
	pushes []recordedPush
}

type recordedPush struct {
	endpoint string
	header   http.Header
	body     string
}

func (p *pushRecorder) Do(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	p.pushes = append(p.pushes, recordedPush{endpoint: r.URL.String(), header: r.Header.Clone(), body: string(body)})
	status := p.status
	if status == 0 {
		status = http.StatusCreated
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}, nil
}

// take returns the pushes since the last call.
func (p *pushRecorder) take() []recordedPush {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.pushes
	p.pushes = nil
	return out
}

func (s *securityServer) openPush(opt *optionalStores) {
	s.t.Helper()
	keyPath := filepath.Join(s.root, "push-key.pem")
	if _, err := os.Stat(keyPath); errors.Is(err, os.ErrNotExist) {
		if err := webpush.GenerateKeyFile(keyPath); err != nil {
			s.t.Fatal(err)
		}
	}
	key, err := webpush.LoadKey(keyPath, "https://app.example.org")
	if err != nil {
		s.t.Fatal(err)
	}
	if s.push, err = webpush.Open(filepath.Join(s.root, "push.json"), s.pairing); err != nil {
		s.t.Fatal(err)
	}
	if s.pushClient == nil {
		s.pushClient = &pushRecorder{}
	}
	opt.pushKey, opt.push, opt.pushClient = key, s.push, s.pushClient
}

func newPushServer(t *testing.T) *chatServer {
	t.Helper()
	s := newChatServer(t, false)
	s.withPush = true
	s.start(false)
	return s
}

var pageCSRF = regexp.MustCompile(`data-csrf="([^"]+)"`)

// subscribe switches notifications on for the device of the cookie.
func (s *chatServer) subscribe(cookie *http.Cookie, endpoint string) int {
	s.t.Helper()
	page := s.do(http.MethodGet, "/child/notifications", cookie, nil)
	m := pageCSRF.FindStringSubmatch(page.Body.String())
	if page.Code != http.StatusOK || m == nil {
		s.t.Fatalf("notifications page: %d", page.Code)
	}
	body, _ := json.Marshal(map[string]string{"endpoint": endpoint})
	r := httptest.NewRequest(http.MethodPost, "https://app.example.org/child/push/subscription", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://app.example.org")
	r.Header.Set("X-CSRF-Token", m[1])
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, r)
	return w.Code
}

// sendJSON sends a message the scripted way with a chosen client id.
func (s *chatServer) sendJSON(cookie *http.Cookie, conversation, clientID, text string) int {
	s.t.Helper()
	m := csrfField.FindStringSubmatch(s.do(http.MethodGet, "/child/chat/"+conversation, cookie, nil).Body.String())
	if m == nil {
		s.t.Fatal("no CSRF token on the chat page")
	}
	body, _ := json.Marshal(map[string]string{"client_id": clientID, "body": text})
	r := httptest.NewRequest(http.MethodPost, "https://app.example.org/child/chat/"+conversation+"/messages", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://app.example.org")
	r.Header.Set("X-CSRF-Token", m[1])
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, r)
	return w.Code
}

func (s *chatServer) flush() []recordedPush {
	s.notifier.Flush(context.Background())
	return s.pushClient.take()
}

func endpoints(ps []recordedPush) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.endpoint)
	}
	return out
}

func TestAChildIsNotifiedWithoutContentWhenSomebodyWritesEndToEnd(t *testing.T) {
	s := newPushServer(t)
	_, alex := s.device("guardian-a", alexParty)
	_, bea := s.device("guardian-b", beaParty)
	if code := s.subscribe(bea, beaEndpoint); code != http.StatusNoContent {
		t.Fatalf("Bea subscribes: %d", code)
	}

	// Alex writes to Bea: Bea's device is told, and nothing else is said.
	if code := s.write("/child/chat", alex, s.link, "Hoi Bea, chunsch au?"); code != http.StatusSeeOther {
		t.Fatalf("Alex writes: %d", code)
	}
	pushes := s.flush()
	if len(pushes) != 1 || pushes[0].endpoint != beaEndpoint || pushes[0].body != "" {
		t.Fatalf("pushes: %+v", pushes)
	}
	dump := pushes[0].endpoint + pushes[0].body
	for k, v := range pushes[0].header {
		dump += k + strings.Join(v, ",")
	}
	for _, leak := range []string{"Hoi", "Alex", "Bea", "child-a", "child-b", "tenant-a", "tenant-b", s.link} {
		if strings.Contains(dump, leak) {
			t.Errorf("push mentions %q", leak)
		}
	}

	// A retry of the same message is not a new message.
	for i := 0; i < 2; i++ {
		if code := s.sendJSON(alex, s.link, "retry-client-id-0001", "Nomal"); code != http.StatusCreated && code != http.StatusOK {
			t.Fatalf("send %d: %d", i, code)
		}
	}
	if got := s.flush(); len(got) != 1 {
		t.Fatalf("retry pushed %d times", len(got))
	}

	// Bea answers: Alex has no subscription, and the writer is never told.
	if code := s.write("/child/chat", bea, s.link, "Klar!"); code != http.StatusSeeOther {
		t.Fatalf("Bea writes: %d", code)
	}
	if got := s.flush(); len(got) != 0 {
		t.Fatalf("pushed to %v", endpoints(got))
	}

	// Bea's guardian writes to Bea: Bea is told.
	bruno := s.issue("guardian-b", "tenant-b")
	own := s.threads(bruno, "Deine Kinder")
	if len(own) != 1 || s.write("/family/chat", bruno, own[0], "Znacht!") != http.StatusSeeOther {
		t.Fatal("guardian cannot write to Bea")
	}
	if got := s.flush(); len(got) != 1 || got[0].endpoint != beaEndpoint {
		t.Fatalf("guardian message: %v", endpoints(got))
	}
	// Bea writes to the guardian: guardians get no pushes yet.
	parents := childThread.FindAllStringSubmatch(s.do(http.MethodGet, "/child", bea, nil).Body.String(), -1)
	for _, m := range parents {
		if m[1] != s.link {
			if code := s.write("/child/chat", bea, m[1], "Chume!"); code != http.StatusSeeOther {
				t.Fatalf("Bea writes to her guardian: %d", code)
			}
		}
	}
	if got := s.flush(); len(got) != 0 {
		t.Fatalf("pushed to %v", endpoints(got))
	}
	// Notices reach Bea even while no session is open: the device stays paired.
	s.start(false)
	_, alexAgain := s.device("guardian-a", alexParty)
	if code := s.write("/child/chat", alexAgain, s.link, "Bisch no da?"); code != http.StatusSeeOther {
		t.Fatalf("after restart: %d", code)
	}
	if got := s.flush(); len(got) != 1 || got[0].endpoint != beaEndpoint {
		t.Fatalf("after restart: %v", endpoints(got))
	}
}

func TestNoNoticeWhenAccessEndsBeforeDelivery(t *testing.T) {
	t.Run("contact removed", func(t *testing.T) {
		s := newPushServer(t)
		_, alex := s.device("guardian-a", alexParty)
		_, bea := s.device("guardian-b", beaParty)
		s.subscribe(bea, beaEndpoint)
		if code := s.write("/child/chat", alex, s.link, "Hoi"); code != http.StatusSeeOther {
			t.Fatalf("write: %d", code)
		}
		if err := s.contacts.Remove("guardian-b", "tenant-b", "child-b", s.link); err != nil {
			t.Fatal(err)
		}
		if got := s.flush(); len(got) != 0 {
			t.Fatalf("pushed after the contact ended: %v", endpoints(got))
		}
	})
	t.Run("contact blocked", func(t *testing.T) {
		s := newPushServer(t)
		_, alex := s.device("guardian-a", alexParty)
		_, bea := s.device("guardian-b", beaParty)
		s.subscribe(bea, beaEndpoint)
		if code := s.write("/child/chat", alex, s.link, "Hoi"); code != http.StatusSeeOther {
			t.Fatalf("write: %d", code)
		}
		if err := s.contacts.Block(beaParty, s.link); err != nil {
			t.Fatal(err)
		}
		if got := s.flush(); len(got) != 0 {
			t.Fatalf("pushed after the contact was blocked: %v", endpoints(got))
		}
	})
	t.Run("device revoked", func(t *testing.T) {
		s := newPushServer(t)
		_, alex := s.device("guardian-a", alexParty)
		beaDevice, bea := s.device("guardian-b", beaParty)
		s.subscribe(bea, beaEndpoint)
		if code := s.write("/child/chat", alex, s.link, "Hoi"); code != http.StatusSeeOther {
			t.Fatalf("write: %d", code)
		}
		if err := s.pairing.Revoke("guardian-b", "tenant-b", "child-b", beaDevice); err != nil {
			t.Fatal(err)
		}
		if got := s.flush(); len(got) != 0 {
			t.Fatalf("pushed to a revoked device: %v", endpoints(got))
		}
		if s.push.Subscribed(webpush.Device{TenantID: "tenant-b", ChildID: "child-b", DeviceID: beaDevice}) {
			t.Fatal("subscription of a revoked device kept")
		}
	})
	t.Run("push service says gone", func(t *testing.T) {
		s := newPushServer(t)
		_, alex := s.device("guardian-a", alexParty)
		beaDevice, bea := s.device("guardian-b", beaParty)
		s.subscribe(bea, beaEndpoint)
		s.pushClient.status = http.StatusGone
		s.write("/child/chat", alex, s.link, "Hoi")
		if got := s.flush(); len(got) != 1 {
			t.Fatalf("%d pushes", len(got))
		}
		if s.push.Subscribed(webpush.Device{TenantID: "tenant-b", ChildID: "child-b", DeviceID: beaDevice}) {
			t.Fatal("expired subscription kept")
		}
	})
}

func TestNotificationsAreOptionalAndNeedTheirParts(t *testing.T) {
	s := newChatServer(t, false)
	_, alex := s.device("guardian-a", alexParty)
	if code := s.do(http.MethodGet, "/child/notifications", alex, nil).Code; code != http.StatusNotFound {
		t.Fatalf("notifications page without push: %d", code)
	}
	if !strings.Contains(s.do(http.MethodGet, "/family", s.issue("guardian-a", "tenant-a"), nil).Body.String(), "Benachrichtigungen") {
		t.Fatal("the overview does not list notifications as switched off")
	}
	p := newPushServer(t)
	if strings.Contains(p.do(http.MethodGet, "/family", p.issue("guardian-a", "tenant-a"), nil).Body.String(), "Benachrichtigungen") {
		t.Fatal("the overview lists notifications as switched off")
	}

	key, err := webpush.LoadKey(filepath.Join(p.root, "push-key.pem"), "https://app.example.org")
	if err != nil {
		t.Fatal(err)
	}
	for name, opt := range map[string]optionalStores{
		"key without subscriptions": {pairing: p.pairing, contacts: p.contacts, messages: p.messages, pushKey: key},
		"subscriptions without key": {pairing: p.pairing, contacts: p.contacts, messages: p.messages, push: p.push},
		"without a chat":            {pairing: p.pairing, pushKey: key, push: p.push},
		"without pairing":           {guardianChat: p.guardianChat, pushKey: key, push: p.push},
	} {
		if _, err := newHandler("https://app.example.org", p.families, p.guardians, p.mailer, p.sessions, opt); err == nil {
			t.Errorf("%s: handler built", name)
		}
	}
	// Only the guardians' chat is enough: guardians write to their children.
	if _, err := newHandler("https://app.example.org", p.families, p.guardians, p.mailer, p.sessions, optionalStores{pairing: p.pairing, guardianChat: p.guardianChat, pushKey: key, push: p.push}); err != nil {
		t.Fatalf("guardian chat only: %v", err)
	}
}

func TestDeletingAChildRemovesItsSubscriptions(t *testing.T) {
	s := newDeletingServer(t, false)
	s.withPush = true
	s.start(false)
	anna := s.issue("guardian-a", "tenant-a")
	if w := s.do(http.MethodPost, "/family/children", anna, url.Values{"csrf_token": {s.csrf(anna, "/family/children")}, "name": {"Ari"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("second child: %d", w.Code)
	}
	alexDevice, alex := s.device("guardian-a", alexParty)
	if code := s.subscribe(alex, alexEndpoint); code != http.StatusNoContent {
		t.Fatalf("subscribe: %d", code)
	}
	csrf := s.csrf(anna, "/family/children/delete?child_id=child-a")
	if w := s.do(http.MethodPost, "/family/children/delete", anna, url.Values{"csrf_token": {csrf}, "child_id": {"child-a"}, "name": {"Alex"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("delete: %d", w.Code)
	}
	if s.push.Subscribed(webpush.Device{TenantID: "tenant-a", ChildID: "child-a", DeviceID: alexDevice}) {
		t.Fatal("subscription survived the deletion")
	}
	if strings.Contains(s.files()["push.json"], "child-a") || strings.Contains(s.files()["push.json"], alexEndpoint) {
		t.Fatalf("push file: %s", s.files()["push.json"])
	}
}

func TestTheNotifierRunsWithTheServer(t *testing.T) {
	s := newPushServer(t)
	_, alex := s.device("guardian-a", alexParty)
	_, bea := s.device("guardian-b", beaParty)
	s.subscribe(bea, beaEndpoint)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.notifier.Run(ctx)
	s.write("/child/chat", alex, s.link, "Hoi")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.pushClient.mu.Lock()
		n := len(s.pushClient.pushes)
		s.pushClient.mu.Unlock()
		if n == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no push in the background")
}
