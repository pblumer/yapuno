package webpush

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder answers every push with a fixed status and keeps the requests.
type recorder struct {
	mu       sync.Mutex
	status   int
	err      error
	requests []*http.Request
	bodies   []string
}

func (r *recorder) Do(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	body := ""
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	r.requests, r.bodies = append(r.requests, req), append(r.bodies, body)
	if r.err != nil {
		return nil, r.err
	}
	return &http.Response{StatusCode: r.status, Body: io.NopCloser(strings.NewReader("ignored"))}, nil
}

func (r *recorder) sent() []*http.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*http.Request(nil), r.requests...)
}

// readers decides which device may read which conversation.
type readers struct {
	mu      sync.Mutex
	allowed map[Device]map[string]bool
}

func (r *readers) may(d Device, conversation string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.allowed[d][conversation]
}

func (r *readers) allow(d Device, conversation string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.allowed == nil {
		r.allowed = map[Device]map[string]bool{}
	}
	if r.allowed[d] == nil {
		r.allowed[d] = map[string]bool{}
	}
	r.allowed[d][conversation] = true
}

func (r *readers) deny(d Device, conversation string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.allowed[d], conversation)
}

type rig struct {
	devices *fakeDevices
	store   *Store
	readers *readers
	client  *recorder
	n       *Notifier
	logs    *strings.Builder
}

