// Package messages stores one-to-one text messages between mutually approved
// contacts.
//
// Every send and every read is authorized again from current state: the
// stored contact, both child profiles and the approval of the device in use.
// A message is acknowledged only after it has been appended to the log and
// synced to disk. Retries are idempotent per sender and client id. Sequence
// numbers are assigned by the server per conversation and define the order.
// See docs/adr/draft-message-storage.md.
package messages

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/devicepairing"
)

var (
	// ErrForbidden covers every authorization failure, including unknown
	// conversations, so callers cannot probe which conversations exist.
	ErrForbidden   = errors.New("not allowed in this conversation")
	ErrInvalid     = errors.New("invalid message")
	ErrConflict    = errors.New("client id already used for a different message")
	ErrRateLimited = errors.New("too many messages")
	ErrUnavailable = errors.New("message could not be stored")
)

const (
	MaxBodyRunes = 1000
	MaxBodyBytes = 4 * MaxBodyRunes
	// MaxPerMinute limits one sender in one conversation.
	MaxPerMinute = 20
	// MaxPerConversation and Retention bound what is kept; older messages
	// are dropped at compaction. Both are provisional (issue #7).
	MaxPerConversation = 2000
	Retention          = 30 * 24 * time.Hour
	MaxPage            = 200

	maxLogBytes = 256 << 20
)

var clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// Principal is a child on one device, taken from a verified child session.
type Principal struct {
	Party    contacts.Party
	DeviceID string
}

// Contacts exposes stored consent records.
type Contacts interface {
	Grant(id string, self contacts.Party) (contacts.Party, authz.ContactGrant, bool)
	Exists(id string) bool
}

// Devices confirms current device approval.
type Devices interface {
	ActiveDevice(tenantID, childID, deviceID string) (devicepairing.Device, bool)
}

// Children confirms that a profile exists.
type Children interface {
	ChildName(tenantID, childID string) (string, bool)
}

// Message is one stored message. Seq orders messages within a conversation.
type Message struct {
	Seq      uint64
	From     contacts.Party
	ClientID string
	Body     string
	At       time.Time
}

// entry is one log line. A mark carries no message; it preserves the highest
// used sequence number of a conversation whose messages have all expired.
type entry struct {
	Conversation string    `json:"conversation"`
	Seq          uint64    `json:"seq"`
	TenantID     string    `json:"tenant_id,omitempty"`
	ChildID      string    `json:"child_id,omitempty"`
	ClientID     string    `json:"client_id,omitempty"`
	Body         string    `json:"body,omitempty"`
	At           time.Time `json:"at"`
	Mark         bool      `json:"mark,omitempty"`
}

type conversation struct {
	messages []Message // ascending by Seq
	last     uint64
}

type idempotencyKey struct {
	conversation string
	sender       contacts.Party
	clientID     string
}

// Store is single-writer per file; a transactional shared backend is needed
// for more than one server process.
type Store struct {
	mu       sync.Mutex
	path     string
	contacts Contacts
	devices  Devices
	children Children
	now      func() time.Time

	file  *os.File
	size  int64
	convs map[string]*conversation
	keys  map[idempotencyKey]uint64

	maxPerConversation int
	// syncFile forces written data to stable storage. It is a field so tests
	// can observe and fail it; production always uses (*os.File).Sync.
	syncFile  func(*os.File) error
	failWrite bool // test hook: the next append writes only part of its line
}

