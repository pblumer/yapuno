// Package reports stores reports that a child files about one message of
// another child. A report holds a copy of exactly that message and is shown
// only to guardians of the reporting child. Guardians have no other way to
// read messages.
//
// The child's right to report is the child's right to read: the message is
// fetched through the message store with the child's own principal, so
// contact, profile and device state are checked at that moment.
package reports

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/messages"
)

var (
	// ErrForbidden covers every authorization failure.
	ErrForbidden   = errors.New("not allowed to report or to see reports here")
	ErrInvalid     = errors.New("this message cannot be reported")
	ErrNotFound    = errors.New("message or report not found")
	ErrLimit       = errors.New("too many open reports")
	ErrUnavailable = errors.New("report could not be stored")
)

const (
	// MaxOpenPerChild bounds what one child can pile up for its guardians.
	MaxOpenPerChild = 20
	// Retention bounds how long the copy of a reported message is kept when
	// no guardian dismisses the report. Provisional (issue #7).
	Retention = 30 * 24 * time.Hour

	maxRecords = 20000
	maxFile    = 32 << 20
)

// Messages reads messages with full authorization for the given principal.
type Messages interface {
	History(p messages.Principal, conversationID string, after uint64, limit int) ([]messages.Message, error)
}

// Families is the authoritative source for guardian grants and child names.
type Families interface {
	Cards(guardianID, tenantID string) []familydashboard.ChildCard
	ChildName(tenantID, childID string) (string, bool)
}

type record struct {
	ID           string         `json:"id"`
	Reporter     contacts.Party `json:"reporter"`
	Conversation string         `json:"conversation"`
	Seq          uint64         `json:"seq"`
	Sender       contacts.Party `json:"sender"`
	// SenderName is the name as the reporting family knew it at that time.
	SenderName string    `json:"sender_name"`
	Body       string    `json:"body"`
	SentAt     time.Time `json:"sent_at"`
	ReportedAt time.Time `json:"reported_at"`
}

type document struct {
	Reports []record `json:"reports"`
}

// Report is what a guardian of the reporting child sees. It carries no
// identifier of the other family.
type Report struct {
	ID         string
	From       string
	Body       string
	SentAt     time.Time
	ReportedAt time.Time
}

// Store is single-writer per file.
type Store struct {
	mu        sync.Mutex
	path      string
	messages  Messages
	families  Families
	now       func() time.Time
	records   []record
	failWrite bool // test hook
}

func validParty(p contacts.Party) bool { return p.TenantID != "" && p.ChildID != "" }

// Open loads or prepares the store. It fails closed on insecure permissions,
// corrupt content, unknown fields and inconsistent records.
func Open(path string, msgs Messages, families Families, now func() time.Time) (*Store, error) {
	if path == "" || msgs == nil || families == nil || now == nil {
		return nil, ErrInvalid
	}
	s := &Store{path: path, messages: msgs, families: families, now: now}
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
	var doc document
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: decode report store", ErrInvalid)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing data", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, r := range doc.Reports {
		key := r.Reporter.TenantID + "\x00" + r.Reporter.ChildID + "\x00" + r.Conversation + "\x00" + fmt.Sprint(r.Seq)
		if len(r.ID) != 32 || !validParty(r.Reporter) || !validParty(r.Sender) || r.Reporter == r.Sender ||
			r.Conversation == "" || r.Seq == 0 || r.ReportedAt.IsZero() || seen["id:"+r.ID] || seen[key] {
			return nil, fmt.Errorf("%w: report record", ErrInvalid)
		}
		seen["id:"+r.ID], seen[key] = true, true
	}
	s.records = doc.Reports
	return s, nil
}

func (s *Store) live(r record) bool { return s.now().Before(r.ReportedAt.Add(Retention)) }

// allowed re-checks the guardian's current contact authority for one child.
func (s *Store) allowed(guardian string, child contacts.Party) bool {
	if guardian == "" || !validParty(child) {
		return false
	}
	for _, c := range s.families.Cards(guardian, child.TenantID) {
		if c.ID == child.ChildID && c.Permissions.ApproveContacts {
			return true
		}
	}
	return false
}

func (s *Store) commit(next []record) error {
	if s.failWrite {
		return errors.New("write failed")
	}
	if next == nil {
		next = []record{}
	}
	data, err := json.Marshal(document{Reports: next})
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	f, err := os.CreateTemp(dir, ".yapuno-reports-*")
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
	if dirHandle, err := os.Open(dir); err == nil {
		defer dirHandle.Close()
		if err := dirHandle.Sync(); err != nil {
			return err
		}
	}
	s.records = next
	return nil
}

