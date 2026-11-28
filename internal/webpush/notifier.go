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

// Authorizer reports whether this device of this child may read the
// conversation right now. It must use the same current-state predicates as
// reading the conversation.
type Authorizer func(d Device, conversationID string) bool

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
	pending map[child]map[string]bool
	wake    chan struct{}
}

// NewNotifier needs every part; there is no default.
func NewNotifier(store *Store, key *Key, may Authorizer, client Doer, now func() time.Time) (*Notifier, error) {
	if store == nil || key == nil || may == nil || client == nil || now == nil {
		return nil, errors.New("push notifier needs a store, a key, an authorizer, a client and a clock")
	}
	return &Notifier{store: store, key: key, may: may, client: client, now: now, logf: log.Printf,
		pending: map[child]map[string]bool{}, wake: make(chan struct{}, 1)}, nil
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
	if tenantID == "" || childID == "" || conversationID == "" {
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
		conversations = map[string]bool{}
		n.pending[c] = conversations
	}
	conversations[conversationID] = true
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
	n.pending = map[child]map[string]bool{}
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

func (n *Notifier) deliver(ctx context.Context, c child, conversations map[string]bool) {
	for _, sub := range n.store.subscriptions(c.tenantID, c.childID) {
		d := sub.Device
		if _, ok := n.store.devices.ActiveDevice(d.TenantID, d.ChildID, d.DeviceID); !ok {
			// Revocation ends delivery and the stored delivery credential.
			if err := n.store.remove(d, sub.Endpoint); err != nil {
				n.logf("push subscription of a revoked device could not be removed")
			}
			continue
		}
		allowed := false
		for id := range conversations {
			if n.may(d, id) {
				allowed = true
				break
			}
		}
		if allowed {
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