func newRig(t *testing.T) *rig {
	t.Helper()
	key, _ := newKey(t)
	devices := newDevices(alexPhone, alexTablet, beaPhone)
	store, _ := openStore(t, devices)
	r := &rig{devices: devices, store: store, readers: &readers{}, client: &recorder{status: http.StatusCreated}, logs: &strings.Builder{}}
	n, err := NewNotifier(store, key, r.readers.may, r.client, func() time.Time { return time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	n.logf = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(r.logs, format+"\n", args...)
	}
	r.n = n
	return r
}

const chat = "0123456789abcdef0123456789abcdef"

func TestAPushIsContentFreeAndSignedForThePushService(t *testing.T) {
	r := newRig(t)
	if err := r.store.Subscribe(alexPhone, appleEndpoint); err != nil {
		t.Fatal(err)
	}
	r.readers.allow(alexPhone, chat)
	r.n.Notify("tenant-a", "child-a", chat)
	r.n.Flush(context.Background())
	sent := r.client.sent()
	if len(sent) != 1 {
		t.Fatalf("%d pushes", len(sent))
	}
	req := sent[0]
	if req.Method != http.MethodPost || req.URL.String() != appleEndpoint {
		t.Fatalf("request %s %s", req.Method, req.URL)
	}
	if r.client.bodies[0] != "" || req.ContentLength != 0 {
		t.Fatal("a push carries a body")
	}
	for k, v := range map[string]string{"TTL": "43200", "Urgency": "high", "Topic": "yapuno-new-message"} {
		if req.Header.Get(k) != v {
			t.Errorf("%s: %q", k, req.Header.Get(k))
		}
	}
	if !strings.HasPrefix(req.Header.Get("Authorization"), "vapid t=") {
		t.Fatalf("authorization: %q", req.Header.Get("Authorization"))
	}
	if req.Header.Get("Content-Encoding") != "" || req.Header.Get("Content-Type") != "" {
		t.Fatal("payload headers on an empty push")
	}
	// Nothing in the request names the child, the device or the conversation.
	dump := req.URL.String() + fmt.Sprint(req.Header)
	for _, word := range []string{"tenant-a", "child-a", "device-a1", chat} {
		if strings.Contains(dump, word) {
			t.Errorf("request mentions %q", word)
		}
	}
}

func TestEveryApprovedDeviceOfTheRecipientIsNotifiedAndNobodyElse(t *testing.T) {
	r := newRig(t)
	for d, e := range map[Device]string{alexPhone: appleEndpoint, alexTablet: "https://web.push.apple.com/tablet", beaPhone: googleEndpoint} {
		if err := r.store.Subscribe(d, e); err != nil {
			t.Fatal(err)
		}
		r.readers.allow(d, chat)
	}
	r.n.Notify("tenant-a", "child-a", chat)
	r.n.Flush(context.Background())
	got := map[string]bool{}
	for _, req := range r.client.sent() {
		got[req.URL.String()] = true
	}
	if len(got) != 2 || !got[appleEndpoint] || !got["https://web.push.apple.com/tablet"] {
		t.Fatalf("pushed to %v", got)
	}
}

func TestNoPushWithoutCurrentReadPermission(t *testing.T) {
	r := newRig(t)
	if err := r.store.Subscribe(alexPhone, appleEndpoint); err != nil {
		t.Fatal(err)
	}
	// The contact was removed or blocked after the message was stored.
	r.n.Notify("tenant-a", "child-a", chat)
	r.n.Flush(context.Background())
	if len(r.client.sent()) != 0 {
		t.Fatal("pushed without permission to read")
	}
	if !r.store.Subscribed(alexPhone) {
		t.Fatal("a missing permission removed the subscription of an approved device")
	}
	r.readers.allow(alexPhone, chat)
	r.readers.deny(alexPhone, chat)
	r.n.Notify("tenant-a", "child-a", chat)
	r.n.Flush(context.Background())
	if len(r.client.sent()) != 0 {
		t.Fatal("pushed after the permission ended")
	}
}

func TestARevokedDeviceGetsNothingAndLosesItsSubscription(t *testing.T) {
	r := newRig(t)
	if err := r.store.Subscribe(alexPhone, appleEndpoint); err != nil {
		t.Fatal(err)
	}
	r.readers.allow(alexPhone, chat)
	r.devices.revoke(alexPhone)
	r.n.Notify("tenant-a", "child-a", chat)
	r.n.Flush(context.Background())
	if len(r.client.sent()) != 0 {
		t.Fatal("pushed to a revoked device")
	}
	if r.store.Subscribed(alexPhone) {
		t.Fatal("subscription of a revoked device kept")
	}
}

func TestGoneSubscriptionsAreRemovedOtherFailuresAreNot(t *testing.T) {
	for _, c := range []struct {
		status int
		err    error
		keep   bool
	}{
		{http.StatusGone, nil, false},
		{http.StatusNotFound, nil, false},
		{http.StatusTooManyRequests, nil, true},
		{http.StatusInternalServerError, nil, true},
		{http.StatusBadRequest, nil, true},
		{0, errors.New("network down"), true},
	} {
		r := newRig(t)
		r.client.status, r.client.err = c.status, c.err
		if err := r.store.Subscribe(alexPhone, appleEndpoint); err != nil {
			t.Fatal(err)
		}
		r.readers.allow(alexPhone, chat)
		r.n.Notify("tenant-a", "child-a", chat)
		r.n.Flush(context.Background())
		if r.store.Subscribed(alexPhone) != c.keep {
			t.Errorf("status %d, error %v: kept=%v", c.status, c.err, !c.keep)
		}
		if strings.Contains(r.logs.String(), appleEndpoint) || strings.Contains(r.logs.String(), "child-a") {
			t.Errorf("log names the endpoint or the child: %s", r.logs.String())
		}
	}
}

func TestNoticesForOneChildAreMergedUntilDelivered(t *testing.T) {
	r := newRig(t)
	if err := r.store.Subscribe(alexPhone, appleEndpoint); err != nil {
		t.Fatal(err)
	}
	other := "fedcba9876543210fedcba9876543210"
	r.readers.allow(alexPhone, other)
	for i := 0; i < 5; i++ {
		r.n.Notify("tenant-a", "child-a", chat)
	}
	r.n.Notify("tenant-a", "child-a", other)
	r.n.Flush(context.Background())
	if len(r.client.sent()) != 1 {
		t.Fatalf("%d pushes for one burst", len(r.client.sent()))
	}
	r.n.Flush(context.Background())
	if len(r.client.sent()) != 1 {
		t.Fatal("pushed again without a new message")
	}
}

func TestTheQueueIsBounded(t *testing.T) {
	r := newRig(t)
	for i := 0; i < MaxPending+10; i++ {
		r.n.Notify("tenant-x", fmt.Sprintf("child-%d", i), chat)
	}
	r.n.mu.Lock()
	pending := len(r.n.pending)
	r.n.mu.Unlock()
	if pending != MaxPending {
		t.Fatalf("%d pending", pending)
	}
	if !strings.Contains(r.logs.String(), "dropped") {
		t.Fatal("a dropped notice was not logged")
	}
	// Invalid input is ignored.
	r.n.Notify("", "child-a", chat)
	r.n.Notify("tenant-a", "", chat)
	r.n.Notify("tenant-a", "child-a", "")
}

func TestRunDeliversInTheBackgroundUntilCancelled(t *testing.T) {
	r := newRig(t)
	if err := r.store.Subscribe(alexPhone, appleEndpoint); err != nil {
		t.Fatal(err)
	}
	r.readers.allow(alexPhone, chat)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.n.Run(ctx); close(done) }()
	r.n.Notify("tenant-a", "child-a", chat)
	deadline := time.Now().Add(5 * time.Second)
	for len(r.client.sent()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(r.client.sent()) != 1 {
		t.Fatal("not delivered in the background")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestNewNotifierNeedsEveryPart(t *testing.T) {
	key, _ := newKey(t)
	store, _ := openStore(t, newDevices())
	may := func(Device, string) bool { return true }
	now := time.Now
	client := &recorder{}
	for name, build := range map[string]func() (*Notifier, error){
		"store":  func() (*Notifier, error) { return NewNotifier(nil, key, may, client, now) },
		"key":    func() (*Notifier, error) { return NewNotifier(store, nil, may, client, now) },
		"check":  func() (*Notifier, error) { return NewNotifier(store, key, nil, client, now) },
		"client": func() (*Notifier, error) { return NewNotifier(store, key, may, nil, now) },
		"clock":  func() (*Notifier, error) { return NewNotifier(store, key, may, client, nil) },
	} {
		if _, err := build(); err == nil {
			t.Errorf("built without %s", name)
		}
	}
}

func TestTheClientFollowsNoRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("redirect followed")
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client := NewClient()
	if client.Timeout <= 0 || client.Timeout > 30*time.Second {
		t.Fatalf("timeout %v", client.Timeout)
	}
	req, _ := http.NewRequest(http.MethodPost, redirect.URL, http.NoBody)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
