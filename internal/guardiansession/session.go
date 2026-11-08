// Package guardiansession manages opaque server-side guardian sessions.
// Session issuance is a privileged action following external identity proof.
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
	csrfToken  string
	expires    time.Time
}

// Manager is a single-process session registry. Instances do not share sessions.
// A real multi-instance deployment must use a shared, revocable session backend.
type Manager struct {
	mu       sync.Mutex
	now      func() time.Time
	ttl      time.Duration
	sessions map[[32]byte]record
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
	csrf, err := secret()
	if err != nil {
		return err
	}
	key := sha256.Sum256([]byte(token))
	expires := m.now().Add(m.ttl)
	m.mu.Lock()
	m.sessions[key] = record{guardianID: guardianID, tenantID: tenantID, csrfToken: csrf, expires: expires}
	m.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/", Secure: true,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Expires: expires, MaxAge: int(m.ttl.Seconds()),
	})
	return nil
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
	return familyweb.Session{GuardianID: entry.guardianID, TenantID: entry.tenantID, CSRFToken: entry.csrfToken}, nil
}

// Revoke invalidates the session immediately. A trusted logout handler should
// also clear the browser cookie with MaxAge=-1 and the same Secure/Path flags.
func (m *Manager) Revoke(r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cookie := range r.Cookies() {
		if cookie.Name == CookieName {
			delete(m.sessions, sha256.Sum256([]byte(cookie.Value)))
		}
	}
}
