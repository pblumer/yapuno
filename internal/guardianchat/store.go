// Package guardianchat stores one-to-one text messages in which a guardian
// takes part: a guardian with their own child, and two guardians of different
// families whose children are connected.
//
// Like the children's chat (package messages) every send and every read is
// authorized again from current state, a message is acknowledged only after
// it is on disk, and retries are idempotent. Unlike it, participants are
// role-aware (adult or child) and no conversation is created or stored as a
// record: who may talk to whom follows from guardian grants and from the
// contacts between children at the moment of each request.
// See docs/adr/draft-guardian-chat.md.
package guardianchat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/messages"
)

var (
	// ErrForbidden covers every authorization failure, including unknown
	// threads, so callers cannot probe which threads exist.
	ErrForbidden   = errors.New("not allowed in this conversation")
	ErrInvalid     = errors.New("invalid message")
	ErrConflict    = errors.New("client id already used for a different message")
	ErrRateLimited = errors.New("too many messages")
	ErrUnavailable = errors.New("message could not be stored")
)

const (
	// MaxPerMinute limits one sender in one thread.
	MaxPerMinute = 20
	// MaxPerThread and Retention bound what is kept. Both are provisional
	// and equal to the children's chat (issue #7).
	MaxPerThread = 2000
	Retention    = 30 * 24 * time.Hour
	MaxPage      = 200

	maxLogBytes = 256 << 20
)

var (
	clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)
	regexpID        = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// Families is the authoritative source for guardian grants and child profiles.
type Families interface {
	Cards(guardianID, tenantID string) []familydashboard.ChildCard
	ChildName(tenantID, childID string) (string, bool)
	GuardiansOf(tenantID, childID string) []string
}

// Contacts lists the established contacts of a child.
type Contacts interface {
	Connections(p contacts.Party) []contacts.Connection
}

// Guardians says whether a guardian identity is active and not locked.
type Guardians interface {
	ActiveGuardian(ctx context.Context, guardianID, tenantID string) bool
}

// Devices confirms current device approval for a child.
type Devices interface {
	ActiveDevice(tenantID, childID, deviceID string) (devicepairing.Device, bool)
}

// Actor is an authenticated participant: a guardian from a verified guardian
// session, or a child on one device from a verified child session.
type Actor struct {
	Participant authz.Participant
	DeviceID    string
}

// Guardian is the actor of a guardian session.
func Guardian(tenantID, guardianID string) Actor {
	return Actor{Participant: authz.Participant{TenantID: tenantID, ID: guardianID, Kind: authz.ParticipantAdult}}
}

// Child is the actor of a child session on one device.
func Child(tenantID, childID, deviceID string) Actor {
	return Actor{Participant: authz.Participant{TenantID: tenantID, ID: childID, Kind: authz.ParticipantChild}, DeviceID: deviceID}
}

// Kind is the kind of pair a thread connects.
type Kind uint8

const (
	// Family is a guardian with their own child.
	Family Kind = iota + 1
	// Parents is two guardians of different families.
	Parents
)

func (k Kind) String() string {
	switch k {
	case Family:
		return "family"
	case Parents:
		return "parents"
	}
	return "unknown"
}

// Thread is one conversation the actor may use right now.
type Thread struct {
	ID   string
	Kind Kind
	Peer authz.Participant
	// Via lists, for a Parents thread, the peer family's children through
	// whom the two guardians are connected. It is how the peer is described
	// to the actor ("a guardian of Bea"); it grants nothing.
	Via []contacts.Party
}

// Message is one stored message. Seq orders messages within a thread.
type Message struct {
	Seq      uint64
	From     authz.Participant
	ClientID string
	Body     string
	At       time.Time
}

type party struct {
	Tenant string `json:"tenant"`
	ID     string `json:"id"`
	Kind   string `json:"kind"`
}

func wire(p authz.Participant) party {
	kind := "child"
	if p.Kind == authz.ParticipantAdult {
		kind = "adult"
	}
	return party{Tenant: p.TenantID, ID: p.ID, Kind: kind}
}

