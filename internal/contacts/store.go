// Package contacts persists guardian-approved contact relationships between
// two children, who may belong to different family tenants.
//
// Flow: a guardian of child A creates a one-time invitation code and passes it
// to the other family out of band. A guardian of child B redeems it for B
// (approval of B's side). A guardian of A then confirms the named request
// (approval of A's side). Only then is the contact active. Either family can
// end a pending request or an active contact at any time.
//
// There is no directory or search: a relationship can only start from a code.
// Callers MUST pass guardian and tenant identity from a verified server-side
// session; every operation re-checks the guardian's current grant.
// See docs/adr/draft-contact-invitations.md.
package contacts

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
)

var (
	ErrInvalid    = errors.New("invalid contact request")
	ErrForbidden  = errors.New("guardian cannot approve contacts for this child")
	ErrExpired    = errors.New("invitation or request expired")
	ErrConsumed   = errors.New("invitation already used")
	ErrExists     = errors.New("a request or contact already exists for these children")
	ErrNotFound   = errors.New("contact request not found")
	ErrNotPending = errors.New("contact request is not pending")
	ErrLimit      = errors.New("too many open invitations")
)

const (
	// InvitationTTL bounds how long a code can be redeemed. Unlike device
	// pairing, the two families are usually not in the same room.
	InvitationTTL = 24 * time.Hour
	// RequestTTL bounds how long a redeemed request waits for confirmation.
	RequestTTL = 7 * 24 * time.Hour
	// MaxOpenInvitations limits unredeemed codes per child.
	MaxOpenInvitations = 5

	codeBytes  = 16 // 128 bits; online guessing is infeasible
	codeLength = 26 // base32 without padding
	maxRecords = 20000
	maxFile    = 8 << 20
)

var codeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// Party is one tenant-qualified child.
type Party struct {
	TenantID string `json:"tenant_id"`
	ChildID  string `json:"child_id"`
}

func (p Party) valid() bool { return p.TenantID != "" && p.ChildID != "" }

// Participant expresses the child as a tenant-qualified chat participant.
func (p Party) Participant() authz.Participant {
	return authz.Participant{TenantID: p.TenantID, ID: p.ChildID, Kind: authz.ParticipantChild}
}

// Families is the authoritative source for guardian grants and child names.
type Families interface {
	Cards(guardianID, tenantID string) []familydashboard.ChildCard
	ChildName(tenantID, childID string) (string, bool)
}

type invitation struct {
	Digest   string    `json:"digest"`
	Inviter  Party     `json:"inviter"`
	Expires  time.Time `json:"expires"`
	Consumed bool      `json:"consumed"`
}

// link is a redeemed invitation. The invitee's side is approved by redeeming;
// Active records the inviter's confirmation of this specific counterpart.
type link struct {
	ID      string    `json:"id"`
	Inviter Party     `json:"inviter"`
	Invitee Party     `json:"invitee"`
	Active  bool      `json:"active"`
	Expires time.Time `json:"expires"` // pending requests only
	// A child may block an active contact. Each side records its own block;
	// only a guardian of the blocking child lifts it.
	InviterBlocked bool `json:"inviter_blocked,omitempty"`
	InviteeBlocked bool `json:"invitee_blocked,omitempty"`
}

func (l link) blocked() bool { return l.InviterBlocked || l.InviteeBlocked }

// blockedBy reports whether this party itself blocked the relationship.
func (l link) blockedBy(p Party) bool {
	return (l.Inviter == p && l.InviterBlocked) || (l.Invitee == p && l.InviteeBlocked)
}

// grant expresses the link as the consent record the authz predicates expect.
// The invitee approved by redeeming; the inviter approves by confirming.
func (l link) grant() authz.ContactGrant {
	return authz.ContactGrant{
		First: l.Inviter.Participant(), Second: l.Invitee.Participant(),
		FirstApproved: l.Active, SecondApproved: true, Active: l.Active,
		Blocked: l.blocked(),
	}
}

