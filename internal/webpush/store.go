package webpush

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/pblumer/yapuno/internal/devicepairing"
)

const (
	// MaxPerChild bounds the subscriptions of one child, one per device.
	MaxPerChild = 10

	maxSubscriptions = 100000
	maxFile          = 64 << 20
)

// Device is one paired device of one child. Callers MUST take it from a
// verified child session or from the authoritative device registry, never
// from identifiers a client sent.
type Device struct {
	TenantID string
	ChildID  string
	DeviceID string
}

func (d Device) valid() bool {
	for _, id := range []string{d.TenantID, d.ChildID, d.DeviceID} {
		if id == "" || len(id) > 128 || strings.ContainsAny(id, "\x00\r\n") {
			return false
		}
	}
	return true
}

// Devices is the authoritative registry of approved devices.
type Devices interface {
	ActiveDevice(tenantID, childID, deviceID string) (devicepairing.Device, bool)
}

type subscription struct {
	Device   Device
	Endpoint string
}

// entry is one line of the file. The payload keys a browser offers (p256dh,
// auth) are deliberately absent: no payload is ever sent.
type entry struct {
	TenantID string `json:"tenant_id"`
	ChildID  string `json:"child_id"`
	DeviceID string `json:"device_id"`
	Endpoint string `json:"endpoint"`
}

type document struct {
	Subscriptions []entry `json:"subscriptions"`
}

// Store keeps one push endpoint per approved device in a private file and
// persists every change with an atomic rename before acknowledging it. It
// must have a single writer.
type Store struct {
	mu        sync.Mutex
	path      string
	devices   Devices
	subs      map[Device]string
	failWrite bool // test hook
}

// Open loads an existing private file or creates an empty one. A malformed,
// symlinked or group/world-accessible file is refused. Subscriptions of
// devices that are no longer approved are removed.
func Open(path string, devices Devices) (*Store, error) {
	if path == "" || devices == nil {
		return nil, ErrInvalid
	}
	s := &Store{path: path, devices: devices, subs: map[Device]string{}}
	stat, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := s.write(s.subs); err != nil {
			return nil, ErrUnavailable
		}
		return s, nil
	}
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 || stat.Size() > maxFile {
		return nil, ErrUnavailable
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.DisallowUnknownFields()
	var doc document
	if err := dec.Decode(&doc); err != nil {
		return nil, ErrUnavailable
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, ErrUnavailable
	}
	if len(doc.Subscriptions) > maxSubscriptions {
		return nil, ErrUnavailable
	}
	endpoints := map[string]bool{}
	for _, e := range doc.Subscriptions {
		d := Device{TenantID: e.TenantID, ChildID: e.ChildID, DeviceID: e.DeviceID}
		_, twice := s.subs[d]
		if !d.valid() || !ValidEndpoint(e.Endpoint) || twice || endpoints[e.Endpoint] {
			return nil, ErrUnavailable
		}
		s.subs[d], endpoints[e.Endpoint] = e.Endpoint, true
	}
	next := map[Device]string{}
	for d, endpoint := range s.subs {
		if _, ok := devices.ActiveDevice(d.TenantID, d.ChildID, d.DeviceID); ok {
			next[d] = endpoint
		}
	}
	if len(next) != len(s.subs) {
		if err := s.write(next); err != nil {
			return nil, ErrUnavailable
		}
		s.subs = next
	}
	return s, nil
}

func (s *Store) write(subs map[Device]string) error {
	if s.failWrite {
		return errors.New("write failed")
	}
	doc := document{Subscriptions: make([]entry, 0, len(subs))}
	for d, endpoint := range subs {
		doc.Subscriptions = append(doc.Subscriptions, entry{TenantID: d.TenantID, ChildID: d.ChildID, DeviceID: d.DeviceID, Endpoint: endpoint})
	}
	sort.Slice(doc.Subscriptions, func(i, j int) bool {
		a, b := doc.Subscriptions[i], doc.Subscriptions[j]
		return a.TenantID+"\x00"+a.ChildID+"\x00"+a.DeviceID < b.TenantID+"\x00"+b.ChildID+"\x00"+b.DeviceID
	})
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	f, err := os.CreateTemp(dir, ".yapuno-push-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	if handle, err := os.Open(dir); err == nil {
		defer handle.Close()
		return handle.Sync()
	}
	return nil
}

// commit writes next and makes it current. The caller holds the lock.
func (s *Store) commit(next map[Device]string) error {
	if err := s.write(next); err != nil {
		return ErrUnavailable
	}
	s.subs = next
	return nil
}

func (s *Store) copySubs() map[Device]string {
	next := make(map[Device]string, len(s.subs)+1)
	for d, e := range s.subs {
		next[d] = e
	}
	return next
}

// Subscribe stores the endpoint for an approved device and replaces the
// device's previous one. An endpoint stored for another device moves to this
// one, so no endpoint is ever delivered to for two devices.
func (s *Store) Subscribe(d Device, endpoint string) error {
	if !d.valid() || !ValidEndpoint(endpoint) {
		return ErrInvalid
	}
	if _, ok := s.devices.ActiveDevice(d.TenantID, d.ChildID, d.DeviceID); !ok {
		return ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subs[d] == endpoint {
		return nil
	}
	next := s.copySubs()
	for other, e := range next {
		if e == endpoint {
			delete(next, other)
		}
	}
	next[d] = endpoint
	children := 0
	for other := range next {
		if other.TenantID == d.TenantID && other.ChildID == d.ChildID {
			children++
		}
	}
	if children > MaxPerChild || len(next) > maxSubscriptions {
		return ErrFull
	}
	return s.commit(next)
}

// Unsubscribe removes the subscription of a device. Removing what is not
// there is not an error.
func (s *Store) Unsubscribe(d Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subs[d]; !ok {
		return nil
	}
	next := s.copySubs()
	delete(next, d)
	return s.commit(next)
}

// remove deletes the subscription of a device only if it still has the given
// endpoint, so a subscription renewed in the meantime survives.
func (s *Store) remove(d Device, endpoint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subs[d] != endpoint {
		return nil
	}
	next := s.copySubs()
	delete(next, d)
	return s.commit(next)
}

// Subscribed reports whether the device has a subscription.
func (s *Store) Subscribed(d Device) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.subs[d]
	return ok
}

// subscriptions returns the subscriptions of one child, in a stable order.
func (s *Store) subscriptions(tenantID, childID string) []subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []subscription
	for d, e := range s.subs {
		if d.TenantID == tenantID && d.ChildID == childID {
			out = append(out, subscription{Device: d, Endpoint: e})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device.DeviceID < out[j].Device.DeviceID })
	return out
}

// PurgeChild removes every subscription of one child. It is a trusted call
// for the deletion of that child.
func (s *Store) PurgeChild(tenantID, childID string) error {
	return s.purge(func(d Device) bool { return d.TenantID == tenantID && d.ChildID == childID })
}

// PurgeTenant removes every subscription of one family.
func (s *Store) PurgeTenant(tenantID string) error {
	return s.purge(func(d Device) bool { return d.TenantID == tenantID })
}

func (s *Store) purge(match func(Device) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := map[Device]string{}
	for d, e := range s.subs {
		if !match(d) {
			next[d] = e
		}
	}
	if len(next) == len(s.subs) {
		return nil
	}
	return s.commit(next)
}