func (p party) participant() (authz.Participant, bool) {
	out := authz.Participant{TenantID: p.Tenant, ID: p.ID}
	switch p.Kind {
	case "adult":
		out.Kind = authz.ParticipantAdult
	case "child":
		out.Kind = authz.ParticipantChild
	default:
		return authz.Participant{}, false
	}
	return out, p.Tenant != "" && p.ID != ""
}

// entry is one log line. Every line names both participants, so the log
// alone says whose conversation it is. A mark carries no message; it keeps
// the highest used sequence number of a thread whose messages have expired.
type entry struct {
	Thread   string    `json:"thread"`
	Seq      uint64    `json:"seq"`
	From     party     `json:"from"`
	To       party     `json:"to"`
	ClientID string    `json:"client_id,omitempty"`
	Body     string    `json:"body,omitempty"`
	At       time.Time `json:"at"`
	Mark     bool      `json:"mark,omitempty"`
}

type thread struct {
	a, b     authz.Participant
	messages []Message // ascending by Seq
	last     uint64
}

type idempotencyKey struct {
	thread   string
	sender   authz.Participant
	clientID string
}

// Store is single-writer per file.
type Store struct {
	mu        sync.Mutex
	path      string
	families  Families
	contacts  Contacts
	guardians Guardians
	devices   Devices
	now       func() time.Time

	file    *os.File
	size    int64
	threads map[string]*thread
	keys    map[idempotencyKey]uint64

	maxPerThread int
	// syncFile forces written data to stable storage. It is a field so tests
	// can observe and fail it; production always uses (*os.File).Sync.
	syncFile  func(*os.File) error
	failWrite bool // test hook: the next append writes only part of its line
}

// threadID is the stable identifier of the conversation of two participants.
// It is derived, not secret: knowing it grants nothing.
func threadID(x, y authz.Participant) string {
	key := func(p authz.Participant) string { return wire(p).Kind + "\x00" + p.TenantID + "\x00" + p.ID }
	a, b := key(x), key(y)
	if b < a {
		a, b = b, a
	}
	sum := sha256.Sum256([]byte("yapuno guardian chat\x00" + a + "\x00\x00" + b))
	return hex.EncodeToString(sum[:16])
}