func (l link) involves(p Party) bool { return l.Inviter == p || l.Invitee == p }

func (l link) other(p Party) Party {
	if l.Inviter == p {
		return l.Invitee
	}
	return l.Inviter
}

func (l link) joins(a, b Party) bool {
	return (l.Inviter == a && l.Invitee == b) || (l.Inviter == b && l.Invitee == a)
}

type state struct {
	Invitations []invitation `json:"invitations"`
	Links       []link       `json:"links"`
}

// Store is single-writer per file; a shared transactional backend is required
// for a horizontally scaled production server.
type Store struct {
	mu       sync.Mutex
	path     string
	families Families
	now      func() time.Time
	state    state
}

// Open loads or prepares the store. It fails closed on insecure permissions,
// corrupt content, unknown fields and inconsistent records.
func Open(path string, families Families, now func() time.Time) (*Store, error) {
	if path == "" || families == nil || now == nil {
		return nil, ErrInvalid
	}
	s := &Store{path: path, families: families, now: now}
	stat, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 || stat.Size() > maxFile {
		return nil, fmt.Errorf("%w: store file must be a private regular file", ErrInvalid)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s.state); err != nil {
		return nil, fmt.Errorf("%w: decode contact store", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, inv := range s.state.Invitations {
		if len(inv.Digest) != sha256.Size*2 || !inv.Inviter.valid() || seen[inv.Digest] {
			return nil, fmt.Errorf("%w: invitation record", ErrInvalid)
		}
		seen[inv.Digest] = true
	}
	for i, l := range s.state.Links {
		if l.ID == "" || !l.Inviter.valid() || !l.Invitee.valid() || l.Inviter == l.Invitee || seen["link:"+l.ID] {
			return nil, fmt.Errorf("%w: link record", ErrInvalid)
		}
		seen["link:"+l.ID] = true
		for _, earlier := range s.state.Links[:i] {
			if earlier.joins(l.Inviter, l.Invitee) {
				return nil, fmt.Errorf("%w: duplicate relationship", ErrInvalid)
			}
		}
	}
	return s, nil
}

// allowed re-checks the guardian's current contact authority for one child.
func (s *Store) allowed(guardian string, p Party) bool {
	if guardian == "" || !p.valid() {
		return false
	}
	for _, c := range s.families.Cards(guardian, p.TenantID) {
		if c.ID == p.ChildID && c.Permissions.ApproveContacts {
			return true
		}
	}
	return false
}

func (s *Store) exists(p Party) bool {
	_, ok := s.families.ChildName(p.TenantID, p.ChildID)
	return ok
}

// live reports whether a link still counts: active, or pending and unexpired.
func (s *Store) live(l link) bool {
	return l.Active || s.now().Before(l.Expires)
}

func normalize(code string) (string, bool) {
	code = strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "\t", "").Replace(code))
	if len(code) != codeLength {
		return "", false
	}
	raw, err := codeEncoding.DecodeString(code)
	if err != nil || len(raw) != codeBytes {
		return "", false
	}
	return code, true
}

func digest(code string) string {
	sum := sha256.Sum256([]byte("yapuno-contact-invite-v1\x00" + code))
	return hex.EncodeToString(sum[:])
}

func format(code string) string {
	var parts []string
	for len(code) > 5 {
		parts = append(parts, code[:5])
		code = code[5:]
	}
	return strings.Join(append(parts, code), "-")
}

// pruned drops expired invitations and expired pending requests. Used
// invitations are kept until they would have expired, so a second redeemer
// learns that the code was already used.
func (s *Store) pruned() state {
	next := state{}
	for _, inv := range s.state.Invitations {
		if s.now().Before(inv.Expires) {
			next.Invitations = append(next.Invitations, inv)
		}
	}
	for _, l := range s.state.Links {
		if s.live(l) {
			next.Links = append(next.Links, l)
		}
	}
	return next
}