// Open loads the log. It fails closed on insecure permissions and on any
// complete line that is malformed or inconsistent. A partial final line is
// the trace of a crash during an unacknowledged append and is removed.
func Open(path string, c Contacts, d Devices, ch Children, now func() time.Time) (*Store, error) {
	if path == "" || c == nil || d == nil || ch == nil || now == nil {
		return nil, ErrInvalid
	}
	s := &Store{path: path, contacts: c, devices: d, children: ch, now: now,
		convs: map[string]*conversation{}, keys: map[idempotencyKey]uint64{}, maxPerConversation: MaxPerConversation,
		syncFile: (*os.File).Sync}
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
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	s.file = f
	if created {
		if err := syncDir(filepath.Dir(path)); err != nil {
			f.Close()
			return nil, err
		}
	}
	if err := s.compact(); err != nil {
		f.Close()
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
	if e.Conversation == "" || len(e.Conversation) > 128 || e.Seq == 0 {
		return fmt.Errorf("%w: log line", ErrInvalid)
	}
	conv := s.conv(e.Conversation)
	if e.Seq <= conv.last {
		return fmt.Errorf("%w: sequence does not increase", ErrInvalid)
	}
	conv.last = e.Seq
	if e.Mark {
		return nil
	}
	from := contacts.Party{TenantID: e.TenantID, ChildID: e.ChildID}
	body, ok := normalize(e.Body)
	if from.TenantID == "" || from.ChildID == "" || !clientIDPattern.MatchString(e.ClientID) || !ok || body != e.Body || e.At.IsZero() {
		return fmt.Errorf("%w: stored message", ErrInvalid)
	}
	key := idempotencyKey{e.Conversation, from, e.ClientID}
	if _, dup := s.keys[key]; dup {
		return fmt.Errorf("%w: duplicate client id", ErrInvalid)
	}
	s.keys[key] = e.Seq
	conv.messages = append(conv.messages, Message{Seq: e.Seq, From: from, ClientID: e.ClientID, Body: e.Body, At: e.At})
	return nil
}

func (s *Store) conv(id string) *conversation {
	c, ok := s.convs[id]
	if !ok {
		c = &conversation{}
		s.convs[id] = c
	}
	return c
}

// normalize returns the canonical stored form of a body: LF line endings,
// surrounding whitespace removed. It rejects empty or oversized text, invalid
// UTF-8, control characters other than newline and tab, and bidirectional
// override characters that can disguise what a message says.
func normalize(body string) (string, bool) {
	return Normalize(body)
}

// Normalize is the one rule for message text in every kind of conversation;
// see normalize.
func Normalize(body string) (string, bool) {
	if len(body) > 2*MaxBodyBytes || !utf8.ValidString(body) {
		return "", false
	}
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	if body == "" || len(body) > MaxBodyBytes || utf8.RuneCountInString(body) > MaxBodyRunes {
		return "", false
	}
	for _, r := range body {
		switch {
		case r == '\n' || r == '\t':
		case unicode.IsControl(r):
			return "", false
		case (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069):
			return "", false
		}
	}
	return body, true
}

// authorize loads the current state behind one attempt and applies the given
// predicate. It runs at the moment of every send and every read.
func (s *Store) authorize(p Principal, conversationID string, can func(authz.ConversationAccess) bool) bool {
	if conversationID == "" || p.DeviceID == "" || p.Party.TenantID == "" || p.Party.ChildID == "" {
		return false
	}
	peer, grant, ok := s.contacts.Grant(conversationID, p.Party)
	if !ok {
		return false
	}
	_, device := s.devices.ActiveDevice(p.Party.TenantID, p.Party.ChildID, p.DeviceID)
	_, self := s.children.ChildName(p.Party.TenantID, p.Party.ChildID)
	_, other := s.children.ChildName(peer.TenantID, peer.ChildID)
	return can(authz.ConversationAccess{
		Self: p.Party.Participant(), Peer: peer.Participant(),
		SelfEnabled: self, PeerEnabled: other, DeviceApproved: device, Contact: grant,
	})
}

// Send stores one message and returns it once it is durable. The second
// result is true when this client id was already stored for the same text;
// the original message is returned and nothing is written.
func (s *Store) Send(p Principal, conversationID, clientID, body string) (Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.authorize(p, conversationID, authz.CanSendMessage) {
		return Message{}, false, ErrForbidden
	}
	normalized, ok := normalize(body)
	if !ok || !clientIDPattern.MatchString(clientID) {
		return Message{}, false, ErrInvalid
	}
	conv := s.conv(conversationID)
	key := idempotencyKey{conversationID, p.Party, clientID}
	if seq, seen := s.keys[key]; seen {
		original, found := conv.find(seq)
		if !found || original.Body != normalized {
			return Message{}, false, ErrConflict
		}
		return original, true, nil
	}
	now := s.now()
	recent := 0
	for i := len(conv.messages) - 1; i >= 0 && conv.messages[i].At.After(now.Add(-time.Minute)); i-- {
		if conv.messages[i].From == p.Party {
			recent++
		}
	}
	if recent >= MaxPerMinute {
		return Message{}, false, ErrRateLimited
	}
	m := Message{Seq: conv.last + 1, From: p.Party, ClientID: clientID, Body: normalized, At: now.UTC()}
	if err := s.append(entry{Conversation: conversationID, Seq: m.Seq, TenantID: m.From.TenantID, ChildID: m.From.ChildID, ClientID: clientID, Body: normalized, At: m.At}); err != nil {
		return Message{}, false, ErrUnavailable
	}
	conv.last = m.Seq
	conv.messages = append(conv.messages, m)
	s.keys[key] = m.Seq
	if len(conv.messages) > s.maxPerConversation+s.maxPerConversation/4 {
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

func (c *conversation) find(seq uint64) (Message, bool) {
	i := sort.Search(len(c.messages), func(i int) bool { return c.messages[i].Seq >= seq })
	if i < len(c.messages) && c.messages[i].Seq == seq {
		return c.messages[i], true
	}
	return Message{}, false
}

func page(limit int) int {
	if limit <= 0 || limit > MaxPage {
		return MaxPage
	}
	return limit
}

// History returns up to limit messages with a sequence number above after,
// oldest first.
func (s *Store) History(p Principal, conversationID string, after uint64, limit int) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.authorize(p, conversationID, authz.CanReadConversation) {
		return nil, ErrForbidden
	}
	conv := s.conv(conversationID)
	start := sort.Search(len(conv.messages), func(i int) bool { return conv.messages[i].Seq > after })
	end := min(start+page(limit), len(conv.messages))
	return append([]Message(nil), conv.messages[start:end]...), nil
}

// CanRead reports whether the principal may read the conversation right now.
// It is the same check as for History and Recent and returns nothing else;
// push delivery asks it before telling a device that a message arrived.
func (s *Store) CanRead(p Principal, conversationID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authorize(p, conversationID, authz.CanReadConversation)
}

// Recent returns the newest messages, oldest first.
func (s *Store) Recent(p Principal, conversationID string, limit int) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.authorize(p, conversationID, authz.CanReadConversation) {
		return nil, ErrForbidden
	}
	conv := s.conv(conversationID)
	start := max(len(conv.messages)-page(limit), 0)
	return append([]Message(nil), conv.messages[start:]...), nil
}

// Compact drops expired messages, messages beyond the per-conversation cap
// and all messages of conversations whose contact no longer exists, then
// rewrites the log atomically.
func (s *Store) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.compact()
}

