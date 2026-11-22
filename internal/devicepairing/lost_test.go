package devicepairing_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
)

type lostWorld struct {
	store    *devicepairing.Store
	families *familystore.Store
	path     string
}

func newLostWorld(t *testing.T) *lostWorld {
	t.Helper()
	dir := t.TempDir()
	families, err := familystore.Open(filepath.Join(dir, "families.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, tenant, guardian string }{
		{"lea", "family-a", "anna"}, {"tim", "family-a", "anna"}, {"mia", "family-b", "bruno"},
		{"lea", "family-c", "carla"}, // same child id in another family
	} {
		grant := authz.GuardianGrant{GuardianID: c.guardian, ChildID: c.id, TenantID: c.tenant, Active: true, CanManageDevices: true, CanApproveContacts: true}
		if err := families.Bootstrap(familydashboard.Child{ID: c.id, TenantID: c.tenant, DisplayName: c.id}, grant); err != nil {
			t.Fatal(err)
		}
	}
	if err := families.BootstrapGuardian(authz.GuardianGrant{GuardianID: "albert", ChildID: "lea", TenantID: "family-a", Active: true, CanManageDevices: true, CanApproveContacts: true}); err != nil {
		t.Fatal(err)
	}
	w := &lostWorld{families: families, path: filepath.Join(dir, "pairing.json")}
	if w.store, err = devicepairing.Open(w.path, families, time.Now); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *lostWorld) active(t *testing.T, guardian, tenant, child string) int {
	t.Helper()
	devices, err := w.store.ListDevices(guardian, tenant, child)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, d := range devices {
		if !d.Revoked {
			n++
		}
	}
	return n
}

func TestRevokeAllLocksEveryDeviceOfExactlyThatChild(t *testing.T) {
	w := newLostWorld(t)
	first, _ := pairingtest.Pair(t, w.store, "anna", "family-a", "lea")
	second, _ := pairingtest.Pair(t, w.store, "anna", "family-a", "lea")
	sibling, _ := pairingtest.Pair(t, w.store, "anna", "family-a", "tim")
	other, _ := pairingtest.Pair(t, w.store, "bruno", "family-b", "mia")
	namesake, _ := pairingtest.Pair(t, w.store, "carla", "family-c", "lea")
	if err := w.store.Revoke("anna", "family-a", "lea", first); err != nil {
		t.Fatal(err)
	}
	// Albert is an equal guardian of Lea and may act although Anna paired.
	n, err := w.store.RevokeAll("albert", "family-a", "lea")
	if err != nil || n != 1 {
		t.Fatalf("revoked %d devices: %v", n, err)
	}
	for _, id := range []string{first, second} {
		if _, ok := w.store.ActiveDevice("family-a", "lea", id); ok {
			t.Fatalf("device %s still active", id)
		}
		if _, ok := w.store.DeviceByID(id); ok {
			t.Fatalf("device %s can still be looked up for login", id)
		}
	}
	if _, ok := w.store.ActiveDevice("family-a", "tim", sibling); !ok {
		t.Fatal("sibling's device was revoked")
	}
	if _, ok := w.store.ActiveDevice("family-b", "mia", other); !ok {
		t.Fatal("another family's device was revoked")
	}
	if _, ok := w.store.ActiveDevice("family-c", "lea", namesake); !ok {
		t.Fatal("device of a child with the same id in another family was revoked")
	}
	// Nothing left to do: not an error, and nothing else changes.
	if n, err := w.store.RevokeAll("anna", "family-a", "lea"); err != nil || n != 0 {
		t.Fatalf("second call: %d %v", n, err)
	}
	reopened, err := devicepairing.Open(w.path, w.families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.ActiveDevice("family-a", "lea", second); ok {
		t.Fatal("revocation not persisted")
	}
	if _, ok := reopened.ActiveDevice("family-a", "tim", sibling); !ok {
		t.Fatal("sibling's device lost after restart")
	}
}

func TestRevokeAllNeedsCurrentAuthorityForThatChild(t *testing.T) {
	w := newLostWorld(t)
	id, _ := pairingtest.Pair(t, w.store, "anna", "family-a", "lea")
	for name, who := range map[string][3]string{
		"other family's guardian":      {"bruno", "family-a", "lea"},
		"guardian naming own tenant":   {"bruno", "family-b", "lea"},
		"unknown guardian":             {"mallory", "family-a", "lea"},
		"empty guardian":               {"", "family-a", "lea"},
		"own guardian, wrong tenant":   {"anna", "family-b", "lea"},
		"own guardian, unknown child":  {"anna", "family-a", "ghost"},
		"own guardian, empty child":    {"anna", "family-a", ""},
		"guardian of another family's": {"anna", "family-b", "mia"},
	} {
		if n, err := w.store.RevokeAll(who[0], who[1], who[2]); !errors.Is(err, devicepairing.ErrForbidden) || n != 0 {
			t.Errorf("%s: %d %v", name, n, err)
		}
	}
	if _, ok := w.store.ActiveDevice("family-a", "lea", id); !ok {
		t.Fatal("a rejected call revoked the device")
	}
}

// A pairing that is still in progress must not produce a fresh device after
// the guardian has locked everything.
// asking registers a fresh device and returns its token, code and key.
func asking(t *testing.T, store *devicepairing.Store, name string) (string, string, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	token, code, err := store.Request(name, der)
	if err != nil {
		t.Fatal(err)
	}
	return token, code, key
}

func TestRevokeAllCancelsPairingsInProgress(t *testing.T) {
	w := newLostWorld(t)
	// A device a guardian already approved for Lea, but that has not finished.
	approved, code, key := asking(t, w.store, "Unknown phone")
	if _, err := w.store.Approve("anna", "family-a", "lea", code); err != nil {
		t.Fatal(err)
	}
	nonce, err := w.store.Nonce(approved)
	if err != nil {
		t.Fatal(err)
	}
	// Approved pairings of a sibling and of a child with the same id in
	// another family, and a device nobody approved yet.
	siblingToken, siblingCode, siblingKey := asking(t, w.store, "Tims Handy")
	if _, err := w.store.Approve("anna", "family-a", "tim", siblingCode); err != nil {
		t.Fatal(err)
	}
	namesakeToken, namesakeCode, namesakeKey := asking(t, w.store, "Other Lea")
	if _, err := w.store.Approve("carla", "family-c", "lea", namesakeCode); err != nil {
		t.Fatal(err)
	}
	_, waitingCode, _ := asking(t, w.store, "Waiting")
	// Only pairings were open: nothing was active, so the count is zero.
	if n, err := w.store.RevokeAll("anna", "family-a", "lea"); err != nil || n != 0 {
		t.Fatalf("lock with pairings only: %d %v", n, err)
	}
	if _, err := w.store.Finish(approved, pairingtest.Sign(t, key, "yapuno-pair-finish-v1\x00"+nonce)); !errors.Is(err, devicepairing.ErrConsumed) {
		t.Fatalf("approved pairing completed after lock: %v", err)
	}
	if w.active(t, "anna", "family-a", "lea") != 0 {
		t.Fatal("a device became active after the lock")
	}
	// The sibling's pairing is untouched.
	siblingNonce, _ := w.store.Nonce(siblingToken)
	if _, err := w.store.Finish(siblingToken, pairingtest.Sign(t, siblingKey, "yapuno-pair-finish-v1\x00"+siblingNonce)); err != nil {
		t.Fatalf("sibling's pairing was cancelled: %v", err)
	}
	// Neither is the pairing of a child with the same id in another family.
	namesakeNonce, _ := w.store.Nonce(namesakeToken)
	if _, err := w.store.Finish(namesakeToken, pairingtest.Sign(t, namesakeKey, "yapuno-pair-finish-v1\x00"+namesakeNonce)); err != nil {
		t.Fatalf("another family's pairing was cancelled: %v", err)
	}
	// A device that belongs to nobody yet is not Lea's and stays open.
	if _, err := w.store.Approve("anna", "family-a", "tim", waitingCode); err != nil {
		t.Fatalf("an unapproved request was cancelled: %v", err)
	}
	// A new pairing started after the lock works: the lock is not permanent.
	if _, _ = pairingtest.Pair(t, w.store, "anna", "family-a", "lea"); w.active(t, "anna", "family-a", "lea") != 1 {
		t.Fatal("re-pairing after the lock failed")
	}
}

func TestNoDeviceBecomesActiveWhileLocking(t *testing.T) {
	for i := 0; i < 10; i++ {
		w := newLostWorld(t)
		token, code, key := asking(t, w.store, "Racing phone")
		if _, err := w.store.Approve("anna", "family-a", "lea", code); err != nil {
			t.Fatal(err)
		}
		nonce, _ := w.store.Nonce(token)
		sig := pairingtest.Sign(t, key, "yapuno-pair-finish-v1\x00"+nonce)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = w.store.Finish(token, sig) }()
		go func() { defer wg.Done(); _, _ = w.store.RevokeAll("anna", "family-a", "lea") }()
		wg.Wait()
		// Either the lock came first and the pairing is cancelled, or the
		// pairing finished first and the lock revoked the new device.
		if w.active(t, "anna", "family-a", "lea") != 0 {
			t.Fatal("a device is active after RevokeAll returned")
		}
	}
}
