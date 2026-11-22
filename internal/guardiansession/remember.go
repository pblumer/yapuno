package guardiansession

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// ErrNotRemembering is returned when no file for remembered sessions is
// configured. Callers then issue an ordinary session or none.
var ErrNotRemembering = errors.New("sessions are not remembered on this installation")

const (
	// MaxRememberedFor bounds how long a session may be remembered at all.
	MaxRememberedFor = 31 * 24 * time.Hour
	// MaxRememberedPerGuardian bounds the devices on which one guardian
	// stays signed in. A further sign-in ends the oldest of them.
	MaxRememberedPerGuardian = 10

	maxRemembered     = 10000
	maxRememberedFile = 4 << 20
)

type storedSession struct {
	Digest   string    `json:"digest"`
	Guardian string    `json:"guardian_id"`
	Tenant   string    `json:"tenant_id"`
	Expires  time.Time `json:"expires"`
}

type storedSessions struct {
	Sessions []storedSession `json:"sessions"`
}

var storedDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Remember makes the manager keep sessions issued with IssueRemembered in a
// private file for ttl, so that they survive a restart. The file holds a
// digest of each session secret, never the secret or a CSRF token. It must
// have a single writer. Call it once, before the manager is used.
func (m *Manager) Remember(path string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if path == "" || m.path != "" || ttl <= m.ttl || ttl > MaxRememberedFor {
		return errors.New("invalid policy for remembered sessions")
	}
	loaded, err := readRemembered(path)
	if err != nil {
		return err
	}
	now := m.now()
	for _, entry := range loaded {
		// Expired sessions are dropped. So is one that would outlive what the
		// current policy allows: a shortened policy or an edited file must
		// not grant more than a new sign-in would.
		if !now.Before(entry.Expires) || entry.Expires.After(now.Add(ttl)) {
			continue
		}
		var key [32]byte
		raw, _ := hex.DecodeString(entry.Digest)
		copy(key[:], raw)
		m.sessions[key] = record{guardianID: entry.Guardian, tenantID: entry.Tenant, expires: entry.Expires, remembered: true}
	}
	m.path, m.rememberFor = path, ttl
	// Proves at start that the file can be written, and creates it.
	if err := m.persist(); err != nil {
		m.path, m.rememberFor = "", 0
		return errors.New("file for remembered sessions cannot be written")
	}
	return nil
}

// Remembers reports whether IssueRemembered can work.
func (m *Manager) Remembers() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.path != ""
}

// Lifetime is how long an ordinary session lasts.
func (m *Manager) Lifetime() time.Duration { return m.ttl }

// RememberedFor is how long a remembered session lasts; zero if none is.
func (m *Manager) RememberedFor() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rememberFor
}

func readRemembered(path string) ([]storedSession, error) {
	stat, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	refused := errors.New("file for remembered sessions is unavailable, not private or malformed")
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 || stat.Size() > maxRememberedFile {
		return nil, refused
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, refused
	}
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.DisallowUnknownFields()
	var doc storedSessions
	if err := dec.Decode(&doc); err != nil {
		return nil, refused
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, refused
	}
	seen := map[string]bool{}
	for _, entry := range doc.Sessions {
		if !storedDigest.MatchString(entry.Digest) || entry.Guardian == "" || entry.Tenant == "" || entry.Expires.IsZero() || seen[entry.Digest] {
			return nil, refused
		}
		seen[entry.Digest] = true
	}
	return doc.Sessions, nil
}

// persist writes all unexpired remembered sessions. The caller holds m.mu.
func (m *Manager) persist() error {
	if m.failWrite {
		return errors.New("write failed")
	}
	now := m.now()
	doc := storedSessions{Sessions: []storedSession{}}
	for key, entry := range m.sessions {
		if entry.remembered && now.Before(entry.expires) {
			doc.Sessions = append(doc.Sessions, storedSession{Digest: hex.EncodeToString(key[:]), Guardian: entry.guardianID, Tenant: entry.tenantID, Expires: entry.expires})
		}
	}
	sort.Slice(doc.Sessions, func(i, j int) bool { return doc.Sessions[i].Digest < doc.Sessions[j].Digest })
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.path)
	f, err := os.CreateTemp(dir, ".yapuno-sessions-*")
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
	if err := os.Rename(f.Name(), m.path); err != nil {
		return err
	}
	if handle, err := os.Open(dir); err == nil {
		defer handle.Close()
		return handle.Sync()
	}
	return nil
}

// forget makes the end of a remembered session durable. The session is gone
// from memory already. If the file cannot be rewritten it is removed, so that
// an ended session can never return with a restart; the price is that every
// remembered session then ends with the next restart. The caller holds m.mu.
func (m *Manager) forget() {
	if m.path == "" {
		return
	}
	if m.persist() == nil {
		return
	}
	if err := os.Remove(m.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("guardian sessions: an ended session could not be removed from disk; remove the session file before the next start")
		return
	}
	log.Printf("guardian sessions: the session file could not be rewritten and was removed; remembered sessions end with the next restart")
}

// IssueRemembered is IssueVerified for a guardian who asked to stay signed
// in on this device. The same rule applies: call it ONLY after the guardian
// was verified. The session is on disk before the cookie is set.
func (m *Manager) IssueRemembered(w http.ResponseWriter, guardianID, tenantID string) error {
	if guardianID == "" || tenantID == "" {
		return ErrUnauthenticated
	}
	token, err := secret()
	if err != nil {
		return err
	}
	key := sha256.Sum256([]byte(token))
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.path == "" {
		return ErrNotRemembering
	}
	now := m.now()
	type old struct {
		key     [32]byte
		expires time.Time
	}
	var own []old
	total := 0
	for k, entry := range m.sessions {
		if !entry.remembered || !now.Before(entry.expires) {
			continue
		}
		total++
		if entry.guardianID == guardianID && entry.tenantID == tenantID {
			own = append(own, old{k, entry.expires})
		}
	}
	if total >= maxRemembered {
		return errors.New("too many remembered sessions")
	}
	// The oldest sign-ins of this guardian make room for the new one.
	sort.Slice(own, func(i, j int) bool { return own[i].expires.Before(own[j].expires) })
	evicted := map[[32]byte]record{}
	for len(own) >= MaxRememberedPerGuardian {
		evicted[own[0].key] = m.sessions[own[0].key]
		delete(m.sessions, own[0].key)
		own = own[1:]
	}
	expires := now.Add(m.rememberFor)
	m.sessions[key] = record{guardianID: guardianID, tenantID: tenantID, expires: expires, remembered: true}
	if err := m.persist(); err != nil {
		// Nothing was acknowledged: put everything back as it was.
		delete(m.sessions, key)
		for k, entry := range evicted {
			m.sessions[k] = entry
		}
		return errors.New("remembered session could not be stored")
	}
	setCookie(w, token, expires, m.rememberFor)
	return nil
}
