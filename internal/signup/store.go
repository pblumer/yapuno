// Package signup implements invited self-registration: a signed-in guardian
// creates a one-time invitation, and whoever opens it registers their own
// family with an e-mail address they control.
//
// An invitation and a confirmed mailbox are not evidence of guardianship.
// Guardianship stays self-declared here, so this feature is switched off by
// default and is not sufficient for real children.
package signup

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid invitation request")
	ErrNotFound    = errors.New("invitation not found, expired or used")
	ErrLimit       = errors.New("invitation limit reached")
	ErrFull        = errors.New("no more families can register")
	ErrUnavailable = errors.New("invitation store unavailable")
)

const (
	// InvitationTTL is how long an unused invitation stays valid.
	InvitationTTL = 7 * 24 * time.Hour
	// MaxOpenPerGuardian bounds the unused invitations of one guardian.
	MaxOpenPerGuardian = 3
	// MaxMailsPerInvitation bounds the confirmation mails one invitation can
	// cause, whatever addresses are entered.
	MaxMailsPerInvitation = 5
	// MaxFamilies bounds how many families can register on one installation.
	MaxFamilies = 50
)

// Invitation is one invitation. It holds identifiers only: no address and no
// name. A used invitation stays as a record of which family invited which.
type Invitation struct {
	ID        string     `json:"id"`
	Digest    string     `json:"digest"`
	Guardian  string     `json:"guardian"`
	Tenant    string     `json:"tenant"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	Mails     int        `json:"mails,omitempty"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	Joined    string     `json:"joined_tenant,omitempty"`
}

type document struct {
	Invitations []Invitation `json:"invitations"`
}

// Store keeps invitations in a private file and persists every change with an
// atomic rename before acknowledging it. It must have a single writer.
type Store struct {
	mu        sync.Mutex
	path      string
	now       func() time.Time
	items     []Invitation
	failWrite bool // test hook
}

var (
	codePattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func validID(id string) bool {
	return id != "" && len(id) <= 128 && !strings.ContainsAny(id, "\x00\r\n")
}

// Open loads an existing private file or creates an empty one. A malformed,
// symlinked or group/world-accessible file is refused.
func Open(path string, now func() time.Time) (*Store, error) {
	if path == "" || now == nil {
		return nil, ErrInvalid
	}
	s := &Store{path: path, now: now}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if err := s.write(nil); err != nil {
			return nil, ErrUnavailable
		}
	}
	items, err := read(path)
	if err != nil {
		return nil, err
	}
	s.items = items
	return s, nil
}

