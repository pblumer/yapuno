// Package guardiandirectory resolves pre-verified guardian email identities.
// An identity is admitted only when it has a current, child-scoped store grant.
package guardiandirectory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/guardianlogin"
)

var (
	ErrUnavailable = errors.New("verified guardian directory unavailable")
	ErrInvalid     = errors.New("invalid guardian identity")
	ErrExists      = errors.New("guardian identity already exists")
)

type Entry struct {
	Email      string `json:"email"`
	GuardianID string `json:"guardian_id"`
	TenantID   string `json:"tenant_id"`
	Active     bool   `json:"active"`
}

type document struct {
	Guardians []Entry `json:"guardians"`
}

type FamilyReader interface {
	Cards(guardianID, tenantID string) []familydashboard.ChildCard
}

// Directory reloads its private, operator-provisioned file on every lookup,
// including authorization checks for existing sessions. A missing, malformed,
// symlinked or incorrectly permissioned file fails closed.
// maxFileSize bounds the directory file for reading and for writing.
const maxFileSize = 1 << 20

type Directory struct {
	path     string
	families FamilyReader
	locks    LockReader
	// writes serializes Register and Unregister in this process. Copies made
	// by WithLocks share it.
	writes *sync.Mutex
}

// LockReader reports emergency locks of guardian access. A locked guardian
// can neither sign in nor keep an existing session until the lock is lifted.
type LockReader interface {
	Locked(guardianID, tenantID string) bool
}

// NewWithLocks is New with an emergency-lock check on every lookup.
func NewWithLocks(path string, families FamilyReader, locks LockReader) (*Directory, error) {
	d, err := New(path, families)
	if err != nil {
		return nil, err
	}
	return d.WithLocks(locks)
}

// WithLocks returns a directory over the same file that additionally denies
// locked guardians. The receiver is left unchanged.
func (d *Directory) WithLocks(locks LockReader) (*Directory, error) {
	if locks == nil {
		return nil, ErrUnavailable
	}
	return &Directory{path: d.path, families: d.families, locks: locks, writes: d.writes}, nil
}

func (d *Directory) locked(guardianID, tenantID string) bool {
	return d.locks != nil && d.locks.Locked(guardianID, tenantID)
}

func New(path string, families FamilyReader) (*Directory, error) {
	if path == "" || families == nil {
		return nil, ErrUnavailable
	}
	d := &Directory{path: path, families: families, writes: &sync.Mutex{}}
	if _, err := d.read(); err != nil {
		return nil, err
	}
	return d, nil
}

func normalize(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if len(email) == 0 || len(email) > 254 || strings.ContainsAny(email, "\r\n\x00") {
		return "", false
	}
	addr, err := mail.ParseAddress(email)
	return email, err == nil && addr.Name == "" && addr.Address == email && strings.Count(email, "@") == 1
}

func (d *Directory) read() ([]Entry, error) {
	stat, err := os.Lstat(d.path)
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 {
		return nil, ErrUnavailable
	}
	f, err := os.Open(d.path)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer f.Close()
	// Check the opened inode as well as the path: swapping in an unsafe file
	// between Lstat and Open must not silently bypass basic file policy.
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Mode().Perm()&0077 != 0 || !os.SameFile(stat, opened) {
		return nil, ErrUnavailable
	}
	content, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil || len(content) > maxFileSize {
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
	seenEmail := make(map[string]bool)
	seenGuardian := make(map[string]bool)
	for _, e := range doc.Guardians {
		normalized, ok := normalize(e.Email)
		if !ok || normalized != e.Email || e.GuardianID == "" || e.TenantID == "" || strings.ContainsAny(e.GuardianID+e.TenantID, "\x00\r\n") {
			return nil, ErrUnavailable
		}
		key := e.TenantID + "\x00" + e.GuardianID
		if seenEmail[normalized] || seenGuardian[key] {
			// Multiple tenants using the same mailbox need explicit workspace
			// selection and must not be resolved by arbitrary first-match.
			return nil, ErrUnavailable
		}
		seenEmail[normalized] = true
		seenGuardian[key] = true
	}
	return doc.Guardians, nil
}

// LookupGuardian implements guardianlogin.Directory. Unknown and inactive
// identities are treated equally. No account is created by email possession.
func (d *Directory) LookupGuardian(ctx context.Context, email string) (guardianlogin.Principal, error) {
	if err := ctx.Err(); err != nil {
		return guardianlogin.Principal{}, err
	}
	normalized, ok := normalize(email)
	if !ok || normalized != email {
		return guardianlogin.Principal{}, nil
	}
	entries, err := d.read()
	if err != nil {
		return guardianlogin.Principal{}, err
	}
	for _, e := range entries {
		if e.Email == normalized && e.Active && !d.locked(e.GuardianID, e.TenantID) && len(d.families.Cards(e.GuardianID, e.TenantID)) > 0 {
			return guardianlogin.Principal{Email: e.Email, GuardianID: e.GuardianID, TenantID: e.TenantID, Active: true}, nil
		}
	}
	return guardianlogin.Principal{}, nil
}

// ActiveGuardian is checked on *every* family request, so disabling the
// identity file entry or revoking its last child grant immediately denies it.
func (d *Directory) ActiveGuardian(ctx context.Context, guardianID, tenantID string) bool {
	if ctx.Err() != nil || guardianID == "" || tenantID == "" {
		return false
	}
	entries, err := d.read()
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Active && e.GuardianID == guardianID && e.TenantID == tenantID {
			return !d.locked(guardianID, tenantID) && len(d.families.Cards(guardianID, tenantID)) > 0
		}
	}
	return false
}

