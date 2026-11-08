// Package devicepairing persists a one-time, guardian-approved device binding.
// It is a privileged backend primitive; no unauthenticated HTTP route exists.
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
	"os"
	"path/filepath"
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
)

const challengeTTL = 5 * time.Minute
const maxChallenges = 10000

type FamilyReader interface {
	Cards(guardianID, tenantID string) []familydashboard.ChildCard
}

type Challenge struct {
	Digest      string    `json:"digest"`
	TenantID    string    `json:"tenant_id"`
	ChildID     string    `json:"child_id"`
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
}

// Open is single-writer per file; a shared transactional backend is required
// for a horizontally scaled production server.
func Open(path string, families FamilyReader, now func() time.Time) (*Store, error) {
	if path == "" || families == nil || now == nil {
		return nil, ErrInvalid
	}
	s := &Store{path: path, families: families, now: now}
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
		if c.Digest == "" || c.TenantID == "" || c.ChildID == "" || seen[c.Digest] {
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

// Start is called by a verified guardian for a child they may manage. Its
// secret token belongs in a short-lived QR code, never in logs or storage.
func (s *Store) Start(guardian, tenant, child string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(guardian, tenant, child) {
		return "", ErrForbidden
	}
	token, err := random(32)
	if err != nil {
		return "", err
	}
	key, _ := digest(token)
	next := s.clone()
	if len(next.Challenges) >= maxChallenges {
		kept := next.Challenges[:0]
		for _, c := range next.Challenges {
			if !c.Consumed && s.now().Before(c.Expires) {
				kept = append(kept, c)
			}
		}
		next.Challenges = kept
		if len(next.Challenges) >= maxChallenges {
			return "", ErrInvalid
		}
	}
	next.Challenges = append(next.Challenges, Challenge{Digest: key, TenantID: tenant, ChildID: child, Expires: s.now().Add(challengeTTL)})
	if err := s.commit(next); err != nil {
		return "", err
	}
	return token, nil
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

// Claim proves control of a P-256 private key. The guardian must later approve
// the exact fingerprint displayed on the enrolling device.
func (s *Store) Claim(token, name string, publicDER, claimSignature []byte) (string, error) {
	key, err := parseKey(publicDER)
	if err != nil || !validName(name) {
		return "", ErrInvalid
	}
	if !verify(key, "yapuno-pair-claim-v1\x00", []byte(token), claimSignature) {
		return "", ErrInvalid
	}
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
	if s.state.Challenges[i].PublicKey != "" {
		return "", ErrNotReady
	}
	fingerprint := sha256.Sum256(publicDER)
	next := s.clone()
	next.Challenges[i].Name = name
	next.Challenges[i].PublicKey = base64.RawURLEncoding.EncodeToString(publicDER)
	next.Challenges[i].Fingerprint = hex.EncodeToString(fingerprint[:])
	if err := s.commit(next); err != nil {
		return "", err
	}
	return hex.EncodeToString(fingerprint[:]), nil
}

// Approve requires a verified guardian session and explicit confirmation of
// the full key fingerprint. Equal guardians with the same child grant may approve.
func (s *Store) Approve(guardian, tenant, child, token, expectedFingerprint string) error {
	d, ok := digest(token)
	if !ok {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(guardian, tenant, child) {
		return ErrForbidden
	}
	i := s.index(d)
	if err := s.validChallenge(i); err != nil {
		return err
	}
	c := s.state.Challenges[i]
	if c.TenantID != tenant || c.ChildID != child {
		return ErrForbidden
	}
	if c.PublicKey == "" || c.Fingerprint == "" || c.Fingerprint != expectedFingerprint || c.Approver != "" {
		return ErrNotReady
	}
	nonce, err := random(32)
	if err != nil {
		return err
	}
	next := s.clone()
	next.Challenges[i].Approver = guardian
	next.Challenges[i].Nonce = nonce
	return s.commit(next)
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
