package contacts

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// Deleting a child removes its contacts, its open requests and its unused
// invitation codes from the store. The other child simply has one contact
// less; nothing else of the other family changes.
func TestPurgeChildRemovesEveryRelationshipAndCodeOfExactlyThatChild(t *testing.T) {
	f := setup(t)
	active := f.connect(t) // Lea and Mia
	// Lea also waits for Noah's family to answer, and holds an unused code.
	code, err := f.store.Invite("carla", noah.TenantID, noah.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := f.store.Redeem("anna", lea.TenantID, lea.ChildID, code)
	if err != nil {
		t.Fatal(err)
	}
	unused, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	// The sibling and the other families have relationships of their own.
	sibling, err := f.store.Connect("anna", tim, "bruno", mia)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := f.store.Connect("bruno", mia, "carla", noah)
	if err != nil {
		t.Fatal(err)
	}
	siblingCode, err := f.store.Invite("anna", tim.TenantID, tim.ChildID)
	if err != nil {
		t.Fatal(err)
	}

	if err := f.store.PurgeChild(lea.TenantID, lea.ChildID); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(f.path, f.families, f.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]*Store{"running": f.store, "reopened": reopened} {
		for _, id := range []string{active, pending} {
			if s.Exists(id) {
				t.Errorf("%s: relationship %s still exists", name, id)
			}
		}
		if s.Allowed(lea, mia) || len(s.Connections(lea)) != 0 || len(s.ContactsOf(lea)) != 0 {
			t.Errorf("%s: the purged child still has contacts", name)
		}
		for _, id := range []string{sibling, foreign} {
			if !s.Exists(id) {
				t.Errorf("%s: relationship %s of others was removed", name, id)
			}
		}
		// Mia keeps her other contacts and loses only Lea.
		if got := s.ContactsOf(mia); len(got) != 2 {
			t.Errorf("%s: contacts of the other child: %+v", name, got)
		}
		view, err := s.Overview("carla", noah.TenantID, noah.ChildID)
		if err != nil || len(view.Incoming)+len(view.Outgoing) != 0 || len(view.Contacts) != 1 {
			t.Errorf("%s: the other family still sees a request: %+v %v", name, view, err)
		}
	}
	// The unused code of the purged child is void; the sibling's works.
	if _, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, unused); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a code of the purged child: %v", err)
	}
	if _, err := f.store.Redeem("carla", noah.TenantID, noah.ChildID, siblingCode); err != nil {
		t.Fatalf("the sibling's code was purged: %v", err)
	}
	raw, _ := os.ReadFile(f.path)
	if strings.Contains(string(raw), active) || strings.Contains(string(raw), pending) {
		t.Fatal("the file still holds a relationship of the purged child")
	}
	if err := f.store.PurgeChild(lea.TenantID, lea.ChildID); err != nil {
		t.Fatalf("second purge: %v", err)
	}
}

func TestPurgeTenantRemovesTheRelationshipsOfAllItsChildren(t *testing.T) {
	f := setup(t)
	leaMia := f.connect(t)
	timMia, err := f.store.Connect("anna", tim, "bruno", mia)
	if err != nil {
		t.Fatal(err)
	}
	// Noah has the child id "lea" in another family: he is not part of this.
	miaNoah, err := f.store.Connect("bruno", mia, "carla", noah)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Invite("anna", tim.TenantID, tim.ChildID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.PurgeTenant("family-a"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(f.path, f.families, f.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Exists(leaMia) || reopened.Exists(timMia) || !reopened.Exists(miaNoah) {
		t.Fatal("wrong relationships after purging the family")
	}
	raw, _ := os.ReadFile(f.path)
	if strings.Contains(string(raw), "family-a") {
		t.Fatalf("the file still names the purged family: %s", raw)
	}
}

func TestPurgeNeedsACompleteTarget(t *testing.T) {
	f := setup(t)
	id := f.connect(t)
	for name, err := range map[string]error{
		"no tenant":      f.store.PurgeChild("", "lea"),
		"no child":       f.store.PurgeChild("family-a", ""),
		"tenant, no id":  f.store.PurgeTenant(""),
		"nothing at all": f.store.PurgeChild("", ""),
	} {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if !f.store.Exists(id) {
		t.Fatal("an incomplete purge removed a relationship")
	}
}