// Email returns the verified address of an active, unlocked guardian who
// still has a child grant. It exists to send security notices; never show
// the address to another guardian.
func (d *Directory) Email(ctx context.Context, guardianID, tenantID string) (string, bool) {
	if ctx.Err() != nil || guardianID == "" || tenantID == "" {
		return "", false
	}
	entries, err := d.read()
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.Active && e.GuardianID == guardianID && e.TenantID == tenantID {
			if d.locked(guardianID, tenantID) || len(d.families.Cards(guardianID, tenantID)) == 0 {
				return "", false
			}
			return e.Email, true
		}
	}
	return "", false
}

// Has reports whether an address is already in the directory, active or not.
// An unreadable directory counts as "in use", so nothing is registered on top
// of a file that cannot be trusted.
func (d *Directory) Has(email string) bool {
	normalized, ok := normalize(email)
	if !ok {
		return true
	}
	entries, err := d.read()
	if err != nil {
		return true
	}
	for _, e := range entries {
		if e.Email == normalized {
			return true
		}
	}
	return false
}

func (d *Directory) write(entries []Entry) error {
	data, err := json.Marshal(document{Guardians: entries})
	if err != nil {
		return err
	}
	// read refuses larger files, and an unreadable directory locks everyone out.
	if len(data)+1 > maxFileSize {
		return errors.New("guardian directory is full")
	}
	dir := filepath.Dir(d.path)
	f, err := os.CreateTemp(dir, ".yapuno-guardians-*")
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
	if err := os.Rename(f.Name(), d.path); err != nil {
		return err
	}
	if handle, err := os.Open(dir); err == nil {
		defer handle.Close()
		return handle.Sync()
	}
	return nil
}

// Register adds an active guardian identity. It is a trusted call: the
// caller must have proven control of the address with a one-time link and
// must be allowed to create this guardian. The identity is admitted for
// sign-in only once it also holds a child grant in the family store.
func (d *Directory) Register(email, guardianID, tenantID string) error {
	normalized, ok := normalize(email)
	if !ok || guardianID == "" || tenantID == "" || strings.ContainsAny(guardianID+tenantID, "\x00\r\n") {
		return ErrInvalid
	}
	d.writes.Lock()
	defer d.writes.Unlock()
	entries, err := d.read()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Email == normalized || (e.TenantID == tenantID && e.GuardianID == guardianID) {
			return ErrExists
		}
	}
	next := append(append([]Entry{}, entries...), Entry{Email: normalized, GuardianID: guardianID, TenantID: tenantID, Active: true})
	if err := d.write(next); err != nil {
		return ErrUnavailable
	}
	return nil
}

// RemoveTenant deletes the identities of every guardian of one family and
// returns how many there were. Their addresses can register again afterwards.
// It is a trusted call for the deletion of a family: the caller must have
// authorized it and must already have removed the family itself, because an
// identity without a family cannot sign in anyway, while a family without
// identities could never be deleted by its guardians.
func (d *Directory) RemoveTenant(tenantID string) (int, error) {
	if tenantID == "" || strings.ContainsAny(tenantID, "\x00\r\n") {
		return 0, ErrInvalid
	}
	d.writes.Lock()
	defer d.writes.Unlock()
	entries, err := d.read()
	if err != nil {
		return 0, err
	}
	next := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.TenantID != tenantID {
			next = append(next, e)
		}
	}
	removed := len(entries) - len(next)
	if removed == 0 {
		return 0, nil
	}
	if err := d.write(next); err != nil {
		return 0, ErrUnavailable
	}
	return removed, nil
}