// Open loads the log. It fails closed on insecure permissions and on any
// complete line that is malformed or inconsistent. A partial final line is
// the trace of a crash during an unacknowledged append and is removed.
func Open(path string, f Families, c Contacts, g Guardians, d Devices, now func() time.Time) (*Store, error) {
	if path == "" || f == nil || c == nil || g == nil || d == nil || now == nil {
		return nil, ErrInvalid
	}
	s := &Store{path: path, families: f, contacts: c, guardians: g, devices: d, now: now,
		threads: map[string]*thread{}, keys: map[idempotencyKey]uint64{}, maxPerThread: MaxPerThread, syncFile: (*os.File).Sync}
	stat, err := os.Lstat(path)
	created := errors.Is(err, os.ErrNotExist)
	if err != nil && !created {
		return nil, err
	}
	if !created {
		if !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 || stat.Size() > maxLogBytes {
			return nil, fmt.Errorf("%w: log must be a private regular file", ErrInvalid)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		complete := data
		if i := bytes.LastIndexByte(data, '\n'); i < len(data)-1 {
			complete = data[:i+1]
			if err := os.Truncate(path, int64(len(complete))); err != nil {
				return nil, err
			}
		}
		for _, line := range bytes.Split(complete, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			if err := s.load(line); err != nil {
				return nil, err
			}
		}
		s.size = int64(len(complete))
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	s.file = file
	if created {
		if err := syncDir(filepath.Dir(path)); err != nil {
			file.Close()
			return nil, err
		}
	}
	if err := s.compact(); err != nil {
		file.Close()
		return nil, err
	}
	return s, nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (s *Store) load(line []byte) error {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	var e entry
	if err := dec.Decode(&e); err != nil {
		return fmt.Errorf("%w: corrupt log line", ErrInvalid)
	}
	from, okFrom := e.From.participant()
	to, okTo := e.To.participant()
	// The id must be the one of exactly these two participants.
	if !okFrom || !okTo || from == to || e.Seq == 0 || e.Thread != threadID(from, to) {
		return fmt.Errorf("%w: log line", ErrInvalid)
	}
	th := s.thread(e.Thread, from, to)
	if e.Seq <= th.last {
		return fmt.Errorf("%w: sequence does not increase", ErrInvalid)
	}
	th.last = e.Seq
	if e.Mark {
		return nil
	}
	body, ok := messages.Normalize(e.Body)
	if !clientIDPattern.MatchString(e.ClientID) || !ok || body != e.Body || e.At.IsZero() {
		return fmt.Errorf("%w: stored message", ErrInvalid)
	}
	key := idempotencyKey{e.Thread, from, e.ClientID}
	if _, dup := s.keys[key]; dup {
		return fmt.Errorf("%w: duplicate client id", ErrInvalid)
	}
	s.keys[key] = e.Seq
	th.messages = append(th.messages, Message{Seq: e.Seq, From: from, ClientID: e.ClientID, Body: e.Body, At: e.At})
	return nil
}

func (s *Store) thread(id string, x, y authz.Participant) *thread {
	th, ok := s.threads[id]
	if !ok {
		th = &thread{a: x, b: y}
		s.threads[id] = th
	}
	return th
}

// grant rebuilds the stored relationship of a guardian and a child from the
// family store's own projection.
func (s *Store) grant(guardianID, tenantID, childID string) authz.GuardianGrant {
	for _, c := range s.families.Cards(guardianID, tenantID) {
		if c.ID == childID {
			return authz.GuardianGrant{GuardianID: guardianID, ChildID: childID, TenantID: tenantID, Active: true,
				CanManageDevices: c.Permissions.ManageDevices, CanApproveContacts: c.Permissions.ApproveContacts}
		}
	}
	return authz.GuardianGrant{}
}

// available computes the threads of one participant from current state. With
// live set, it is the authorization of this very request: locks, deactivated
// identities and device approval count. Without it, it only says which
// relationships still exist, which decides what compaction keeps.
func (s *Store) available(a Actor, live bool) []Thread {
	p := a.Participant
	if p.TenantID == "" || p.ID == "" {
		return nil
	}
	active := func(guardianID, tenantID string) bool {
		return !live || s.guardians.ActiveGuardian(context.Background(), guardianID, tenantID)
	}
	var out []Thread
	switch p.Kind {
	case authz.ParticipantChild:
		approved := !live
		if live && a.DeviceID != "" {
			_, approved = s.devices.ActiveDevice(p.TenantID, p.ID, a.DeviceID)
		}
		_, enabled := s.families.ChildName(p.TenantID, p.ID)
		for _, g := range s.families.GuardiansOf(p.TenantID, p.ID) {
			guardian := authz.Participant{TenantID: p.TenantID, ID: g, Kind: authz.ParticipantAdult}
			if authz.CanUseFamilyChat(authz.FamilyChatAccess{Self: p, Guardian: guardian, Child: p, Grant: s.grant(g, p.TenantID, p.ID),
				GuardianActive: active(g, p.TenantID), ChildEnabled: enabled, DeviceApproved: approved}) {
				out = append(out, Thread{ID: threadID(p, guardian), Kind: Family, Peer: guardian})
			}
		}
	case authz.ParticipantAdult:
		if !active(p.ID, p.TenantID) {
			return nil
		}
		via := map[authz.Participant][]contacts.Party{}
		for _, card := range s.families.Cards(p.ID, p.TenantID) {
			child := authz.Participant{TenantID: p.TenantID, ID: card.ID, Kind: authz.ParticipantChild}
			own := s.grant(p.ID, p.TenantID, card.ID)
			_, enabled := s.families.ChildName(p.TenantID, card.ID)
			if authz.CanUseFamilyChat(authz.FamilyChatAccess{Self: p, Guardian: p, Child: child, Grant: own, GuardianActive: true, ChildEnabled: enabled}) {
				out = append(out, Thread{ID: threadID(p, child), Kind: Family, Peer: child})
			}
			for _, conn := range s.contacts.Connections(contacts.Party{TenantID: p.TenantID, ChildID: card.ID}) {
				for _, g := range s.families.GuardiansOf(conn.Peer.TenantID, conn.Peer.ChildID) {
					peer := authz.Participant{TenantID: conn.Peer.TenantID, ID: g, Kind: authz.ParticipantAdult}
					if authz.CanUseParentsChat(authz.ParentsChatAccess{Self: p, Peer: peer, SelfActive: true, PeerActive: active(g, peer.TenantID),
						Contact: conn.Grant, SelfGrant: own, PeerGrant: s.grant(g, conn.Peer.TenantID, conn.Peer.ChildID)}) {
						via[peer] = append(via[peer], conn.Peer)
					}
				}
			}
		}
		for peer, children := range via {
			sort.Slice(children, func(i, j int) bool { return children[i].ChildID < children[j].ChildID })
			out = append(out, Thread{ID: threadID(p, peer), Kind: Parents, Peer: peer, Via: children})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Peer.TenantID+"\x00"+out[i].Peer.ID < out[j].Peer.TenantID+"\x00"+out[j].Peer.ID
	})
	return out
}

// SetGuardians replaces the identity check. The server calls it once while
// it is assembled, before any request, with the directory that also knows
// emergency locks; a store opened earlier would otherwise not see them.
func (s *Store) SetGuardians(g Guardians) error {
	if g == nil {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.guardians = g
	return nil
}

// Threads lists the conversations the actor may use right now.
func (s *Store) Threads(a Actor) []Thread {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.available(a, true)
}

// authorize runs at the moment of every send and every read. A thread is
// usable only if current state yields it for this actor.
func (s *Store) authorize(a Actor, id string) (Thread, bool) {
	if !regexpID.MatchString(id) {
		return Thread{}, false
	}
	for _, th := range s.available(a, true) {
		if th.ID == id {
			return th, true
		}
	}
	return Thread{}, false
}

// Send stores one message and returns it once it is durable. The second
// result is true when this client id was already stored for the same text.
func (s *Store) Send(a Actor, id, clientID, body string) (Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	allowed, ok := s.authorize(a, id)
	if !ok {
		return Message{}, false, ErrForbidden
	}
	normalized, ok := messages.Normalize(body)
	if !ok || !clientIDPattern.MatchString(clientID) {
		return Message{}, false, ErrInvalid
	}
	self := a.Participant
	th := s.thread(id, self, allowed.Peer)
	key := idempotencyKey{id, self, clientID}
	if seq, seen := s.keys[key]; seen {
		original, found := th.find(seq)
		if !found || original.Body != normalized {
			return Message{}, false, ErrConflict
		}
		return original, true, nil
	}
	now := s.now()
	recent := 0
	for i := len(th.messages) - 1; i >= 0 && th.messages[i].At.After(now.Add(-time.Minute)); i-- {
		if th.messages[i].From == self {
			recent++
		}
	}
	if recent >= MaxPerMinute {
		return Message{}, false, ErrRateLimited
	}
	m := Message{Seq: th.last + 1, From: self, ClientID: clientID, Body: normalized, At: now.UTC()}
	if err := s.append(entry{Thread: id, Seq: m.Seq, From: wire(self), To: wire(allowed.Peer), ClientID: clientID, Body: normalized, At: m.At}); err != nil {
		return Message{}, false, ErrUnavailable
	}
	th.last = m.Seq
	th.messages = append(th.messages, m)
	s.keys[key] = m.Seq
	if len(th.messages) > s.maxPerThread+s.maxPerThread/4 {
		// The message is already durable; a failed tidy-up must not fail it.
		_ = s.compact()
	}
	return m, false, nil
}

// append writes one line and syncs it. On any failure the log is cut back to
// its previous length so a partial line cannot corrupt later appends.
func (s *Store) append(e entry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if s.failWrite {
		s.failWrite = false
		_, _ = s.file.Write(line[:len(line)/2])
		err = errors.New("simulated short write")
	} else {
		_, err = s.file.Write(line)
	}
	if err == nil {
		err = s.syncFile(s.file)
	}
	if err != nil {
		if terr := s.file.Truncate(s.size); terr == nil {
			_ = s.syncFile(s.file)
		}
		return err
	}
	s.size += int64(len(line))
	return nil
}

func (t *thread) find(seq uint64) (Message, bool) {
	i := sort.Search(len(t.messages), func(i int) bool { return t.messages[i].Seq >= seq })
	if i < len(t.messages) && t.messages[i].Seq == seq {
		return t.messages[i], true
	}
	return Message{}, false
}

func page(limit int) int {
	if limit <= 0 || limit > MaxPage {
		return MaxPage
	}
	return limit
}

func (s *Store) stored(id string) []Message {
	if th, ok := s.threads[id]; ok {
		return th.messages
	}
	return nil
}

// History returns up to limit messages with a sequence number above after,
// oldest first.
func (s *Store) History(a Actor, id string, after uint64, limit int) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.authorize(a, id); !ok {
		return nil, ErrForbidden
	}
	all := s.stored(id)
	start := sort.Search(len(all), func(i int) bool { return all[i].Seq > after })
	end := min(start+page(limit), len(all))
	return append([]Message{}, all[start:end]...), nil
}

// Recent returns the newest messages, oldest first.
func (s *Store) Recent(a Actor, id string, limit int) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.authorize(a, id); !ok {
		return nil, ErrForbidden
	}
	all := s.stored(id)
	start := max(len(all)-page(limit), 0)
	return append([]Message{}, all[start:]...), nil
}

// exists says whether the relationship behind a stored thread still exists,
// whatever the momentary state of locks and devices.
func (s *Store) exists(id string, th *thread) bool {
	for _, p := range []authz.Participant{th.a, th.b} {
		for _, candidate := range s.available(Actor{Participant: p}, false) {
			if candidate.ID == id {
				return true
			}
		}
	}
	return false
}

// Compact drops expired messages, messages beyond the per-thread cap and all
// messages of threads whose relationship has ended (for example because the
// contact between the children was ended), then rewrites the log atomically.
func (s *Store) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.compact()
}

func (s *Store) compact() error {
	cutoff := s.now().Add(-Retention)
	ids := make([]string, 0, len(s.threads))
	for id := range s.threads {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	next := map[string]*thread{}
	changed := false
	var out bytes.Buffer
	for _, id := range ids {
		old := s.threads[id]
		if !s.exists(id, old) {
			changed = changed || old.last > 0
			continue
		}
		kept := old.messages
		for len(kept) > 0 && !kept[0].At.After(cutoff) {
			kept = kept[1:]
		}
		if len(kept) > s.maxPerThread {
			kept = kept[len(kept)-s.maxPerThread:]
		}
		changed = changed || len(kept) != len(old.messages)
		next[id] = &thread{a: old.a, b: old.b, messages: append([]Message(nil), kept...), last: old.last}
		other := func(p authz.Participant) authz.Participant {
			if p == old.a {
				return old.b
			}
			return old.a
		}
		if len(kept) == 0 && old.last > 0 {
			line, err := json.Marshal(entry{Thread: id, Seq: old.last, From: wire(old.a), To: wire(old.b), At: s.now().UTC(), Mark: true})
			if err != nil {
				return err
			}
			out.Write(append(line, '\n'))
		}
		for _, m := range kept {
			line, err := json.Marshal(entry{Thread: id, Seq: m.Seq, From: wire(m.From), To: wire(other(m.From)), ClientID: m.ClientID, Body: m.Body, At: m.At})
			if err != nil {
				return err
			}
			out.Write(append(line, '\n'))
		}
	}
	if !changed {
		return nil
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".yapuno-guardian-chat-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(out.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return err
	}
	if err := syncDir(dir); err != nil {
		return err
	}
	file, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if s.file != nil {
		_ = s.file.Close()
	}
	s.file, s.size, s.threads = file, int64(out.Len()), next
	s.keys = map[idempotencyKey]uint64{}
	for id, th := range next {
		for _, m := range th.messages {
			s.keys[idempotencyKey{id, m.From, m.ClientID}] = m.Seq
		}
	}
	return nil
}

// Close releases the log file.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}