// File stores a report about one message. The principal must come from a
// verified child session. Only a message the child can read right now, sent
// by the other child, can be reported. The second result is true when a new
// report was stored; reporting the same message again changes nothing.
func (s *Store) File(p messages.Principal, conversationID string, seq uint64) (bool, error) {
	if seq == 0 {
		// Still authorize, so the answer does not depend on the number alone.
		if _, err := s.messages.History(p, conversationID, 0, 1); err != nil {
			return false, ErrForbidden
		}
		return false, ErrNotFound
	}
	found, err := s.messages.History(p, conversationID, seq-1, 1)
	if err != nil {
		return false, ErrForbidden
	}
	if len(found) != 1 || found[0].Seq != seq {
		return false, ErrNotFound
	}
	m := found[0]
	if m.From == p.Party || !validParty(m.From) {
		return false, ErrInvalid
	}
	// The contact exists at this moment, which justifies recording the name.
	name, _ := s.families.ChildName(m.From.TenantID, m.From.ChildID)
	id, err := randomID()
	if err != nil {
		return false, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]record, 0, len(s.records)+1)
	open := 0
	for _, r := range s.records {
		if !s.live(r) {
			continue
		}
		if r.Reporter == p.Party {
			if r.Conversation == conversationID && r.Seq == seq {
				return false, nil
			}
			open++
		}
		next = append(next, r)
	}
	if open >= MaxOpenPerChild || len(next) >= maxRecords {
		return false, ErrLimit
	}
	next = append(next, record{
		ID: id, Reporter: p.Party, Conversation: conversationID, Seq: seq, Sender: m.From,
		SenderName: name, Body: m.Body, SentAt: m.At.UTC(), ReportedAt: s.now().UTC(),
	})
	if err := s.commit(next); err != nil {
		return false, ErrUnavailable
	}
	return true, nil
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// ForChild lists the open reports of one child, newest first, for a guardian
// with current contact authority for that child.
func (s *Store) ForChild(guardian, tenant, child string) ([]Report, error) {
	party := contacts.Party{TenantID: tenant, ChildID: child}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(guardian, party) {
		return nil, ErrForbidden
	}
	var mine []record
	for _, r := range s.records {
		if r.Reporter == party && s.live(r) {
			mine = append(mine, r)
		}
	}
	sort.SliceStable(mine, func(i, j int) bool {
		if !mine[i].ReportedAt.Equal(mine[j].ReportedAt) {
			return mine[i].ReportedAt.After(mine[j].ReportedAt)
		}
		if !mine[i].SentAt.Equal(mine[j].SentAt) {
			return mine[i].SentAt.After(mine[j].SentAt)
		}
		return mine[i].Seq > mine[j].Seq
	})
	var out []Report
	for _, r := range mine {
		out = append(out, Report{ID: r.ID, From: r.SenderName, Body: r.Body, SentAt: r.SentAt, ReportedAt: r.ReportedAt})
	}
	return out, nil
}

// Count returns the number of open reports of one child for an authorized
// guardian, and zero for everybody else.
func (s *Store) Count(guardian, tenant, child string) int {
	list, err := s.ForChild(guardian, tenant, child)
	if err != nil {
		return 0
	}
	return len(list)
}

// Dismiss deletes one report of this child, including the message copy.
func (s *Store) Dismiss(guardian, tenant, child, id string) error {
	party := contacts.Party{TenantID: tenant, ChildID: child}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(guardian, party) {
		return ErrForbidden
	}
	next := make([]record, 0, len(s.records))
	found := false
	for _, r := range s.records {
		if !s.live(r) {
			continue
		}
		if r.ID == id && r.Reporter == party {
			found = true
			continue
		}
		next = append(next, r)
	}
	if !found {
		return ErrNotFound
	}
	if err := s.commit(next); err != nil {
		return ErrUnavailable
	}
	return nil
}

// Reported returns the sequence numbers this child has already reported in
// one conversation. The caller must have authenticated exactly this child.
func (s *Store) Reported(p contacts.Party, conversationID string) map[uint64]bool {
	out := map[uint64]bool{}
	if !validParty(p) || conversationID == "" {
		return out
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.records {
		if r.Reporter == p && r.Conversation == conversationID && s.live(r) {
			out[r.Seq] = true
		}
	}
	return out
}
