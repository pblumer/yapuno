package signup

import (
	"errors"
	"testing"
)

// When a family is deleted its open invitations are void, and the record
// that it once joined is dropped, so the place it took is free again. What it
// did for families that still exist stays on record.
func TestPurgeTenantVoidsItsInvitationsAndFreesItsPlace(t *testing.T) {
	s, c, path := newStore(t)
	// Family a invited family b, which invited family c. Each has an open one too.
	_, joinedB, _ := s.Invite("anna", "family-a", "")
	if err := s.Consume(joinedB.ID, "family-b"); err != nil {
		t.Fatal(err)
	}
	_, joinedC, _ := s.Invite("bruno", "family-b", "leo")
	if err := s.Consume(joinedC.ID, "family-c"); err != nil {
		t.Fatal(err)
	}
	openA, _, _ := s.Invite("anna", "family-a", "")
	openB, invB, _ := s.Invite("bruno", "family-b", "leo")
	openB2, _, _ := s.Invite("bea", "family-b", "")
	if s.Registered() != 2 {
		t.Fatalf("registered: %d", s.Registered())
	}
	if err := s.PurgeTenant("family-b"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, c.now)
	if err != nil {
		t.Fatal(err)
	}
	for name, store := range map[string]*Store{"running": s, "reopened": reopened} {
		for _, code := range []string{openB, openB2} {
			if _, err := store.Lookup(code); !errors.Is(err, ErrNotFound) {
				t.Errorf("%s: an invitation of the purged family is still valid: %v", name, err)
			}
		}
		if _, err := store.Usable(invB.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: usable by id: %v", name, err)
		}
		if len(store.Pending("bruno", "family-b"))+len(store.Pending("bea", "family-b")) != 0 {
			t.Errorf("%s: pending invitations of the purged family", name)
		}
		if _, err := store.Lookup(openA); err != nil {
			t.Errorf("%s: another family's invitation was removed: %v", name, err)
		}
		// Family b no longer counts; family c, which it invited, still does.
		if store.Registered() != 1 {
			t.Errorf("%s: registered %d", name, store.Registered())
		}
	}
	// The record of who invited family c is kept although the inviter is gone.
	found := false
	for _, inv := range reopened.items {
		found = found || (inv.Joined == "family-c" && inv.Tenant == "family-b")
		if inv.Joined == "family-b" {
			t.Fatal("the record that the purged family joined is left")
		}
	}
	if !found {
		t.Fatal("the record of a family that still exists was removed")
	}
	if err := s.PurgeTenant("family-b"); err != nil {
		t.Fatalf("second purge: %v", err)
	}
}

func TestPurgeTenantNeedsATargetAndADurableWrite(t *testing.T) {
	s, _, _ := newStore(t)
	code, _, _ := s.Invite("anna", "family-a", "")
	for _, bad := range []string{"", "a\nb"} {
		if err := s.PurgeTenant(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	s.failWrite = true
	if err := s.PurgeTenant("family-a"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed write: %v", err)
	}
	s.failWrite = false
	if _, err := s.Lookup(code); err != nil {
		t.Fatal("a failed purge removed an invitation")
	}
}

// An invitation may name a child as the one to connect with. When that child
// is deleted the invitation still registers a family, but it no longer names
// anybody, and no record of the deleted child's identifier is left.
func TestPurgeChildRemovesTheChildFromInvitationsThatNameIt(t *testing.T) {
	s, c, path := newStore(t)
	openCode, open, _ := s.Invite("anna", "family-a", "lea")
	_, used, _ := s.Invite("anna", "family-a", "lea")
	if err := s.Consume(used.ID, "family-b"); err != nil {
		t.Fatal(err)
	}
	siblingCode, _, _ := s.Invite("anna", "family-a", "tim")
	// The same child id in another family is another child.
	namesakeCode, _, _ := s.Invite("carla", "family-c", "lea")
	if err := s.PurgeChild("family-a", "lea"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, c.now)
	if err != nil {
		t.Fatal(err)
	}
	for name, store := range map[string]*Store{"running": s, "reopened": reopened} {
		got, err := store.Lookup(openCode)
		if err != nil || got.ID != open.ID || got.Child != "" {
			t.Errorf("%s: open invitation: %+v %v", name, got, err)
		}
		if got, err := store.Lookup(siblingCode); err != nil || got.Child != "tim" {
			t.Errorf("%s: the sibling's invitation: %+v %v", name, got, err)
		}
		if got, err := store.Lookup(namesakeCode); err != nil || got.Child != "lea" {
			t.Errorf("%s: another family's invitation: %+v %v", name, got, err)
		}
		if store.Registered() != 1 {
			t.Errorf("%s: registered %d", name, store.Registered())
		}
	}
	for _, inv := range reopened.items {
		if inv.Tenant == "family-a" && inv.Child == "lea" {
			t.Fatalf("an invitation still names the purged child: %+v", inv)
		}
	}
	if err := s.PurgeChild("family-a", "lea"); err != nil {
		t.Fatalf("second purge: %v", err)
	}
	for name, err := range map[string]error{"no tenant": s.PurgeChild("", "lea"), "no child": s.PurgeChild("family-a", "")} {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	s.failWrite = true
	if err := s.PurgeChild("family-a", "tim"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed write: %v", err)
	}
	s.failWrite = false
	if got, _ := s.Lookup(siblingCode); got.Child != "tim" {
		t.Fatal("a failed purge changed an invitation")
	}
}