func (s *Store) compact() error {
	cutoff := s.now().Add(-Retention)
	ids := make([]string, 0, len(s.convs))
	for id := range s.convs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	next := map[string]*conversation{}
	changed := false
	var out bytes.Buffer
	for _, id := range ids {
		old := s.convs[id]
		if !s.contacts.Exists(id) {
			changed = changed || old.last > 0
			continue
		}
		kept := old.messages
		for len(kept) > 0 && !kept[0].At.After(cutoff) {
			kept = kept[1:]
		}
		if len(kept) > s.maxPerConversation {
			kept = kept[len(kept)-s.maxPerConversation:]
		}
		changed = changed || len(kept) != len(old.messages)
		next[id] = &conversation{messages: append([]Message(nil), kept...), last: old.last}
		if len(kept) == 0 && old.last > 0 {
			line, err := json.Marshal(entry{Conversation: id, Seq: old.last, Mark: true})
			if err != nil {
				return err
			}
			out.Write(append(line, '\n'))
		}
		for _, m := range kept {
			line, err := json.Marshal(entry{Conversation: id, Seq: m.Seq, TenantID: m.From.TenantID, ChildID: m.From.ChildID, ClientID: m.ClientID, Body: m.Body, At: m.At})
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
	tmp, err := os.CreateTemp(dir, ".yapuno-messages-*")
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
	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if s.file != nil {
		_ = s.file.Close()
	}
	s.file, s.size, s.convs = f, int64(out.Len()), next
	s.keys = map[idempotencyKey]uint64{}
	for id, c := range next {
		for _, m := range c.messages {
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
