package webpush

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"
)

const (
	// MaxPending bounds how many children can wait for a notice at once.
	// Beyond that a notice is dropped; the message itself is stored.
	MaxPending = 1024

	// ttl is how long a push service keeps an undelivered notice, in seconds.
	ttl = "43200"
	// topic lets a push service replace an undelivered notice with the next
	// one, so a device that was offline gets one notice, not a pile.
	topic = "yapuno-new-message"

	requestTimeout = 10 * time.Second
)

// Kind is what a notice is about. A push carries no data; the device asks
// the server afterwards which kinds were delivered to it (Take).
type Kind uint8

const (
	// KindMessage: a message was stored in a conversation.
	KindMessage Kind = 1 << iota
	// KindTurn: it is the child's move in a game.
	KindTurn
	// KindInvite: the child was invited to a game.
	KindInvite

	allKinds = KindMessage | KindTurn | KindInvite
)

// Notice is one reason to tell a child: its kind and the conversation or
// game scope it concerns.
type Notice struct {
	Kind Kind
	ID   string
}

// NoticeLifetime is how long a device can ask what it was told; the same
// as a push service keeps an undelivered push.
const NoticeLifetime = 12 * time.Hour

// maxTold bounds the devices remembered as told.
const maxTold = 4096

// Authorizer reports whether this device of this child may be told about
// the notice right now. It must use the same current-state predicates as
// reading the conversation or playing the game.
type Authorizer func(d Device, n Notice) bool

// Doer sends one HTTP request.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type child struct{ tenantID, childID string }

// Notifier sends content-free notices in the background.
type Notifier struct {
	store  *Store
	key    *Key
	may    Authorizer
	client Doer
	now    func() time.Time
	logf   func(format string, args ...any)

	mu      sync.Mutex
	pending map[child]map[Notice]bool
	told    map[Device]told
	wake    chan struct{}
}

type told struct {
	kinds Kind
	at    time.Time
}

// NewNotifier needs every part; there is no default.
func NewNotifier(store *Store, key *Key, may Authorizer, client Doer, now func() time.Time) (*Notifier, error) {
	if store == nil || key == nil || may == nil || client == nil || now == nil {
		return nil, errors.New("push notifier needs a store, a key, an authorizer, a client and a clock")
	}
	return &Notifier{store: store, key: key, may: may, client: client, now: now, logf: log.Printf,
		pending: map[child]map[Notice]bool{}, told: map[Device]told{}, wake: make(chan struct{}, 1)}, nil
}

// NewClient returns the HTTP client for push services: a short timeout and
// no redirects, so a push service cannot send the server elsewhere.
func NewClient() *http.Client {
	return &http.Client{Timeout: requestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// Notify asks for a notice to the devices of a child because a message in
// the conversation was stored. It never blocks and never fails the caller:
// notices for the same child are merged, and a full queue drops the notice.
// Whether a device is told is decided at delivery from current state.
func (n *Notifier) Notify(tenantID, childID, conversationID string) {
	n.Send(tenantID, childID, Notice{Kind: KindMessage, ID: conversationID})
}

// Send is Notify for any kind of notice.
func (n *Notifier) Send(tenantID, childID string, notice Notice) {
	if tenantID == "" || childID == "" || notice.ID == "" || notice.Kind == 0 || notice.Kind&^allKinds != 0 ||
		notice.Kind&(notice.Kind-1) != 0 {
		return
	}
	c := child{tenantID, childID}
	n.mu.Lock()
	conversations, waiting := n.pending[c]
	if !waiting {
		if len(n.pending) >= MaxPending {
			n.mu.Unlock()
			n.logf("push notice dropped: queue full")
			return
		}
		conversations = map[Notice]bool{}
		n.pending[c] = conversations
	}
	conversations[notice] = true
	n.mu.Unlock()
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// Run delivers pending notices until ctx ends.
func (n *Notifier) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-n.wake:
			n.Flush(ctx)
		}
	}
}

// Flush takes every waiting notice and delivers it once, synchronously. Run
// calls it whenever a notice arrives; tests and shutdown may call it directly.
func (n *Notifier) Flush(ctx context.Context) {
	n.mu.Lock()
	batch := n.pending
	n.pending = map[child]map[Notice]bool{}
	n.mu.Unlock()
	children := make([]child, 0, len(batch))
	for c := range batch {
		children = append(children, c)
	}
	sort.Slice(children, func(i, j int) bool {
		return children[i].tenantID+"\x00"+children[i].childID < children[j].tenantID+"\x00"+children[j].childID
	})
	for _, c := range children {
		if ctx.Err() != nil {
			return
		}
		n.deliver(ctx, c, batch[c])
	}
}

func (n *Notifier) deliver(ctx context.Context, c child, notices map[Notice]bool) {
	for _, sub := range n.store.subscriptions(c.tenantID, c.childID) {
		d := sub.Device
		if _, ok := n.store.devices.ActiveDevice(d.TenantID, d.ChildID, d.DeviceID); !ok {
			// Revocation ends delivery and the stored delivery credential.
			if err := n.store.remove(d, sub.Endpoint); err != nil {
				n.logf("push subscription of a revoked device could not be removed")
			}
			continue
		}
		var kinds Kind
		for notice := range notices {
			if kinds&notice.Kind == 0 && n.may(d, notice) {
				kinds |= notice.Kind
			}
		}
		if kinds != 0 {
			// Recorded before the push, so the device finds it when it asks.
			n.record(d, kinds)
			n.send(ctx, sub)
		}
	}
}

func (n *Notifier) send(ctx context.Context, sub subscription) {
	authorization, err := n.key.authorization(sub.Endpoint, n.now())
	if err != nil {
		n.logf("push notice not signed")
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, http.NoBody)
	if err != nil {
		n.logf("push notice not built")
		return
	}
	req.ContentLength = 0
	req.Header.Set("Authorization", authorization)
	req.Header.Set("TTL", ttl)
	req.Header.Set("Urgency", "high")
	req.Header.Set("Topic", topic)
	resp, err := n.client.Do(req)
	if err != nil {
		// The error text may contain the endpoint; it is not logged.
		n.logf("push notice not delivered: push service unreachable")
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		// The browser ended the subscription; it will not come back.
		if err := n.store.remove(sub.Device, sub.Endpoint); err != nil {
			n.logf("expired push subscription could not be removed")
		}
	default:
		n.logf("push notice not delivered: push service answered %d", resp.StatusCode)
	}
}

// record remembers what a device was told, merged with what it has not
// asked for yet.
func (n *Notifier) record(d Device, kinds Kind) {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.now()
	if prev, ok := n.told[d]; ok && now.Sub(prev.at) < NoticeLifetime {
		kinds |= prev.kinds
	}
	if _, ok := n.told[d]; !ok && len(n.told) >= maxTold {
		for k, t := range n.told {
			if now.Sub(t.at) >= NoticeLifetime {
				delete(n.told, k)
			}
		}
		if len(n.told) >= maxTold {
			n.logf("push notice kinds not kept: too many devices")
			return
		}
	}
	n.told[d] = told{kinds: kinds, at: now}
}

// Take returns what the device was told and has not asked for yet, and
// forgets it. Only the device's own verified session may ask.
func (n *Notifier) Take(d Device) Kind {
	n.mu.Lock()
	defer n.mu.Unlock()
	t, ok := n.told[d]
	delete(n.told, d)
	if !ok || n.now().Sub(t.at) >= NoticeLifetime {
		return 0
	}
	return t.kinds
}
