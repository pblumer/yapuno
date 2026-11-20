package childsession

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type device struct {
	id  string
	key *ecdsa.PrivateKey
}

type world struct {
	families *familystore.Store
	pairing  *devicepairing.Store
	sessions *Manager
	clock    *clock
}

func signASN1(t *testing.T, key *ecdsa.PrivateKey, message string) []byte {
	t.Helper()
	digest := sha256.Sum256([]byte(message))
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// pair runs the real guardian-approved pairing flow and returns the device.
func (w *world) pair(t *testing.T, guardian, tenant, child string) device {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	token, err := w.pairing.Start(guardian, tenant, child)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := w.pairing.Claim(token, "Test phone", der, signASN1(t, key, "yapuno-pair-claim-v1\x00"+token))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.pairing.Approve(guardian, tenant, child, token, fingerprint); err != nil {
		t.Fatal(err)
	}
	nonce, err := w.pairing.Nonce(token)
	if err != nil {
		t.Fatal(err)
	}
	paired, err := w.pairing.Finish(token, signASN1(t, key, "yapuno-pair-finish-v1\x00"+nonce))
	if err != nil {
		t.Fatal(err)
	}
	return device{id: paired.ID, key: key}
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	families, err := familystore.Open(filepath.Join(dir, "families.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, tenant, guardian, name string }{
		{"lea", "family-a", "anna", "Lea"}, {"mia", "family-b", "bruno", "Mia"},
	} {
		grant := authz.GuardianGrant{GuardianID: c.guardian, ChildID: c.id, TenantID: c.tenant, Active: true, CanManageDevices: true, CanApproveContacts: true}
		if err := families.Bootstrap(familydashboard.Child{ID: c.id, TenantID: c.tenant, DisplayName: c.name}, grant); err != nil {
			t.Fatal(err)
		}
	}
	c := &clock{t: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	pairing, err := devicepairing.Open(filepath.Join(dir, "pairing.json"), families, c.now)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := New(pairing, families, c.now, 12*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &world{families: families, pairing: pairing, sessions: sessions, clock: c}
}

func proof(t *testing.T, d device, nonce string) []byte {
	t.Helper()
	return signASN1(t, d.key, "yapuno-device-login-v1\x00"+d.id+"."+nonce)
}

// login performs a full challenge-response login and returns the cookie.
func (w *world) login(t *testing.T, d device) *http.Cookie {
	t.Helper()
	nonce, err := w.sessions.Challenge(d.id)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := w.sessions.Login(rec, d.id, nonce, proof(t, d, nonce)); err != nil {
		t.Fatalf("login: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	return cookies[0]
}

func request(cookies ...*http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "https://app.example.org/child", nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return r
}

func TestLoginIssuesKeyBoundSession(t *testing.T) {
	w := newWorld(t)
	d := w.pair(t, "anna", "family-a", "lea")
	cookie := w.login(t, d)
	if cookie.Name != CookieName || !strings.HasPrefix(cookie.Name, "__Host-") || !cookie.Secure || !cookie.HttpOnly ||
		cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge != int((12*time.Hour).Seconds()) {
		t.Fatalf("unsafe session cookie: %+v", cookie)
	}
	s, err := w.sessions.Authenticate(request(cookie))
	if err != nil {
		t.Fatal(err)
	}
	if s.TenantID != "family-a" || s.ChildID != "lea" || s.DeviceID != d.id || len(s.CSRFToken) < 32 {
		t.Fatalf("unexpected session: %+v", s)
	}
	if cookie.Value == s.CSRFToken {
		t.Fatal("CSRF token equals the session secret")
	}
}

func TestLoginRejectsInvalidProofs(t *testing.T) {
	w := newWorld(t)
	lea := w.pair(t, "anna", "family-a", "lea")
	mia := w.pair(t, "bruno", "family-b", "mia")
	stranger, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(nonce string) (deviceID string, sig []byte){
		"signed by another key": func(n string) (string, []byte) {
			return lea.id, signASN1(t, stranger, "yapuno-device-login-v1\x00"+lea.id+"."+n)
		},
		"signed by another paired device": func(n string) (string, []byte) {
			return lea.id, signASN1(t, mia.key, "yapuno-device-login-v1\x00"+lea.id+"."+n)
		},
		"signature over another nonce": func(n string) (string, []byte) {
			return lea.id, proof(t, lea, strings.Repeat("A", 43))
		},
		"pairing-domain signature reused": func(n string) (string, []byte) {
			return lea.id, signASN1(t, lea.key, "yapuno-pair-finish-v1\x00"+n)
		},
		"device id not covered": func(n string) (string, []byte) {
			return lea.id, signASN1(t, lea.key, "yapuno-device-login-v1\x00"+n)
		},
		"challenge of another device": func(n string) (string, []byte) {
			return mia.id, proof(t, mia, n)
		},
		"empty signature":   func(n string) (string, []byte) { return lea.id, nil },
		"garbage signature": func(n string) (string, []byte) { return lea.id, []byte("not asn1") },
	}
	for name, attack := range cases {
		t.Run(name, func(t *testing.T) {
			nonce, err := w.sessions.Challenge(lea.id)
			if err != nil {
				t.Fatal(err)
			}
			deviceID, sig := attack(nonce)
			rec := httptest.NewRecorder()
			if err := w.sessions.Login(rec, deviceID, nonce, sig); !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("got %v", err)
			}
			if len(rec.Result().Cookies()) != 0 {
				t.Fatal("cookie issued for a rejected login")
			}
			// A failed attempt burns the nonce, even for the legitimate device.
			if err := w.sessions.Login(httptest.NewRecorder(), lea.id, nonce, proof(t, lea, nonce)); !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("nonce survived a failed attempt: %v", err)
			}
		})
	}
}

func TestChallengeIsSingleUseAndExpires(t *testing.T) {
	w := newWorld(t)
	d := w.pair(t, "anna", "family-a", "lea")
	nonce, err := w.sessions.Challenge(d.id)
	if err != nil {
		t.Fatal(err)
	}
	sig := proof(t, d, nonce)
	if err := w.sessions.Login(httptest.NewRecorder(), d.id, nonce, sig); err != nil {
		t.Fatal(err)
	}
	if err := w.sessions.Login(httptest.NewRecorder(), d.id, nonce, sig); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("captured login replayed: %v", err)
	}
	stale, err := w.sessions.Challenge(d.id)
	if err != nil {
		t.Fatal(err)
	}
	w.clock.advance(ChallengeTTL)
	if err := w.sessions.Login(httptest.NewRecorder(), d.id, stale, proof(t, d, stale)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired challenge accepted: %v", err)
	}
	if err := w.sessions.Login(httptest.NewRecorder(), d.id, strings.Repeat("B", 43), proof(t, d, strings.Repeat("B", 43))); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("self-chosen nonce accepted: %v", err)
	}
}

func TestOnlyNewestChallengesPerDeviceStayValid(t *testing.T) {
	w := newWorld(t)
	d := w.pair(t, "anna", "family-a", "lea")
	var nonces []string
	for i := 0; i < MaxPendingChallenges+1; i++ {
		n, err := w.sessions.Challenge(d.id)
		if err != nil {
			t.Fatal(err)
		}
		nonces = append(nonces, n)
	}
	if err := w.sessions.Login(httptest.NewRecorder(), d.id, nonces[0], proof(t, d, nonces[0])); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("oldest challenge still valid: %v", err)
	}
	last := nonces[len(nonces)-1]
	if err := w.sessions.Login(httptest.NewRecorder(), d.id, last, proof(t, d, last)); err != nil {
		t.Fatalf("newest challenge rejected: %v", err)
	}
}

func TestUnknownAndMalformedDevices(t *testing.T) {
	w := newWorld(t)
	d := w.pair(t, "anna", "family-a", "lea")
	// Unknown but well-formed ids get a challenge like any other, so the
	// endpoint does not reveal which device ids exist.
	unknown := strings.Repeat("A", len(d.id))
	nonce, err := w.sessions.Challenge(unknown)
	if err != nil {
		t.Fatalf("challenge for unknown device: %v", err)
	}
	if err := w.sessions.Login(httptest.NewRecorder(), unknown, nonce, proof(t, device{id: unknown, key: d.key}, nonce)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unknown device logged in: %v", err)
	}
	for _, bad := range []string{"", "short", d.id + "A", "../../etc/passwd", strings.Repeat("!", len(d.id)), d.id[:len(d.id)-1] + "\n"} {
		if _, err := w.sessions.Challenge(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("challenge for %q: %v", bad, err)
		}
		if err := w.sessions.Login(httptest.NewRecorder(), bad, nonce, []byte("x")); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("login for %q: %v", bad, err)
		}
	}
}

