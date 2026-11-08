package familystore

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
)

func TestBootstrapGuardianEqualChildScopedRights(t *testing.T) {
	path := filepath.Join(t.TempDir(), "family.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first := authz.GuardianGrant{GuardianID: "g1", ChildID: "c1", TenantID: "t1", Active: true, CanManageDevices: true, CanApproveContacts: true}
	if err := store.Bootstrap(familydashboard.Child{ID: "c1", TenantID: "t1", DisplayName: "Alex"}, first); err != nil {
		t.Fatal(err)
	}
	second := authz.GuardianGrant{GuardianID: "g2", ChildID: "c1", TenantID: "t1", Active: true, CanManageDevices: true, CanApproveContacts: true}
	if err := store.BootstrapGuardian(second); err != nil {
		t.Fatal(err)
	}
	for _, guardian := range []string{"g1", "g2"} {
		cards := store.Cards(guardian, "t1")
		if len(cards) != 1 || !cards[0].Permissions.ManageDevices || !cards[0].Permissions.ApproveContacts {
			t.Fatalf("guardian %s lacks equal access: %+v", guardian, cards)
		}
	}
	if err := store.RenameChild("g2", "t1", "c1", "Nina"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Cards("g1", "t1"); len(got) != 1 || got[0].DisplayName != "Nina" {
		t.Fatalf("guardian g1 did not see persisted change: %+v", got)
	}
	if len(reloaded.Cards("g2", "other-tenant")) != 0 || len(reloaded.Cards("intruder", "t1")) != 0 {
		t.Fatal("cross-tenant or unauthorized read")
	}
	if err := reloaded.BootstrapGuardian(second); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate grant accepted: %v", err)
	}
	wrongTenant := second
	wrongTenant.GuardianID = "g3"
	wrongTenant.TenantID = "other-tenant"
	if err := reloaded.BootstrapGuardian(wrongTenant); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant grant accepted: %v", err)
	}
	partial := second
	partial.GuardianID = "g4"
	partial.CanApproveContacts = false
	if err := reloaded.BootstrapGuardian(partial); !errors.Is(err, ErrInvalid) {
		t.Fatalf("partial guardian grant accepted: %v", err)
	}
}