// Invite creates a one-time code for one child. The code is returned once and
// only its digest is stored.
func (s *Store) Invite(guardian, tenant, child string) (string, error) {
	inviter := Party{TenantID: tenant, ChildID: child}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(guardian, inviter) {
		return "", ErrForbidden
	}
	next := s.pruned()
	open := 0
	for _, inv := range next.Invitations {
		if inv.Inviter == inviter && !inv.Consumed {
			open++
		}
	}
	if open >= MaxOpenInvitations || len(next.Invitations)+len(next.Links) >= maxRecords {
		return "", ErrLimit
	}
	raw := make([]byte, codeBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	code := codeEncoding.EncodeToString(raw)
	next.Invitations = append(next.Invitations, invitation{Digest: digest(code), Inviter: inviter, Expires: s.now().Add(InvitationTTL)})
	if err := s.commit(next); err != nil {
		return "", err
	}
	return format(code), nil
}

// Redeem records the invitee family's approval and consumes the code. The
// contact stays inactive until the inviting family confirms it.
func (s *Store) Redeem(guardian, tenant, child, code string) (string, error) {
	invitee := Party{TenantID: tenant, ChildID: child}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(guardian, invitee) {
		return "", ErrForbidden
	}
	normalized, ok := normalize(code)
	if !ok {
		return "", ErrInvalid
	}
	want := digest(normalized)
	index := -1
	for i, inv := range s.state.Invitations {
		if inv.Digest == want {
			index = i
		}
	}
	if index < 0 {
		return "", ErrInvalid
	}
	inv := s.state.Invitations[index]
	if inv.Consumed {
		return "", ErrConsumed
	}
	if !s.now().Before(inv.Expires) {
		return "", ErrExpired
	}
	if inv.Inviter == invitee || !s.exists(inv.Inviter) {
		return "", ErrInvalid
	}
	for _, l := range s.state.Links {
		if l.joins(inv.Inviter, invitee) && s.live(l) {
			return "", ErrExists
		}
	}
	rawID := make([]byte, 16)
	if _, err := rand.Read(rawID); err != nil {
		return "", err
	}
	id := hex.EncodeToString(rawID)
	next := state{Invitations: append([]invitation(nil), s.state.Invitations...)}
	next.Invitations[index].Consumed = true
	for _, l := range s.state.Links {
		if s.live(l) {
			next.Links = append(next.Links, l)
		}
	}
	if len(next.Invitations)+len(next.Links) >= maxRecords {
		return "", ErrLimit
	}
	next.Links = append(next.Links, link{ID: id, Inviter: inv.Inviter, Invitee: invitee, Expires: s.now().Add(RequestTTL)})
	if err := s.commit(next); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) find(id string) int {
	if id == "" {
		return -1
	}
	for i, l := range s.state.Links {
		if l.ID == id {
			return i
		}
	}
	return -1
}

// Confirm activates a pending request. Only a guardian of the inviting child
// may confirm; any equal guardian of that child qualifies.
func (s *Store) Confirm(guardian, tenant, child, id string) error {
	party := Party{TenantID: tenant, ChildID: child}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(guardian, party) {
		return ErrForbidden
	}
	i := s.find(id)
	if i < 0 {
		return ErrNotFound
	}
	l := s.state.Links[i]
	if l.Inviter != party {
		return ErrForbidden
	}
	if l.Active {
		return ErrNotPending
	}
	if !s.now().Before(l.Expires) {
		return ErrExpired
	}
	if !s.exists(l.Invitee) {
		return ErrInvalid
	}
	next := state{Invitations: s.state.Invitations, Links: append([]link(nil), s.state.Links...)}
	next.Links[i].Active = true
	next.Links[i].Expires = time.Time{}
	return s.commit(next)
}

// Remove ends a pending request or an active contact. A guardian of either
// child may do so; the record is deleted.
func (s *Store) Remove(guardian, tenant, child, id string) error {
	party := Party{TenantID: tenant, ChildID: child}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(guardian, party) {
		return ErrForbidden
	}
	i := s.find(id)
	if i < 0 {
		return ErrNotFound
	}
	if !s.state.Links[i].involves(party) {
		return ErrForbidden
	}
	next := state{Invitations: s.state.Invitations}
	next.Links = append(next.Links, s.state.Links[:i]...)
	next.Links = append(next.Links, s.state.Links[i+1:]...)
	return s.commit(next)
}

// Block suspends one of the child's own active contacts for both children.
// It performs no guardian check: the caller must have authenticated exactly
// this child. Every failure looks the same, so ids cannot be probed. Blocking
// again is harmless. Only a guardian of this child can lift the block.
func (s *Store) Block(p Party, id string) error {
	if !p.valid() {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.find(id)
	if i < 0 || !s.exists(p) {
		return ErrNotFound
	}
	l := s.state.Links[i]
	if !l.Active || !l.involves(p) {
		return ErrNotFound
	}
	if l.blockedBy(p) {
		return nil
	}
	next := state{Invitations: s.state.Invitations, Links: append([]link(nil), s.state.Links...)}
	if l.Inviter == p {
		next.Links[i].InviterBlocked = true
	} else {
		next.Links[i].InviteeBlocked = true
	}
	return s.commit(next)
}

// Unblock lifts the block that this child set. A guardian with current
// contact authority for this child may do so; a block set by the other child
// is not touched.
func (s *Store) Unblock(guardian, tenant, child, id string) error {
	party := Party{TenantID: tenant, ChildID: child}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(guardian, party) {
		return ErrForbidden
	}
	i := s.find(id)
	if i < 0 || !s.state.Links[i].involves(party) || !s.state.Links[i].blockedBy(party) {
		return ErrNotFound
	}
	next := state{Invitations: s.state.Invitations, Links: append([]link(nil), s.state.Links...)}
	if next.Links[i].Inviter == party {
		next.Links[i].InviterBlocked = false
	} else {
		next.Links[i].InviteeBlocked = false
	}
	return s.commit(next)
}

// Entry is one relationship as shown to a guardian. It deliberately carries
// no tenant or child identifier of the other family.
type Entry struct {
	ID   string
	Name string
}

// Overview lists one child's relationships for an authorized guardian.
type Overview struct {
	Contacts        []Entry // active and not blocked
	Blocked         []Entry // blocked by this child; a guardian of this child may lift it
	Paused          []Entry // blocked by the other child only
	Incoming        []Entry // redeemed by the other family, awaiting our confirmation
	Outgoing        []Entry // redeemed by us, awaiting the other family
	OpenInvitations int
}

func (s *Store) Overview(guardian, tenant, child string) (Overview, error) {
	party := Party{TenantID: tenant, ChildID: child}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(guardian, party) {
		return Overview{}, ErrForbidden
	}
	var view Overview
	for _, inv := range s.state.Invitations {
		if inv.Inviter == party && !inv.Consumed && s.now().Before(inv.Expires) {
			view.OpenInvitations++
		}
	}
	for _, l := range s.state.Links {
		if !l.involves(party) || !s.live(l) {
			continue
		}
		other := l.other(party)
		name, ok := s.families.ChildName(other.TenantID, other.ChildID)
		if !ok {
			continue
		}
		entry := Entry{ID: l.ID, Name: name}
		switch {
		case l.Active && l.blockedBy(party):
			view.Blocked = append(view.Blocked, entry)
		case l.Active && l.blocked():
			view.Paused = append(view.Paused, entry)
		case l.Active:
			view.Contacts = append(view.Contacts, entry)
		case l.Inviter == party:
			view.Incoming = append(view.Incoming, entry)
		default:
			view.Outgoing = append(view.Outgoing, entry)
		}
	}
	for _, list := range [][]Entry{view.Contacts, view.Blocked, view.Paused, view.Incoming, view.Outgoing} {
		sort.Slice(list, func(i, j int) bool { return list[i].Name+list[i].ID < list[j].Name+list[j].ID })
	}
	return view, nil
}

// ContactsOf lists the active, unblocked contacts of one child by display
// name. It performs no guardian check: the caller must have authenticated
// this exact child (for example through a child device session).
func (s *Store) ContactsOf(p Party) []Entry {
	return s.listFor(p, func(l link) bool { return !l.blocked() })
}

// BlockedBy lists the contacts this child has blocked itself. Like
// ContactsOf it is only for the authenticated child. A child never learns
// from it that somebody else blocked them.
func (s *Store) BlockedBy(p Party) []Entry {
	return s.listFor(p, func(l link) bool { return l.blockedBy(p) })
}

func (s *Store) listFor(p Party, keep func(link) bool) []Entry {
	if !p.valid() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.exists(p) {
		return nil
	}
	var out []Entry
	for _, l := range s.state.Links {
		if !l.Active || !l.involves(p) || !keep(l) {
			continue
		}
		other := l.other(p)
		if name, ok := s.families.ChildName(other.TenantID, other.ChildID); ok {
			out = append(out, Entry{ID: l.ID, Name: name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name+out[i].ID < out[j].Name+out[j].ID })
	return out
}

// Exists reports whether a relationship with this id is still stored (active,
// or pending and unexpired). It reveals nothing about who is involved and is
// meant for housekeeping, such as purging messages of ended contacts.
func (s *Store) Exists(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.find(id)
	return i >= 0 && s.live(s.state.Links[i])
}

// Grant returns the stored consent record of one relationship and the other
// party, but only when self is one of its two parties. It does not decide
// anything: callers apply authz.CanSendMessage or authz.CanReadConversation.
// An expired pending request is reported as absent.
func (s *Store) Grant(id string, self Party) (Party, authz.ContactGrant, bool) {
	if !self.valid() {
		return Party{}, authz.ContactGrant{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.find(id)
	if i < 0 {
		return Party{}, authz.ContactGrant{}, false
	}
	l := s.state.Links[i]
	if !l.involves(self) || !s.live(l) {
		return Party{}, authz.ContactGrant{}, false
	}
	return l.other(self), l.grant(), true
}

// Connection is one established contact of a child, seen from that child.
type Connection struct {
	Peer  Party
	Grant authz.ContactGrant
}

// Connections lists the established contacts of one child: approved by both
// families and not ended. Unlike ContactsOf it includes contacts a child has
// blocked, marked in the grant, because a block pauses the children's chat
// without ending the connection between the families. Requests that are not
// confirmed yet are not connections. It is not an authorization check:
// callers decide from the grants what the connection allows.
func (s *Store) Connections(p Party) []Connection {
	if !p.valid() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.exists(p) {
		return nil
	}
	var out []Connection
	for _, l := range s.state.Links {
		if !l.Active || !l.involves(p) || !s.exists(l.other(p)) {
			continue
		}
		out = append(out, Connection{Peer: l.other(p), Grant: l.grant()})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Peer.TenantID+"\x00"+out[i].Peer.ChildID < out[j].Peer.TenantID+"\x00"+out[j].Peer.ChildID
	})
	return out
}

// Allowed reports whether two children are mutually approved contacts right
// now. It authorizes the relationship only: device approval, sender status
// and per-operation checks remain separate (see authz.CanContact).
func (s *Store) Allowed(a, b Party) bool {
	if !a.valid() || !b.valid() || a == b {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.state.Links {
		if !l.joins(a, b) {
			continue
		}
		return authz.CanContact(l.grant(), a.Participant(), b.Participant()) && s.exists(a) && s.exists(b)
	}
	return false
}

func (s *Store) commit(next state) error {
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	f, err := os.CreateTemp(dir, ".yapuno-contacts-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
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
	if d, err := os.Open(dir); err == nil {
		defer d.Close()
		if err := d.Sync(); err != nil {
			return err
		}
	}
	s.state = next
	return nil
}
