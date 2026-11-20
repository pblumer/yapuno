// Package childsession issues short-lived sessions to guardian-approved child
// devices. A device logs in by signing a fresh server challenge with the
// non-extractable P-256 key bound during pairing; no password, phone number
// or long-lived bearer secret exists. Sessions are opaque and server-side,
// and every request re-checks that the device is still approved.
// See docs/adr/draft-child-device-sessions.md.
package childsession

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/pblumer/yapuno/internal/devicepairing"
)

const (
	CookieName = "__Host-yapuno_child"
	// ChallengeTTL bounds how long a login challenge may be answered.
	ChallengeTTL = time.Minute
	// MaxPendingChallenges per device; older ones are dropped.
	MaxPendingChallenges = 3
	// MaxSessionsPerDevice; the oldest session is evicted beyond this.
	MaxSessionsPerDevice = 5

	maxSessionTTL = 24 * time.Hour
	maxChallenges = 20000
	maxSessions   = 100000
	loginDomain   = "yapuno-device-login-v1\x00"
)

var (
	// ErrUnauthenticated is returned for every failed login or session check,
	// without saying which part failed.
	ErrUnauthenticated = errors.New("child device not authenticated")
	ErrInvalid         = errors.New("invalid device identifier")
	ErrBusy            = errors.New("too many pending logins")

	deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
	noncePattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

// Devices is the authoritative registry of paired devices.
type Devices interface {
	DeviceByID(deviceID string) (devicepairing.Device, bool)
	ActiveDevice(tenantID, childID, deviceID string) (devicepairing.Device, bool)
}

// Children confirms that a child profile still exists.
type Children interface {
	ChildName(tenantID, childID string) (string, bool)
}

// Session is the server-verified identity of one child on one device.
type Session struct {
	TenantID  string
	ChildID   string
	DeviceID  string
	CSRFToken string
}

// seq orders records by issue; timestamps can tie.
type challenge struct {
	deviceID string
	seq      uint64
	expires  time.Time
}

type record struct {
	session Session
	seq     uint64
	expires time.Time
}

// Manager is a single-process registry. Instances do not share challenges or
// sessions; a multi-instance deployment needs a shared, revocable backend.
type Manager struct {
	mu         sync.Mutex
	devices    Devices
	children   Children
	now        func() time.Time
	ttl        time.Duration
	seq        uint64
	challenges map[[32]byte]challenge
	sessions   map[[32]byte]record
}

func New(devices Devices, children Children, now func() time.Time, ttl time.Duration) (*Manager, error) {
	if devices == nil || children == nil || now == nil || ttl <= 0 || ttl > maxSessionTTL {
		return nil, errors.New("invalid child session policy")
	}
	return &Manager{devices: devices, children: children, now: now, ttl: ttl,
		challenges: make(map[[32]byte]challenge), sessions: make(map[[32]byte]record)}, nil
}

func secret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// Challenge returns a fresh single-use nonce for a well-formed device id. It
// answers the same way for unknown ids so it cannot be used to probe them.
func (m *Manager) Challenge(deviceID string) (string, error) {
	if !deviceIDPattern.MatchString(deviceID) {
		return "", ErrInvalid
	}
	nonce, err := secret()
	if err != nil {
		return "", err
	}
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	var oldest [32]byte
	var oldestSeq uint64
	pending := 0
	for key, c := range m.challenges {
		if !now.Before(c.expires) {
			delete(m.challenges, key)
			continue
		}
		if c.deviceID == deviceID {
			if pending == 0 || c.seq < oldestSeq {
				oldest, oldestSeq = key, c.seq
			}
			pending++
		}
	}
	if pending >= MaxPendingChallenges {
		delete(m.challenges, oldest)
	}
	if len(m.challenges) >= maxChallenges {
		return "", ErrBusy
	}
	m.seq++
	m.challenges[sha256.Sum256([]byte(nonce))] = challenge{deviceID: deviceID, seq: m.seq, expires: now.Add(ChallengeTTL)}
	return nonce, nil
}

func verify(device devicepairing.Device, deviceID, nonce string, signature []byte) bool {
	if len(signature) == 0 || len(signature) > 256 {
		return false
	}
	der, err := base64.RawURLEncoding.DecodeString(device.PublicKey)
	if err != nil || len(der) == 0 || len(der) > 512 {
		return false
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return false
	}
	key, ok := parsed.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		return false
	}
	// The signed message covers the device id and the server's nonce under a
	// login-specific domain, so pairing signatures cannot be replayed here.
	digest := sha256.Sum256([]byte(loginDomain + deviceID + "." + nonce))
	return ecdsa.VerifyASN1(key, digest[:], signature)
}