func read(path string) ([]Invitation, error) {
	stat, err := os.Lstat(path)
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 {
		return nil, ErrUnavailable
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Mode().Perm()&0077 != 0 || !os.SameFile(stat, opened) {
		return nil, ErrUnavailable
	}
	content, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(content) > 1<<20 {
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
	ids, digests := map[string]bool{}, map[string]bool{}
	for _, inv := range doc.Invitations {
		if !validID(inv.ID) || !digestPattern.MatchString(inv.Digest) || !validID(inv.Guardian) || !validID(inv.Tenant) ||
			inv.CreatedAt.IsZero() || inv.ExpiresAt.IsZero() || inv.Mails < 0 || ids[inv.ID] || digests[inv.Digest] ||
			(inv.UsedAt == nil) != (inv.Joined == "") || (inv.Joined != "" && !validID(inv.Joined)) {
			return nil, ErrUnavailable
		}
		ids[inv.ID], digests[inv.Digest] = true, true
	}
	return doc.Invitations, nil
}

func (s *Store) write(items []Invitation) error {
	if s.failWrite {
		return errors.New("write failed")
	}
	if items == nil {
		items = []Invitation{}
	}
	data, err := json.Marshal(document{Invitations: items})
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	f, err := os.CreateTemp(dir, ".yapuno-signup-*")
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

// commit persists the next state and only then makes it current. Unused
// invitations that have expired are dropped on the way.
func (s *Store) commit(next []Invitation) error {
	now := s.now()
	kept := make([]Invitation, 0, len(next))
	for _, inv := range next {
		if inv.UsedAt == nil && !now.Before(inv.ExpiresAt) {
			continue
		}
		kept = append(kept, inv)
	}
	if err := s.write(kept); err != nil {
		return ErrUnavailable
	}
	s.items = kept
	return nil
}

func (s *Store) copyItems() []Invitation {
	return append([]Invitation(nil), s.items...)
}

func (s *Store) open(inv Invitation) bool {
	return inv.UsedAt == nil && s.now().Before(inv.ExpiresAt)
}

func (s *Store) used() int {
	n := 0
	for _, inv := range s.items {
		if inv.UsedAt != nil {
			n++
		}
	}
	return n
}

func randomText(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func digest(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// Invite creates an invitation for a guardian the caller has authenticated.
// The returned code is shown once; only its digest is stored.
func (s *Store) Invite(guardian, tenant string) (string, Invitation, error) {
	if !validID(guardian) || !validID(tenant) {
		return "", Invitation{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used() >= MaxFamilies {
		return "", Invitation{}, ErrFull
	}
	open := 0
	for _, inv := range s.items {
		if inv.Guardian == guardian && inv.Tenant == tenant && s.open(inv) {
			open++
		}
	}
	if open >= MaxOpenPerGuardian {
		return "", Invitation{}, ErrLimit
	}
	code, err := randomText(16)
	if err != nil {
		return "", Invitation{}, ErrUnavailable
	}
	id, err := randomText(12)
	if err != nil {
		return "", Invitation{}, ErrUnavailable
	}
	now := s.now()
	inv := Invitation{ID: id, Digest: digest(code), Guardian: guardian, Tenant: tenant, CreatedAt: now, ExpiresAt: now.Add(InvitationTTL)}
	if err := s.commit(append(s.copyItems(), inv)); err != nil {
		return "", Invitation{}, err
	}
	return code, inv, nil
}

// Pending lists the unused, unexpired invitations of one guardian, oldest first.
func (s *Store) Pending(guardian, tenant string) []Invitation {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Invitation
	for _, inv := range s.items {
		if inv.Guardian == guardian && inv.Tenant == tenant && s.open(inv) {
			out = append(out, inv)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Withdraw removes an unused invitation of the calling guardian.
func (s *Store) Withdraw(guardian, tenant, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]Invitation, 0, len(s.items))
	found := false
	for _, inv := range s.items {
		if inv.ID == id && inv.Guardian == guardian && inv.Tenant == tenant && s.open(inv) {
			found = true
			continue
		}
		next = append(next, inv)
	}
	if !found {
		return ErrNotFound
	}
	return s.commit(next)
}

// Lookup resolves the code from an invitation link. Unknown, malformed,
// expired and used codes are indistinguishable.
func (s *Store) Lookup(code string) (Invitation, error) {
	if !codePattern.MatchString(code) {
		return Invitation{}, ErrNotFound
	}
	want := digest(code)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, inv := range s.items {
		if inv.Digest == want && s.open(inv) {
			return inv, nil
		}
	}
	return Invitation{}, ErrNotFound
}

// Usable returns an invitation by id if it can still be used.
func (s *Store) Usable(id string) (Invitation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, inv := range s.items {
		if inv.ID == id && s.open(inv) {
			return inv, nil
		}
	}
	return Invitation{}, ErrNotFound
}

// CountMail charges one confirmation mail to the invitation's budget.
func (s *Store) CountMail(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyItems()
	for i := range next {
		if next[i].ID != id || !s.open(next[i]) {
			continue
		}
		if next[i].Mails >= MaxMailsPerInvitation {
			return ErrLimit
		}
		next[i].Mails++
		return s.commit(next)
	}
	return ErrNotFound
}

// Consume marks an invitation as used for the family that joins with it.
// Exactly one caller succeeds.
func (s *Store) Consume(id, joinedTenant string) error {
	if !validID(joinedTenant) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyItems()
	for i := range next {
		if next[i].ID != id || !s.open(next[i]) {
			continue
		}
		if s.used() >= MaxFamilies {
			return ErrFull
		}
		now := s.now()
		next[i].UsedAt, next[i].Joined = &now, joinedTenant
		return s.commit(next)
	}
	return ErrNotFound
}

// Restore reopens an invitation whose registration could not be completed.
func (s *Store) Restore(id, joinedTenant string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyItems()
	for i := range next {
		if next[i].ID == id && next[i].UsedAt != nil && next[i].Joined == joinedTenant {
			next[i].UsedAt, next[i].Joined = nil, ""
			return s.commit(next)
		}
	}
	return ErrNotFound
}

// Registered is how many families have joined through an invitation.
func (s *Store) Registered() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.used()
}
