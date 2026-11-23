package devicepairing_test

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
)

// Deleting a child removes its devices from the store, revoked ones and
// pairings in progress included. Nothing of anybody else is touched.
func TestPurgeChildRemovesEveryDeviceAndPairingOfExactlyThatChild(t *testing.T) {
	w := newLostWorld(t)
	active, _ := pairingtest.Pair(t, w.store, "anna", "family-a", "lea")
	revoked, _ := pairingtest.Pair(t, w.store, "anna", "family-a", "lea")
	if err := w.store.Revoke("anna", "family-a", "lea", revoked); err != nil {
		t.Fatal(err)
	}
	// A pairing a guardian approved for Lea that has not finished.
	token, code, key := asking(t, w.store, "Leas Tablet")
	if _, err := w.store.Approve("anna", "family-a", "lea", code); err != nil {
		t.Fatal(err)
	}
	nonce, err := w.store.Nonce(token)
	if err != nil {
		t.Fatal(err)
	}
	sibling, _ := pairingtest.Pair(t, w.store, "anna", "family-a", "tim")
	other, _ := pairingtest.Pair(t, w.store, "bruno", "family-b", "mia")
	namesake, _ := pairingtest.Pair(t, w.store, "carla", "family-c", "lea")
	// A device that waits for a code belongs to nobody yet.
	_, waitingCode, _ := asking(t, w.store, "Wartet")

	if err := w.store.PurgeChild("family-a", "lea"); err != nil {
		t.Fatal(err)
	}
	reopened, err := devicepairing.Open(w.path, w.families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for name, store := range map[string]*devicepairing.Store{"running": w.store, "reopened": reopened} {
		for _, id := range []string{active, revoked} {
			if _, ok := store.DeviceByID(id); ok {
				t.Errorf("%s: device %s can still sign in", name, id)
			}
		}
		devices, err := store.ListDevices("anna", "family-a", "lea")
		if err != nil || len(devices) != 0 {
			t.Errorf("%s: devices of the purged child: %v %v", name, devices, err)
		}
		if _, ok := store.ActiveDevice("family-a", "tim", sibling); !ok {
			t.Errorf("%s: the sibling lost a device", name)
		}
		if _, ok := store.ActiveDevice("family-b", "mia", other); !ok {
			t.Errorf("%s: another family lost a device", name)
		}
		if _, ok := store.ActiveDevice("family-c", "lea", namesake); !ok {
			t.Errorf("%s: a child with the same id in another family lost a device", name)
		}
	}
	// The approved pairing is gone, not merely cancelled.
	if _, err := w.store.Finish(token, pairingtest.Sign(t, key, "yapuno-pair-finish-v1\x00"+nonce)); err == nil {
		t.Fatal("a pairing of the purged child completed")
	}
	raw, _ := os.ReadFile(w.path)
	for _, gone := range []string{active, revoked, "Leas Tablet"} {
		if strings.Contains(string(raw), gone) {
			t.Errorf("the file still holds %q", gone)
		}
	}
	// The unbound request is still open and can be given to another child.
	if _, err := w.store.Approve("anna", "family-a", "tim", waitingCode); err != nil {
		t.Fatalf("an unbound request was purged: %v", err)
	}
	// Purging again, or purging nothing, is not an error.
	if err := w.store.PurgeChild("family-a", "lea"); err != nil {
		t.Fatalf("second purge: %v", err)
	}
}

func TestPurgeTenantRemovesTheDevicesOfAllItsChildren(t *testing.T) {
	w := newLostWorld(t)
	lea, _ := pairingtest.Pair(t, w.store, "anna", "family-a", "lea")
	tim, _ := pairingtest.Pair(t, w.store, "anna", "family-a", "tim")
	other, _ := pairingtest.Pair(t, w.store, "bruno", "family-b", "mia")
	namesake, _ := pairingtest.Pair(t, w.store, "carla", "family-c", "lea")
	if err := w.store.PurgeTenant("family-a"); err != nil {
		t.Fatal(err)
	}
	reopened, err := devicepairing.Open(w.path, w.families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{lea, tim} {
		if _, ok := reopened.DeviceByID(id); ok {
			t.Errorf("device %s survived", id)
		}
	}
	for _, id := range []string{other, namesake} {
		if _, ok := reopened.DeviceByID(id); !ok {
			t.Errorf("device %s of another family was removed", id)
		}
	}
}

func TestPurgeNeedsACompleteTarget(t *testing.T) {
	w := newLostWorld(t)
	id, _ := pairingtest.Pair(t, w.store, "anna", "family-a", "lea")
	for name, err := range map[string]error{
		"no tenant":      w.store.PurgeChild("", "lea"),
		"no child":       w.store.PurgeChild("family-a", ""),
		"tenant, no id":  w.store.PurgeTenant(""),
		"nothing at all": w.store.PurgeChild("", ""),
	} {
		if !errors.Is(err, devicepairing.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, ok := w.store.ActiveDevice("family-a", "lea", id); !ok {
		t.Fatal("an incomplete purge removed a device")
	}
}
