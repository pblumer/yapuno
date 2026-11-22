// Package devicepairing persists guardian-approved device bindings.
//
// A device that wants to be paired asks for a short code and shows it. A
// guardian reads the code off that device and enters it for one of their
// children; the device then proves possession of its key and is bound. The
// code is what ties the guardian's decision to the physical device in their
// hand, so it must only ever be read from that device.
package devicepairing

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/yapuno/internal/familydashboard"
)

var (
	ErrInvalid   = errors.New("invalid pairing request")
	ErrForbidden = errors.New("guardian cannot manage child device")
	ErrExpired   = errors.New("pairing challenge expired")
	ErrNotReady  = errors.New("pairing challenge is not ready")
	ErrConsumed  = errors.New("pairing challenge already used")
	// ErrNotFound means a well-formed code that no open request carries.
	ErrNotFound = errors.New("no open pairing request has this code")
	// ErrRateLimited means this guardian entered too many unknown codes.
	ErrRateLimited = errors.New("too many unknown pairing codes")
	// ErrBusy means too many devices are waiting for approval right now.
	ErrBusy = errors.New("too many open pairing requests")
)

const (
	challengeTTL = 5 * time.Minute
	// maxPending bounds requests that wait for a guardian. Anybody can ask
	// for a code, so this also bounds how many codes are live at once.
	maxPending = 200
	// maxWrongCodes unknown codes per guardian within wrongCodeWindow.
	maxWrongCodes   = 5
	wrongCodeWindow = 10 * time.Minute
)

// A code is five random digits and a check digit.
var codePattern = regexp.MustCompile(`^[0-9]{6}$`)

// damm is the quasigroup of the Damm algorithm. Its check digit detects
// every single wrong digit and every swap of two neighbouring digits, so a
// mistyped code is never another device's code.
var damm = [10][10]byte{
	{0, 3, 1, 7, 5, 9, 8, 6, 4, 2},
	{7, 0, 9, 2, 1, 5, 4, 8, 6, 3},
	{4, 2, 0, 6, 8, 7, 1, 3, 5, 9},
	{1, 7, 5, 0, 9, 8, 3, 4, 2, 6},
	{6, 1, 2, 3, 0, 4, 5, 9, 7, 8},
	{3, 6, 7, 4, 2, 0, 9, 5, 8, 1},
	{5, 8, 6, 9, 7, 2, 0, 1, 3, 4},
	{8, 9, 4, 5, 3, 6, 2, 0, 1, 7},
	{9, 4, 3, 8, 6, 1, 7, 2, 0, 5},
	{2, 5, 8, 1, 4, 3, 6, 7, 9, 0},
}

func dammInterim(digits string) byte {
	var interim byte
	for i := 0; i < len(digits); i++ {
		interim = damm[interim][digits[i]-'0']
	}
	return interim
}

// dammDigit returns the check digit for a string of decimal digits.
func dammDigit(digits string) byte { return '0' + dammInterim(digits) }

// validCode reports whether a code is six digits with a correct check digit.
func validCode(code string) bool {
	return codePattern.MatchString(code) && dammInterim(code) == 0
}

// normalizeCode drops the spaces and dashes people type when they read a
// code off a screen.
func normalizeCode(code string) string {
	return strings.NewReplacer(" ", "", "-", "", "\u00a0", "").Replace(code)
}

type FamilyReader interface {
	Cards(guardianID, tenantID string) []familydashboard.ChildCard
}

