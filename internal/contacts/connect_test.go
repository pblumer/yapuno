package contacts

import (
	"errors"
	"sync"
	"testing"
)

// Connect is the second way to a contact: both guardians have already said
// yes, each for their own child, so no code is passed and nothing waits.
func TestConnectCreatesAnActiveContactForBothChildren(t *testing.T) {
	f := setup(t)
	id, err := f.store.Connect("anna", lea, "bruno", mia)
	if err != nil || id == "" {
		t.Fatalf("connect: %q %v", id, err)
	}
	if !f.store.Allowed(lea, mia) || !f.store.Allowed(mia, lea) {
		t.Fatal("connected children are not contacts")
	}
	for name, pair := range map[string][2]Party{
		"sibling of connected child":  {tim, mia},
		"same child id, other family": {noah, mia},
		"unrelated family":            {lea, noah},
	} {
		if f.store.Allowed(pair[0], pair[1]) {
			t.Errorf("%s: contact allowed", name)
		}
	}
	own, err := f.store.Overview("anna", lea.TenantID, lea.ChildID)
	if err != nil || len(own.Contacts) != 1 || own.Contacts[0] != (Entry{ID: id, Name: "Mia"}) || len(own.Incoming)+len(own.Outgoing) != 0 {
		t.Fatalf("inviting side: %+v %v", own, err)
	}
	other, err := f.store.Overview("bruno", mia.TenantID, mia.ChildID)
	if err != nil || len(other.Contacts) != 1 || other.Contacts[0] != (Entry{ID: id, Name: "Lea"}) || len(other.Incoming)+len(other.Outgoing) != 0 {
		t.Fatalf("invited side: %+v %v", other, err)
	}
	if got := f.store.Connections(mia); len(got) != 1 || got[0].Peer != lea || !got[0].Grant.Active {
		t.Fatalf("connections: %+v", got)
	}
	reopened, err := Open(f.path, f.families, f.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Allowed(lea, mia) {
		t.Fatal("connection not persisted")
	}
	// It is an ordinary contact: either family ends it.
	if err := f.store.Remove("bruno", mia.TenantID, mia.ChildID, id); err != nil {
		t.Fatal(err)
	}
	if f.store.Allowed(lea, mia) {
		t.Fatal("contact survived its removal")
	}
}

func TestConnectNeedsCurrentAuthorityOnBothSides(t *testing.T) {
	f := setup(t)
	type call struct {
		g1 string
		p1 Party
		g2 string
		p2 Party
	}
	for name, c := range map[string]call{
		"inviting guardian of another family": {"carla", lea, "bruno", mia},
		"invited guardian of another family":  {"anna", lea, "carla", mia},
		"one guardian for both sides":         {"anna", lea, "anna", mia},
		"unknown inviting guardian":           {"mallory", lea, "bruno", mia},
		"empty inviting guardian":             {"", lea, "bruno", mia},
		"empty invited guardian":              {"anna", lea, "", mia},
		"inviting child of another tenant":    {"anna", Party{TenantID: "family-b", ChildID: "lea"}, "bruno", mia},
		"invited child unknown":               {"anna", lea, "bruno", Party{TenantID: "family-b", ChildID: "ghost"}},
		"inviting child unknown":              {"anna", Party{TenantID: "family-a", ChildID: "ghost"}, "bruno", mia},
		"empty invited party":                 {"anna", lea, "bruno", Party{}},
		"sides swapped":                       {"bruno", lea, "anna", mia},
	} {
		if id, err := f.store.Connect(c.g1, c.p1, c.g2, c.p2); !errors.Is(err, ErrForbidden) || id != "" {
			t.Errorf("%s: %q %v", name, id, err)
		}
	}
	if f.store.Allowed(lea, mia) {
		t.Fatal("a rejected call connected the children")
	}
	// Authority is the current one: a revoked grant connects nobody.
	revoked := map[string]bool{"bruno": true}
	strict, err := Open(f.path, revoking{Families: f.families, revoked: revoked}, f.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := strict.Connect("anna", lea, "bruno", mia); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked invited guardian connected: %v", err)
	}
	revoked["bruno"], revoked["anna"] = false, true
	if _, err := strict.Connect("anna", lea, "bruno", mia); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked inviting guardian connected: %v", err)
	}
	// An equal guardian of the inviting child may stand in.
	if _, err := f.store.Connect("albert", lea, "carla", noah); err != nil {
		t.Fatalf("equal guardian: %v", err)
	}
}

func TestConnectRejectsSelfAndExistingRelationships(t *testing.T) {
	f := setup(t)
	if _, err := f.store.Connect("anna", lea, "anna", lea); !errors.Is(err, ErrInvalid) {
		t.Fatalf("self: %v", err)
	}
	// A request that still waits is not silently approved by a second path.
	code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, code)
	if err != nil {
		t.Fatal(err)
	}
	for _, sides := range [][2]Party{{lea, mia}, {mia, lea}} {
		guardians := map[Party]string{lea: "anna", mia: "bruno"}
		if _, err := f.store.Connect(guardians[sides[0]], sides[0], guardians[sides[1]], sides[1]); !errors.Is(err, ErrExists) {
			t.Fatalf("pending request: %v", err)
		}
	}
	if f.store.Allowed(lea, mia) {
		t.Fatal("a pending request became active")
	}
	if err := f.store.Confirm("anna", lea.TenantID, lea.ChildID, pending); err != nil {
		t.Fatalf("the pending request was damaged: %v", err)
	}
	if _, err := f.store.Connect("anna", lea, "bruno", mia); !errors.Is(err, ErrExists) {
		t.Fatalf("active contact: %v", err)
	}
	view, err := f.store.Overview("anna", lea.TenantID, lea.ChildID)
	if err != nil || len(view.Contacts) != 1 {
		t.Fatalf("duplicate relationship: %+v %v", view, err)
	}
}

func TestConnectReplacesAnExpiredRequest(t *testing.T) {
	f := setup(t)
	code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, code); err != nil {
		t.Fatal(err)
	}
	f.clock.advance(RequestTTL)
	if _, err := f.store.Connect("bruno", mia, "anna", lea); err != nil {
		t.Fatalf("an expired request blocked the connection: %v", err)
	}
	if !f.store.Allowed(lea, mia) {
		t.Fatal("not connected")
	}
	// The file holds one relationship for the pair and opens again.
	if _, err := Open(f.path, f.families, f.clock.now); err != nil {
		t.Fatalf("store does not reopen: %v", err)
	}
}

func TestConcurrentConnectHasExactlyOneWinner(t *testing.T) {
	f := setup(t)
	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_, results[i] = f.store.Connect("anna", lea, "bruno", mia)
			} else {
				_, results[i] = f.store.Connect("bruno", mia, "anna", lea)
			}
		}(i)
	}
	wg.Wait()
	won := 0
	for _, err := range results {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, ErrExists):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d callers connected the same pair", won)
	}
}
