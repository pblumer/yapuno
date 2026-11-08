package familystore

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
)

func TestPersistAndScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "family.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	child := familydashboard.Child{ID: "c1", TenantID: "tenant-a", DisplayName: "Alex"}
	grant := authz.GuardianGrant{GuardianID: "parent-a", ChildID: "c1", TenantID: "tenant-a", Active: true, CanManageDevices: true, CanApproveContacts: true}
	if err := s.Bootstrap(child, grant); err != nil {
		t.Fatal(err)
	}
	if got := s.Cards("parent-b", "tenant-a"); len(got) != 0 {
		t.Fatal("unrelated guardian can see child")
	}
	if got := s.Cards("parent-a", "tenant-b"); len(got) != 0 {
		t.Fatal("cross-tenant disclosure")
	}
	if err := s.RenameChild("parent-b", "tenant-a", "c1", "Wrong"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected forbidden, got %v", err)
	}
	if err := s.RenameChild("parent-a", "tenant-a", "c1", "Nina"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cards := reloaded.Cards("parent-a", "tenant-a")
	if len(cards) != 1 || cards[0].DisplayName != "Nina" {
		t.Fatalf("not persisted: %+v", cards)
	}
	if err := reloaded.Bootstrap(child, grant); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate accepted: %v", err)
	}
}

func TestRejectsInvalidNames(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "family.json"))
	if err != nil {
		t.Fatal(err)
	}
	err = s.Bootstrap(familydashboard.Child{ID: "c1", TenantID: "t", DisplayName: "<script>"}, authz.GuardianGrant{GuardianID: "p", ChildID: "c1", TenantID: "t", Active: true})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid name, got %v", err)
	}
}
