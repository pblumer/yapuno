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

func sign(t *testing.T, priv *ecdsa.PrivateKey, domain, value string) []byte {
	t.Helper()
	digest := sha256.Sum256([]byte(domain + value))
	sig, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// request registers a fresh device and returns its token, its code and its key.
func request(t *testing.T, s *Store, name string) (string, string, *ecdsa.PrivateKey) {
	t.Helper()
	priv, der := key(t)
	token, code, err := s.Request(name, der)
	if err != nil {
		t.Fatal(err)
	}
	return token, code, priv
}

func finish(t *testing.T, s *Store, token string, priv *ecdsa.PrivateKey) (Device, error) {
	t.Helper()
	nonce, err := s.Nonce(token)
	if err != nil {
		return Device{}, err
	}
	return s.Finish(token, sign(t, priv, "yapuno-pair-finish-v1\x00", nonce))
}

func TestADeviceShowsACodeAndAGuardianBindsItToTheirChild(t *testing.T) {
	s, _, _, path := fixture(t)
	token, code, priv := request(t, s, "iPhone")
	if len(token) != 43 || !codePattern.MatchString(code) || !validCode(code) {
		t.Fatalf("token %q code %q", token, code)
	}
	// Nothing is bound yet: the device has no child and cannot finish.
	if _, err := s.Nonce(token); !errors.Is(err, ErrNotReady) {
		t.Fatalf("nonce before approval: %v", err)
	}
	if _, err := s.Finish(token, sign(t, priv, "yapuno-pair-finish-v1\x00", "x")); !errors.Is(err, ErrNotReady) {
		t.Fatalf("finish before approval: %v", err)
	}
	if devices, _ := s.ListDevices("parent-a", "tenant-a", "child-a"); len(devices) != 0 {
		t.Fatal("a device exists before approval")
	}
	name, err := s.Approve("parent-a", "tenant-a", "child-a", code)
	if err != nil || name != "iPhone" {
		t.Fatalf("approve: %q %v", name, err)
	}
	device, err := finish(t, s, token, priv)
	if err != nil || device.ID == "" || device.TenantID != "tenant-a" || device.ChildID != "child-a" || device.Name != "iPhone" {
		t.Fatalf("finish: %+v %v", device, err)
	}
	if _, ok := s.ActiveDevice("tenant-a", "child-a", device.ID); !ok {
		t.Fatal("device not active")
	}
	// The request is spent: neither the code nor the token works again.
	if _, err := s.Approve("parent-a", "tenant-a", "child-a", code); !errors.Is(err, ErrNotFound) {
		t.Fatalf("code reused: %v", err)
	}
	if _, err := finish(t, s, token, priv); !errors.Is(err, ErrConsumed) {
		t.Fatalf("token reused: %v", err)
	}
	// Durable: the device is there after a restart, the secrets are not on disk.
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), token) {
		t.Fatal("the device token is stored in clear")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("file mode %v", info.Mode())
	}
	reopened, err := Open(path, &family{rights: map[string]bool{"parent-a": true}}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.ActiveDevice("tenant-a", "child-a", device.ID); !ok {
		t.Fatal("device lost by a restart")
	}
}

