// Package guardiansession manages opaque server-side guardian sessions.
// Session issuance is a privileged action following external identity proof.
//
// A session normally lives in this process only and ends with it. A guardian
// may ask to stay signed in on a device; such a remembered session is also
// written to a private file and survives a restart (see remember.go and
// docs/adr/draft-guardian-remembered-sessions.md).
package guardiansession

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/pblumer/yapuno/internal/familyweb"
)

const CookieName = "__Host-yapuno_session"

var ErrUnauthenticated = errors.New("guardian session not authenticated")

type record struct {
	guardianID string
	tenantID   string
	expires    time.Time
	// remembered sessions are the only ones written to the file.
	remembered bool
}

// Manager is a single-process session registry. Instances do not share sessions.
// A real multi-instance deployment must use a shared, revocable session backend.
type Manager struct {
	mu       sync.Mutex
	now      func() time.Time
	ttl      time.Duration
	sessions map[[32]byte]record

	// Set by Remember; empty path means no session is remembered.
	path        string
	rememberFor time.Duration
	failWrite   bool // test hook
}

func New(now func() time.Time, ttl time.Duration) (*Manager, error) {
	if now == nil || ttl <= 0 || ttl > 24*time.Hour {
		return nil, errors.New("invalid session policy")
	}
	return &Manager{now: now, ttl: ttl, sessions: make(map[[32]byte]record)}, nil
}

func secret() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}

// IssueVerified must be called ONLY after a trusted authentication provider
// verifies the guardian and tenant relationship. Never expose it as a
// self-enrolment endpoint or pass client-supplied IDs to it.
func (m *Manager) IssueVerified(w http.ResponseWriter, guardianID, tenantID string) error {
	if guardianID == "" || tenantID == "" {
		return ErrUnauthenticated
	}
	token, err := secret()
	if err != nil {
		return err
	}
	key := sha256.Sum256([]byte(token))
	expires := m.now().Add(m.ttl)
	m.mu.Lock()
	m.sessions[key] = record{guardianID: guardianID, tenantID: tenantID, expires: expires}
	m.mu.Unlock()
	setCookie(w, token, expires, m.ttl)
	return nil
}

func setCookie(w http.ResponseWriter, token string, expires time.Time, lifetime time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/", Secure: true,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Expires: expires, MaxAge: int(lifetime.Seconds()),
	})
}

// csrfToken derives the token that forms of a session carry from the session
// secret. It cannot be turned back into the secret, is different for every
// session, and needs no storage, so it is the same after a restart.
func csrfToken(token string) string {
	sum := sha256.Sum256([]byte("yapuno-guardian-csrf-v1\x00" + token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Authenticate implements familyweb.Authenticator. Identifiers are resolved
// exclusively from server-side state, never from HTTP headers or query values.
func (m *Manager) Authenticate(r *http.Request) (familyweb.Session, error) {
	var tokens []string
	for _, cookie := range r.Cookies() {
		if cookie.Name == CookieName {
			tokens = append(tokens, cookie.Value)
		}
	}
	if len(tokens) != 1 || tokens[0] == "" {
		return familyweb.Session{}, ErrUnauthenticated
	}
	key := sha256.Sum256([]byte(tokens[0]))
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.sessions[key]
	if !ok {
		return familyweb.Session{}, ErrUnauthenticated
	}
	if !m.now().Before(entry.expires) {
		delete(m.sessions, key)
		return familyweb.Session{}, ErrUnauthenticated
	}
	return familyweb.Session{GuardianID: entry.guardianID, TenantID: entry.tenantID, CSRFToken: csrfToken(tokens[0])}, nil
}

// Revoke invalidates the session immediately. A trusted logout handler should
// also clear the browser cookie with MaxAge=-1 and the same Secure/Path flags.
func (m *Manager) Revoke(r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := false
	for _, cookie := range r.Cookies() {
		if cookie.Name == CookieName {
			key := sha256.Sum256([]byte(cookie.Value))
			changed = changed || m.sessions[key].remembered
			delete(m.sessions, key)
		}
	}
	if changed {
		m.forget()
	}
}

// Count returns the number of unexpired sessions of one guardian.
func (m *Manager) Count(guardianID, tenantID string) int {
	if guardianID == "" || tenantID == "" {
		return 0
	}
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, entry := range m.sessions {
		if entry.guardianID == guardianID && entry.tenantID == tenantID && now.Before(entry.expires) {
			count++
		}
	}
	return count
}

// RevokeAll ends every session of one guardian, remembered ones included
// ("sign out everywhere"), and returns how many were ended. Callers must take guardian
// and tenant from a verified session. It does not prevent a new sign-in.
func (m *Manager) RevokeAll(guardianID, tenantID string) int {
	if guardianID == "" || tenantID == "" {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ended, changed := 0, false
	for key, entry := range m.sessions {
		if entry.guardianID == guardianID && entry.tenantID == tenantID {
			changed = changed || entry.remembered
			delete(m.sessions, key)
			ended++
		}
	}
	if changed {
		m.forget()
	}
	return ended
}

// Clear tells the browser to drop the session cookie. It does not end the
// server-side session; use Revoke or RevokeAll for that.
func (m *Manager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", Secure: true,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}