// Login consumes the challenge, verifies proof of possession of the paired
// device key and sets a new session cookie. Any failure burns the challenge.
func (m *Manager) Login(w http.ResponseWriter, deviceID, nonce string, signature []byte) error {
	if !deviceIDPattern.MatchString(deviceID) || !noncePattern.MatchString(nonce) {
		return ErrUnauthenticated
	}
	now := m.now()
	key := sha256.Sum256([]byte(nonce))
	m.mu.Lock()
	c, ok := m.challenges[key]
	delete(m.challenges, key)
	m.mu.Unlock()
	if !ok || c.deviceID != deviceID || !now.Before(c.expires) {
		return ErrUnauthenticated
	}
	device, ok := m.devices.DeviceByID(deviceID)
	if !ok || device.ID != deviceID || device.Revoked || device.TenantID == "" || device.ChildID == "" {
		return ErrUnauthenticated
	}
	if !verify(device, deviceID, nonce, signature) {
		return ErrUnauthenticated
	}
	if _, ok := m.children.ChildName(device.TenantID, device.ChildID); !ok {
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
	expires := now.Add(m.ttl)
	m.mu.Lock()
	var oldest [32]byte
	var oldestSeq uint64
	count := 0
	for k, r := range m.sessions {
		if !now.Before(r.expires) {
			delete(m.sessions, k)
			continue
		}
		if r.session.DeviceID == deviceID {
			if count == 0 || r.seq < oldestSeq {
				oldest, oldestSeq = k, r.seq
			}
			count++
		}
	}
	if count >= MaxSessionsPerDevice {
		delete(m.sessions, oldest)
	}
	if len(m.sessions) >= maxSessions {
		m.mu.Unlock()
		return ErrBusy
	}
	m.seq++
	m.sessions[sha256.Sum256([]byte(token))] = record{
		session: Session{TenantID: device.TenantID, ChildID: device.ChildID, DeviceID: deviceID, CSRFToken: csrf},
		seq:     m.seq, expires: expires,
	}
	m.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/", Secure: true,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Expires: expires, MaxAge: int(m.ttl.Seconds()),
	})
	return nil
}

func sessionKey(r *http.Request) ([32]byte, bool) {
	var values []string
	for _, cookie := range r.Cookies() {
		if cookie.Name == CookieName {
			values = append(values, cookie.Value)
		}
	}
	if len(values) != 1 || !noncePattern.MatchString(values[0]) {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(values[0])), true
}

// Authenticate resolves the session from server-side state only and confirms
// on every call that the device is still approved for the same child and
// that the child profile still exists.
func (m *Manager) Authenticate(r *http.Request) (Session, error) {
	key, ok := sessionKey(r)
	if !ok {
		return Session{}, ErrUnauthenticated
	}
	m.mu.Lock()
	entry, ok := m.sessions[key]
	if ok && !m.now().Before(entry.expires) {
		delete(m.sessions, key)
		ok = false
	}
	m.mu.Unlock()
	if !ok {
		return Session{}, ErrUnauthenticated
	}
	s := entry.session
	if _, active := m.devices.ActiveDevice(s.TenantID, s.ChildID, s.DeviceID); !active {
		m.mu.Lock()
		delete(m.sessions, key)
		m.mu.Unlock()
		return Session{}, ErrUnauthenticated
	}
	if _, exists := m.children.ChildName(s.TenantID, s.ChildID); !exists {
		return Session{}, ErrUnauthenticated
	}
	return s, nil
}

// Logout ends the presented session and clears the cookie.
func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	if key, ok := sessionKey(r); ok {
		m.mu.Lock()
		delete(m.sessions, key)
		m.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", Secure: true,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}