func TestOnlyTheKeyThatAskedCanFinish(t *testing.T) {
	s, _, _, _ := fixture(t)
	token, code, priv := request(t, s, "iPhone")
	if _, err := s.Approve("parent-a", "tenant-a", "child-a", code); err != nil {
		t.Fatal(err)
	}
	nonce, _ := s.Nonce(token)
	other, _ := key(t)
	for name, sig := range map[string][]byte{
		"another key":    sign(t, other, "yapuno-pair-finish-v1\x00", nonce),
		"another domain": sign(t, priv, "yapuno-device-login-v1\x00", nonce),
		"another nonce":  sign(t, priv, "yapuno-pair-finish-v1\x00", nonce+"x"),
		"empty":          nil,
	} {
		if _, err := s.Finish(token, sig); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A wrong token is not a pairing at all.
	if _, err := s.Finish(strings.Repeat("A", 43), sign(t, priv, "yapuno-pair-finish-v1\x00", nonce)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown token: %v", err)
	}
	if _, err := s.Finish(token, sign(t, priv, "yapuno-pair-finish-v1\x00", nonce)); err != nil {
		t.Fatalf("the right key after wrong attempts: %v", err)
	}
}

func TestApprovalNeedsCurrentAuthorityForExactlyThatChild(t *testing.T) {
	s, f, _, _ := fixture(t)
	_, code, _ := request(t, s, "iPhone")
	for name, c := range map[string][3]string{
		"stranger":       {"mallory", "tenant-a", "child-a"},
		"other tenant":   {"parent-a", "tenant-b", "child-a"},
		"other child":    {"parent-a", "tenant-a", "child-b"},
		"empty guardian": {"", "tenant-a", "child-a"},
	} {
		if _, err := s.Approve(c[0], c[1], c[2], code); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// None of those attempts used up the code.
	token, code2, priv := request(t, s, "Tablet")
	if _, err := s.Approve("parent-a", "tenant-a", "child-a", code); err != nil {
		t.Fatalf("the code after refused attempts: %v", err)
	}
	// Authority is checked again when the device finishes.
	if _, err := s.Approve("parent-b", "tenant-a", "child-a", code2); err != nil {
		t.Fatal(err)
	}
	f.revoke("parent-b")
	if _, err := finish(t, s, token, priv); !errors.Is(err, ErrForbidden) {
		t.Fatalf("finish after the approver lost authority: %v", err)
	}
}

func TestCodesCarryACheckDigitSoATypoIsNeverAnotherCode(t *testing.T) {
	// Known answer for the Damm algorithm: 572 has check digit 4.
	if dammDigit("572") != '4' || !validCode("012340"[:5]+string(dammDigit("01234"))) {
		t.Fatalf("check digit: %c", dammDigit("572"))
	}
	s, _, _, _ := fixture(t)
	_, code, _ := request(t, s, "iPhone")
	// Every single wrong digit and every swap of two neighbours is rejected
	// as a typo, without looking anything up.
	for i := 0; i < len(code); i++ {
		for d := byte('0'); d <= '9'; d++ {
			if d == code[i] {
				continue
			}
			typo := code[:i] + string(d) + code[i+1:]
			if validCode(typo) {
				t.Fatalf("single typo %q of %q is a valid code", typo, code)
			}
		}
		if i+1 < len(code) && code[i] != code[i+1] {
			swapped := code[:i] + string(code[i+1]) + string(code[i]) + code[i+2:]
			if validCode(swapped) {
				t.Fatalf("swap %q of %q is a valid code", swapped, code)
			}
		}
	}
	for _, bad := range []string{"", "12345", "1234567", "12345a", " 12345", "١٢٣٤٥٦", code + "0"} {
		if _, err := s.Approve("parent-a", "tenant-a", "child-a", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	// Spaces and a dash, as people type what they read, are fine.
	if _, err := s.Approve("parent-a", "tenant-a", "child-a", " "+code[:3]+" "+code[3:]+" "); err != nil {
		t.Fatalf("code with spaces: %v", err)
	}
}

func TestWrongCodesAreLimitedPerGuardian(t *testing.T) {
	s, _, now, _ := fixture(t)
	_, code, _ := request(t, s, "iPhone")
	unknown := func(i int) string {
		for n := i * 7919; ; n++ {
			candidate := []byte{byte('0' + n/10000%10), byte('0' + n/1000%10), byte('0' + n/100%10), byte('0' + n/10%10), byte('0' + n%10)}
			full := string(candidate) + string(dammDigit(string(candidate)))
			if full != code {
				return full
			}
		}
	}
	// Typos do not count: they cannot be a guess at a real code.
	for i := 0; i < 2*maxWrongCodes; i++ {
		if _, err := s.Approve("parent-a", "tenant-a", "child-a", "12345"); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	for i := 0; i < maxWrongCodes; i++ {
		if _, err := s.Approve("parent-a", "tenant-a", "child-a", unknown(i)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("wrong code %d: %v", i, err)
		}
	}
	// Now even the right code is refused for this guardian...
	if _, err := s.Approve("parent-a", "tenant-a", "child-a", code); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("after too many wrong codes: %v", err)
	}
	// ...but not for another guardian...
	if _, err := s.Approve("parent-b", "tenant-a", "child-a", code); err != nil {
		t.Fatalf("another guardian is limited too: %v", err)
	}
	// ...and not for ever. (The first code has expired by then.)
	*now = now.Add(wrongCodeWindow)
	_, later, _ := request(t, s, "Zweites Handy")
	if _, err := s.Approve("parent-a", "tenant-a", "child-a", later); err != nil {
		t.Fatalf("after the window: %v", err)
	}
	_, code2, _ := request(t, s, "Tablet")
	for i := 0; i < maxWrongCodes; i++ {
		_, _ = s.Approve("parent-a", "tenant-a", "child-a", unknown(i+50))
	}
	if _, err := s.Approve("parent-a", "tenant-a", "child-a", code2); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("the limit does not apply again: %v", err)
	}
}

func TestRequestsExpireAndAreBounded(t *testing.T) {
	s, _, now, _ := fixture(t)
	token, code, priv := request(t, s, "iPhone")
	*now = now.Add(challengeTTL - time.Second)
	if _, err := s.Nonce(token); !errors.Is(err, ErrNotReady) {
		t.Fatalf("just before expiry: %v", err)
	}
	*now = now.Add(time.Second)
	if _, err := s.Approve("parent-a", "tenant-a", "child-a", code); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired code: %v", err)
	}
	if _, err := s.Nonce(token); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired token: %v", err)
	}
	// An approval does not extend the five minutes.
	token, code, priv = request(t, s, "iPhone")
	if _, err := s.Approve("parent-a", "tenant-a", "child-a", code); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(challengeTTL)
	if _, err := finish(t, s, token, priv); !errors.Is(err, ErrExpired) {
		t.Fatalf("finish after expiry: %v", err)
	}
	// Anybody can ask for a code, so open requests are capped; expired ones make room.
	seen := map[string]bool{}
	for i := 0; i < maxPending; i++ {
		_, c, _ := request(t, s, "Gerät")
		if seen[c] {
			t.Fatalf("code %q handed out twice", c)
		}
		seen[c] = true
	}
	_, der := key(t)
	if _, _, err := s.Request("Gerät", der); !errors.Is(err, ErrBusy) {
		t.Fatalf("beyond the cap: %v", err)
	}
	*now = now.Add(challengeTTL)
	if _, _, err := s.Request("Gerät", der); err != nil {
		t.Fatalf("after the old requests expired: %v", err)
	}
}

func TestRequestRejectsBadKeysAndNames(t *testing.T) {
	s, _, _, _ := fixture(t)
	_, der := key(t)
	rsaLike := []byte("not a key")
	for name, c := range map[string]struct {
		name string
		der  []byte
	}{
		"no key":      {"iPhone", nil},
		"garbage key": {"iPhone", rsaLike},
		"huge key":    {"iPhone", make([]byte, 600)},
		"empty name":  {"", der},
		"markup":      {"<b>x</b>", der},
		"line break":  {"a\nb", der},
		"very long":   {strings.Repeat("x", 65), der},
	} {
		if _, _, err := s.Request(c.name, c.der); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestOneCodeBindsOneDeviceEvenWhenApprovedConcurrently(t *testing.T) {
	s, _, _, _ := fixture(t)
	_, code, _ := request(t, s, "iPhone")
	var wg sync.WaitGroup
	wins := make(chan bool, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		guardian := "parent-a"
		if i%2 == 1 {
			guardian = "parent-b"
		}
		go func() {
			defer wg.Done()
			_, err := s.Approve(guardian, "tenant-a", "child-a", code)
			wins <- err == nil
		}()
	}
	wg.Wait()
	close(wins)
	won := 0
	for ok := range wins {
		if ok {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("code approved %d times", won)
	}
}

func TestRevokedDevicesStayRevokedAndFilesFailClosed(t *testing.T) {
	s, _, _, path := fixture(t)
	token, code, priv := request(t, s, "iPhone")
	if _, err := s.Approve("parent-a", "tenant-a", "child-a", code); err != nil {
		t.Fatal(err)
	}
	device, err := finish(t, s, token, priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke("mallory", "tenant-a", "child-a", device.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("foreign revoke: %v", err)
	}
	if err := s.Revoke("parent-a", "tenant-a", "child-a", device.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.ActiveDevice("tenant-a", "child-a", device.ID); ok {
		t.Fatal("revoked device still active")
	}
	f := &family{rights: map[string]bool{"parent-a": true}}
	// A file written before codes existed still opens: old open pairings
	// carry a child and no code.
	old := `{"challenges":[{"digest":"abc","tenant_id":"tenant-a","child_id":"child-a","expires":"2026-01-01T00:00:00Z","consumed":false}],"devices":[]}`
	oldPath := filepath.Join(t.TempDir(), "old.json")
	_ = os.WriteFile(oldPath, []byte(old), 0600)
	if _, err := Open(oldPath, f, time.Now); err != nil {
		t.Fatalf("file from before: %v", err)
	}
	for name, content := range map[string]string{
		"corrupt":                  `{"challenges":[`,
		"challenge without digest": `{"challenges":[{"digest":"","expires":"2026-01-01T00:00:00Z"}],"devices":[]}`,
		"duplicate digest":         `{"challenges":[{"digest":"a","expires":"2026-01-01T00:00:00Z"},{"digest":"a","expires":"2026-01-01T00:00:00Z"}],"devices":[]}`,
		"approved without a child": `{"challenges":[{"digest":"a","expires":"2026-01-01T00:00:00Z","approver":"parent-a","nonce":"n"}],"devices":[]}`,
		"device without a child":   `{"challenges":[],"devices":[{"id":"d","tenant_id":"tenant-a","child_id":"","name":"x","public_key":"k"}]}`,
	} {
		p := filepath.Join(t.TempDir(), "pairing.json")
		_ = os.WriteFile(p, []byte(content), 0600)
		if _, err := Open(p, f, time.Now); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	_ = os.Chmod(path, 0644)
	if _, err := Open(path, f, time.Now); err == nil {
		t.Error("world-readable file opened")
	}
	if _, err := Open("", f, time.Now); err == nil {
		t.Error("empty path accepted")
	}
}

func TestTwoWaitingDevicesNeverShareACode(t *testing.T) {
	s, _, now, _ := fixture(t)
	// The random source repeats itself: 11111, 11111, 11111, 22222, ...
	draws := []int64{11111, 11111, 11111, 22222, 22222, 33333}
	s.draw = func() (int64, error) {
		n := draws[0]
		draws = draws[1:]
		return n, nil
	}
	_, first, _ := request(t, s, "Eins")
	_, second, _ := request(t, s, "Zwei")
	if first[:5] != "11111" || second[:5] != "22222" || first == second {
		t.Fatalf("codes %q %q", first, second)
	}
	// A code is free again once its request is over.
	*now = now.Add(challengeTTL)
	draws = []int64{11111}
	if _, again, _ := request(t, s, "Drei"); again != first {
		t.Fatalf("a free code was not reused: %q", again)
	}
	// A failing random source fails the request instead of handing out a guess.
	s.draw = func() (int64, error) { return 0, errors.New("no entropy") }
	_, der := key(t)
	if _, _, err := s.Request("Vier", der); err == nil {
		t.Fatal("a code was handed out without randomness")
	}
}