func TestRevokedDeviceCannotLogInAndLosesItsSession(t *testing.T) {
	w := newWorld(t)
	d := w.pair(t, "anna", "family-a", "lea")
	other := w.pair(t, "anna", "family-a", "lea")
	cookie, otherCookie := w.login(t, d), w.login(t, other)
	pending, err := w.sessions.Challenge(d.id)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.pairing.Revoke("anna", "family-a", "lea", d.id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.sessions.Authenticate(request(cookie)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("session survived device revocation: %v", err)
	}
	if err := w.sessions.Login(httptest.NewRecorder(), d.id, pending, proof(t, d, pending)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked device logged in: %v", err)
	}
	// The child's other device is unaffected.
	if _, err := w.sessions.Authenticate(request(otherCookie)); err != nil {
		t.Fatalf("unrelated device lost its session: %v", err)
	}
}

func TestSessionExpiresAndCookiesAreValidatedStrictly(t *testing.T) {
	w := newWorld(t)
	d := w.pair(t, "anna", "family-a", "lea")
	cookie := w.login(t, d)
	forged := &http.Cookie{Name: CookieName, Value: strings.Repeat("A", len(cookie.Value))}
	for name, r := range map[string]*http.Request{
		"no cookie":        request(),
		"forged cookie":    request(forged),
		"empty cookie":     request(&http.Cookie{Name: CookieName, Value: ""}),
		"two cookies":      request(cookie, forged),
		"guardian cookie":  request(&http.Cookie{Name: "__Host-yapuno_session", Value: cookie.Value}),
		"truncated cookie": request(&http.Cookie{Name: CookieName, Value: cookie.Value[:10]}),
	} {
		if _, err := w.sessions.Authenticate(r); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s: %v", name, err)
		}
	}
	w.clock.advance(12*time.Hour - time.Second)
	if _, err := w.sessions.Authenticate(request(cookie)); err != nil {
		t.Fatalf("session ended early: %v", err)
	}
	w.clock.advance(time.Second)
	if _, err := w.sessions.Authenticate(request(cookie)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired session accepted: %v", err)
	}
}