// Challenge is one pairing in progress. It starts with the device's key and
// code; TenantID, ChildID and Approver are set when a guardian enters the code.
type Challenge struct {
	Digest      string    `json:"digest"`
	TenantID    string    `json:"tenant_id,omitempty"`
	ChildID     string    `json:"child_id,omitempty"`
	Code        string    `json:"code,omitempty"`
	Expires     time.Time `json:"expires"`
	Name        string    `json:"name,omitempty"`
	PublicKey   string    `json:"public_key,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Approver    string    `json:"approver,omitempty"`
	Nonce       string    `json:"nonce,omitempty"`
	Consumed    bool      `json:"consumed"`
}

type Device struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	ChildID   string `json:"child_id"`
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
	Revoked   bool   `json:"revoked"`
}

type state struct {
	Challenges []Challenge `json:"challenges"`
	Devices    []Device    `json:"devices"`
}

type Store struct {
	mu       sync.Mutex
	path     string
	families FamilyReader
	now      func() time.Time
	state    state
	// wrong remembers, per guardian, when unknown codes were entered. It is
	// kept in memory only: a restart forgives, which is harmless.
	wrong map[string][]time.Time
	// draw returns five random digits as a number below 100000. It is a
	// field so tests can make it repeat; production uses crypto/rand.
	draw func() (int64, error)
}

func drawDigits() (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(100000))
	if err != nil {
		return 0, err
	}
	return n.Int64(), nil
}

// Open is single-writer per file; a shared transactional backend is required
// for a horizontally scaled production server.
func Open(path string, families FamilyReader, now func() time.Time) (*Store, error) {
	if path == "" || families == nil || now == nil {
		return nil, ErrInvalid
	}
	s := &Store{path: path, families: families, now: now, wrong: map[string][]time.Time{}, draw: drawDigits}
	stat, statErr := os.Lstat(path)
	if statErr == nil && (!stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0) {
		return nil, ErrInvalid
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, ErrInvalid
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil || len(data) > 2<<20 {
		return nil, ErrInvalid
	}
	if err := json.Unmarshal(data, &s.state); err != nil {
		return nil, ErrInvalid
	}
	seen := make(map[string]bool)
	for _, c := range s.state.Challenges {
		// A request that a guardian approved must name the child it is for.
		if c.Digest == "" || seen[c.Digest] || (c.Approver != "" && (c.TenantID == "" || c.ChildID == "")) {
			return nil, ErrInvalid
		}
		seen[c.Digest] = true
	}
	for _, d := range s.state.Devices {
		if d.ID == "" || d.ChildID == "" || d.TenantID == "" || seen[d.ID] {
			return nil, ErrInvalid
		}
		seen[d.ID] = true
	}
	return s, nil
}

func (s *Store) allowed(guardian, tenant, child string) bool {
	if guardian == "" || tenant == "" || child == "" {
		return false
	}
	for _, c := range s.families.Cards(guardian, tenant) {
		if c.ID == child && c.Permissions.ManageDevices {
			return true
		}
	}
	return false
}
func random(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func digest(token string) (string, bool) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != 32 || len(token) != 43 {
		return "", false
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:]), true
}
func (s *Store) index(digest string) int {
	for i, c := range s.state.Challenges {
		if c.Digest == digest {
			return i
		}
	}
	return -1
}
func (s *Store) validChallenge(index int) error {
	if index < 0 {
		return ErrInvalid
	}
	c := s.state.Challenges[index]
	if c.Consumed {
		return ErrConsumed
	}
	if !s.now().Before(c.Expires) {
		return ErrExpired
	}
	return nil
}
func (s *Store) commit(next state) error {
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	f, err := os.CreateTemp(dir, ".yapuno-pairing-*")
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
	s.state = next
	return nil
}
func (s *Store) clone() state {
	return state{Challenges: append([]Challenge(nil), s.state.Challenges...), Devices: append([]Device(nil), s.state.Devices...)}
}

// live reports whether a challenge can still be used.
func (s *Store) live(c Challenge) bool {
	return !c.Consumed && s.now().Before(c.Expires)
}

// Request is called by a device that wants to be paired; nobody is
// authenticated yet. It stores the device's public key and returns a secret
// token, which stays on the device, and a short code, which the device shows.
// The request binds nothing until a guardian enters the code.
func (s *Store) Request(name string, publicDER []byte) (token, code string, err error) {
	if _, err := parseKey(publicDER); err != nil || !validName(name) {
		return "", "", ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Finished and expired pairings are dropped on the way.
	next := state{Devices: append([]Device(nil), s.state.Devices...)}
	pending := 0
	taken := map[string]bool{}
	for _, c := range s.state.Challenges {
		if !s.live(c) {
			continue
		}
		next.Challenges = append(next.Challenges, c)
		if c.Approver == "" {
			pending++
			taken[c.Code] = true
		}
	}
	if pending >= maxPending {
		return "", "", ErrBusy
	}
	if token, err = random(32); err != nil {
		return "", "", err
	}
	for code == "" || taken[code] {
		n, err := s.draw()
		if err != nil || n < 0 || n > 99999 {
			return "", "", ErrInvalid
		}
		digits := fmt.Sprintf("%05d", n)
		code = digits + string(dammDigit(digits))
	}
	key, _ := digest(token)
	fingerprint := sha256.Sum256(publicDER)
	next.Challenges = append(next.Challenges, Challenge{Digest: key, Code: code, Name: name,
		PublicKey: base64.RawURLEncoding.EncodeToString(publicDER), Fingerprint: hex.EncodeToString(fingerprint[:]),
		Expires: s.now().Add(challengeTTL)})
	if err := s.commit(next); err != nil {
		return "", "", err
	}
	return token, code, nil
}

func parseKey(data []byte) (*ecdsa.PublicKey, error) {
	if len(data) == 0 || len(data) > 512 {
		return nil, ErrInvalid
	}
	parsed, err := x509.ParsePKIXPublicKey(data)
	if err != nil {
		return nil, ErrInvalid
	}
	key, ok := parsed.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, ErrInvalid
	}
	return key, nil
}
func verify(key *ecdsa.PublicKey, domain string, payload []byte, signature []byte) bool {
	if len(signature) == 0 || len(signature) > 256 {
		return false
	}
	msg := sha256.Sum256(append([]byte(domain), payload...))
	return ecdsa.VerifyASN1(key, msg[:], signature)
}
func validName(name string) bool {
	return len(name) > 0 && len(name) <= 64 && !strings.ContainsAny(name, "\r\n\x00<>")
}

// Approve binds the request that carries this code to one child of a
// verified guardian and returns the name the device gave itself. The guardian
// must have read the code off the device they hold: whoever supplies the
// code decides which device is bound.
//
// A code that fails its check digit is a typo (ErrInvalid) and costs nothing.
// A well-formed code that no open request carries is ErrNotFound; a guardian
// who enters too many of those is refused for a while, so codes cannot be
// guessed to capture another family's device.
func (s *Store) Approve(guardian, tenant, child, code string) (string, error) {
	code = normalizeCode(code)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(guardian, tenant, child) {
		return "", ErrForbidden
	}
	if !validCode(code) {
		return "", ErrInvalid
	}
	who, now := tenant+"\x00"+guardian, s.now()
	recent := s.wrong[who][:0]
	for _, at := range s.wrong[who] {
		if now.Sub(at) < wrongCodeWindow {
			recent = append(recent, at)
		}
	}
	s.wrong[who] = recent
	if len(recent) >= maxWrongCodes {
		return "", ErrRateLimited
	}
	for i, c := range s.state.Challenges {
		if c.Code != code || c.Approver != "" || !s.live(c) {
			continue
		}
		nonce, err := random(32)
		if err != nil {
			return "", err
		}
		next := s.clone()
		next.Challenges[i].TenantID, next.Challenges[i].ChildID = tenant, child
		next.Challenges[i].Approver, next.Challenges[i].Nonce = guardian, nonce
		if err := s.commit(next); err != nil {
			return "", err
		}
		return c.Name, nil
	}
	s.wrong[who] = append(s.wrong[who], now)
	return "", ErrNotFound
}

// Nonce is a public proof-of-possession challenge, not a device credential.
func (s *Store) Nonce(token string) (string, error) {
	d, ok := digest(token)
	if !ok {
		return "", ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(d)
	if err := s.validChallenge(i); err != nil {
		return "", err
	}
	c := s.state.Challenges[i]
	if c.Approver == "" || c.Nonce == "" {
		return "", ErrNotReady
	}
	return c.Nonce, nil
}

// Finish durably binds a device to its P-256 public key once, and only after
// a fresh signature proves possession of the same private key after approval.
// This does not create a device session or authenticate future requests.
func (s *Store) Finish(token string, signature []byte) (Device, error) {
	d, ok := digest(token)
	if !ok {
		return Device{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(d)
	if err := s.validChallenge(i); err != nil {
		return Device{}, err
	}
	c := s.state.Challenges[i]
	if c.Approver == "" || c.Nonce == "" || c.PublicKey == "" {
		return Device{}, ErrNotReady
	}
	if !s.allowed(c.Approver, c.TenantID, c.ChildID) {
		return Device{}, ErrForbidden
	}
	publicDER, err := base64.RawURLEncoding.DecodeString(c.PublicKey)
	if err != nil {
		return Device{}, ErrInvalid
	}
	key, err := parseKey(publicDER)
	if err != nil {
		return Device{}, err
	}
	if !verify(key, "yapuno-pair-finish-v1\x00", []byte(c.Nonce), signature) {
		return Device{}, ErrInvalid
	}
	deviceID, err := random(16)
	if err != nil {
		return Device{}, err
	}
	next := s.clone()
	device := Device{ID: deviceID, TenantID: c.TenantID, ChildID: c.ChildID, Name: c.Name, PublicKey: c.PublicKey}
	next.Devices = append(next.Devices, device)
	next.Challenges[i].Consumed = true
	if err := s.commit(next); err != nil {
		return Device{}, err
	}
	return device, nil
}

func (s *Store) Revoke(guardian, tenant, child, deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(guardian, tenant, child) {
		return ErrForbidden
	}
	next := s.clone()
	for i, d := range next.Devices {
		if d.ID == deviceID && d.ChildID == child && d.TenantID == tenant {
			next.Devices[i].Revoked = true
			return s.commit(next)
		}
	}
	return ErrInvalid
}

// ActiveDevice is only a key lookup; callers must separately verify a fresh
// signature, tenant/session boundaries, and current contact permissions.
func (s *Store) ActiveDevice(tenant, child, deviceID string) (Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.state.Devices {
		if d.ID == deviceID && d.TenantID == tenant && d.ChildID == child && !d.Revoked {
			return d, true
		}
	}
	return Device{}, false
}
