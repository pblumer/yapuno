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
	"strings"

	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/guardianlogin"
)

var ErrUnavailable = errors.New("verified guardian directory unavailable")

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
type Directory struct {
	path     string
	families FamilyReader
}

func New(path string, families FamilyReader) (*Directory, error) {
	if path == "" || families == nil {
		return nil, ErrUnavailable
	}
	d := &Directory{path: path, families: families}
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
		if e.Email == normalized && e.Active && len(d.families.Cards(e.GuardianID, e.TenantID)) > 0 {
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
			return len(d.families.Cards(guardianID, tenantID)) > 0
		}
	}
	return false
}