func TestLogoutEndsOnlyThatSession(t *testing.T) {
	w := newWorld(t)
	d := w.pair(t, "anna", "family-a", "lea")
	first, second := w.login(t, d), w.login(t, d)
	if first.Value == second.Value {
		t.Fatal("two logins share a session secret")
	}
	rec := httptest.NewRecorder()
	w.sessions.Logout(rec, request(first))
	cleared := rec.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != CookieName || cleared[0].MaxAge >= 0 || !cleared[0].Secure || cleared[0].Path != "/" {
		t.Fatalf("cookie not cleared safely: %+v", cleared)
	}
	if _, err := w.sessions.Authenticate(request(first)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("session usable after logout")
	}
	if _, err := w.sessions.Authenticate(request(second)); err != nil {
		t.Fatalf("logout ended another session: %v", err)
	}
}

func TestSessionsAreIsolatedBetweenChildren(t *testing.T) {
	w := newWorld(t)
	lea := w.pair(t, "anna", "family-a", "lea")
	mia := w.pair(t, "bruno", "family-b", "mia")
	a, err := w.sessions.Authenticate(request(w.login(t, lea)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := w.sessions.Authenticate(request(w.login(t, mia)))
	if err != nil {
		t.Fatal(err)
	}
	if a.TenantID != "family-a" || a.ChildID != "lea" || b.TenantID != "family-b" || b.ChildID != "mia" || a.CSRFToken == b.CSRFToken {
		t.Fatalf("sessions mixed up: %+v %+v", a, b)
	}
}

type vanishing struct {
	Children
	gone map[string]bool
}

func (v vanishing) ChildName(tenantID, childID string) (string, bool) {
	if v.gone[tenantID+"/"+childID] {
		return "", false
	}
	return v.Children.ChildName(tenantID, childID)
}

func TestSessionEndsWhenChildProfileIsRemoved(t *testing.T) {
	w := newWorld(t)
	d := w.pair(t, "anna", "family-a", "lea")
	gone := map[string]bool{}
	sessions, err := New(w.pairing, vanishing{Children: w.families, gone: gone}, w.clock.now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	w.sessions = sessions
	cookie := w.login(t, d)
	gone["family-a/lea"] = true
	if _, err := sessions.Authenticate(request(cookie)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("session for a removed child: %v", err)
	}
	nonce, _ := sessions.Challenge(d.id)
	if err := sessions.Login(httptest.NewRecorder(), d.id, nonce, proof(t, d, nonce)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("login for a removed child: %v", err)
	}
}

func TestOldestSessionsOfADeviceAreEvicted(t *testing.T) {
	w := newWorld(t)
	d := w.pair(t, "anna", "family-a", "lea")
	var cookies []*http.Cookie
	for i := 0; i < MaxSessionsPerDevice+1; i++ {
		cookies = append(cookies, w.login(t, d))
	}
	if _, err := w.sessions.Authenticate(request(cookies[0])); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("oldest session not evicted")
	}
	if _, err := w.sessions.Authenticate(request(cookies[len(cookies)-1])); err != nil {
		t.Fatalf("newest session evicted: %v", err)
	}
}

func TestNewRejectsUnsafeConfiguration(t *testing.T) {
	w := newWorld(t)
	for name, build := range map[string]func() (*Manager, error){
		"no devices":  func() (*Manager, error) { return New(nil, w.families, w.clock.now, time.Hour) },
		"no children": func() (*Manager, error) { return New(w.pairing, nil, w.clock.now, time.Hour) },
		"no clock":    func() (*Manager, error) { return New(w.pairing, w.families, nil, time.Hour) },
		"zero ttl":    func() (*Manager, error) { return New(w.pairing, w.families, w.clock.now, 0) },
		"week ttl":    func() (*Manager, error) { return New(w.pairing, w.families, w.clock.now, 7*24*time.Hour) },
	} {
		if _, err := build(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestConcurrentLoginsWithOneChallengeHaveOneWinner(t *testing.T) {
	w := newWorld(t)
	d := w.pair(t, "anna", "family-a", "lea")
	nonce, err := w.sessions.Challenge(d.id)
	if err != nil {
		t.Fatal(err)
	}
	sig := proof(t, d, nonce)
	results := make([]error, 8)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = w.sessions.Login(httptest.NewRecorder(), d.id, nonce, sig)
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("%d logins succeeded with one challenge", wins)
	}
}
