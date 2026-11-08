package devicepairing

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/familydashboard"
)

type family struct {
	mu     sync.Mutex
	rights map[string]bool
}

func (f *family) Cards(guardianID, tenantID string) []familydashboard.ChildCard {
	f.mu.Lock()
	defer f.mu.Unlock()
	if tenantID != "tenant-a" || !f.rights[guardianID] {
		return nil
	}
	return []familydashboard.ChildCard{{ID: "child-a", Permissions: familydashboard.Permissions{ManageDevices: true}}}
}
func (f *family) revoke(guardian string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rights[guardian] = false
}
func fixture(t *testing.T) (*Store, *family, *time.Time, string) {
	t.Helper()
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	f := &family{rights: map[string]bool{"parent-a": true, "parent-b": true}}
	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := Open(path, f, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return s, f, &now, path
}
func key(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return priv, der
}
func sign(t *testing.T, priv *ecdsa.PrivateKey, domain, payload string) []byte {
	t.Helper()
	d := sha256.Sum256(append([]byte(domain), []byte(payload)...))
	sig, err := ecdsa.SignASN1(rand.Reader, priv, d[:])
	if err != nil {
		t.Fatal(err)
	}
	return sig
}
func TestGuardianApprovedDeviceDurableSingleUseAndRevocable(t *testing.T) {
	s, f, _, path := fixture(t)
	token, err := s.Start("parent-a", "tenant-a", "child-a")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(stored), token) {
		t.Fatal("raw QR token leaked in durable state")
	}
	priv, der := key(t)
	fp, err := s.Claim(token, "Family tablet", der, sign(t, priv, "yapuno-pair-claim-v1\x00", token))
	if err != nil || len(fp) != 64 {
		t.Fatalf("claim: %s %v", fp, err)
	}
	if err := s.Approve("parent-b", "tenant-a", "child-a", token, fp); err != nil {
		t.Fatal(err)
	}
	// State survives a process restart before the device finishes binding.
	restarted, err := Open(path, f, s.now)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := restarted.Nonce(token)
	if err != nil {
		t.Fatal(err)
	}
	sig := sign(t, priv, "yapuno-pair-finish-v1\x00", nonce)
	device, err := restarted.Finish(token, sig)
	if err != nil || device.ID == "" {
		t.Fatalf("finish: %+v %v", device, err)
	}
	if _, err := restarted.Finish(token, sig); !errors.Is(err, ErrConsumed) {
		t.Fatalf("replay: %v", err)
	}
	again, err := Open(path, f, s.now)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := again.ActiveDevice("tenant-a", "child-a", device.ID); !ok || got.Name != "Family tablet" {
		t.Fatalf("not durable %+v, %v", got, ok)
	}
	if _, ok := again.ActiveDevice("wrong-tenant", "child-a", device.ID); ok {
		t.Fatal("cross-tenant device leaked")
	}
	if err := again.Revoke("parent-b", "tenant-a", "child-a", device.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := again.ActiveDevice("tenant-a", "child-a", device.ID); ok {
		t.Fatal("revoked device remained active")
	}
}
func TestRejectWrongGuardianKeyFingerprintAndRevocation(t *testing.T) {
	s, f, _, _ := fixture(t)
	if _, err := s.Start("intruder", "tenant-a", "child-a"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("intruder start %v", err)
	}
	token, err := s.Start("parent-a", "tenant-a", "child-a")
	if err != nil {
		t.Fatal(err)
	}
	priv, der := key(t)
	wrong, _ := key(t)
	if _, err := s.Claim(token, "Phone", der, sign(t, wrong, "yapuno-pair-claim-v1\x00", token)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong key accepted %v", err)
	}
	fp, err := s.Claim(token, "Phone", der, sign(t, priv, "yapuno-pair-claim-v1\x00", token))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Approve("parent-a", "tenant-b", "child-a", token, fp); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant approval %v", err)
	}
	if err := s.Approve("parent-a", "tenant-a", "child-a", token, "wrong-fingerprint"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("wrong fingerprint %v", err)
	}
	if err := s.Approve("parent-a", "tenant-a", "child-a", token, fp); err != nil {
		t.Fatal(err)
	}
	nonce, err := s.Nonce(token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Finish(token, sign(t, wrong, "yapuno-pair-finish-v1\x00", nonce)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong finish signature accepted %v", err)
	}
	f.revoke("parent-a")
	if _, err := s.Finish(token, sign(t, priv, "yapuno-pair-finish-v1\x00", nonce)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked guardian still approves %v", err)
	}
}
func TestExpiredAndConcurrentFinish(t *testing.T) {
	s, _, now, _ := fixture(t)
	expired, err := s.Start("parent-a", "tenant-a", "child-a")
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(challengeTTL)
	if _, err := s.Nonce(expired); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired nonce %v", err)
	}
	fresh, err := s.Start("parent-a", "tenant-a", "child-a")
	if err != nil {
		t.Fatal(err)
	}
	priv, der := key(t)
	fp, err := s.Claim(fresh, "iPhone", der, sign(t, priv, "yapuno-pair-claim-v1\x00", fresh))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Approve("parent-a", "tenant-a", "child-a", fresh, fp); err != nil {
		t.Fatal(err)
	}
	nonce, err := s.Nonce(fresh)
	if err != nil {
		t.Fatal(err)
	}
	sig := sign(t, priv, "yapuno-pair-finish-v1\x00", nonce)
	var wg sync.WaitGroup
	successes := make(chan struct{}, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Finish(fresh, sig); err == nil {
				successes <- struct{}{}
			}
		}()
	}
	wg.Wait()
	if len(successes) != 1 {
		t.Fatalf("expected exactly one binding, got %d", len(successes))
	}
}
func TestRejectUnsafeStoreFileAndInvalidKey(t *testing.T) {
	s, f, _, path := fixture(t)
	token, err := s.Start("parent-a", "tenant-a", "child-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(token, "Device", []byte("not DER"), []byte("x")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unvalidated public key %v", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, f, s.now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("read unsafe store %v", err)
	}
}
