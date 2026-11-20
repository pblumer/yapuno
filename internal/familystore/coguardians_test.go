package familystore

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
)

func TestCoGuardiansAreGuardiansSharingAChildInTheSameTenant(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "family.json"))
	if err != nil {
		t.Fatal(err)
	}
	grant := func(guardian, child, tenant string) authz.GuardianGrant {
		return authz.GuardianGrant{GuardianID: guardian, ChildID: child, TenantID: tenant, Active: true, CanManageDevices: true, CanApproveContacts: true}
	}
	for _, c := range []struct{ child, tenant, guardian string }{
		{"alex", "family-1", "anna"},
		{"ada", "family-1", "anna"},
		{"noe", "family-1", "nora"},  // same tenant, no shared child
		{"alex", "family-2", "zoe"},  // same child id in another family
		{"solo", "family-3", "sole"}, // single guardian
	} {
		if err := s.Bootstrap(familydashboard.Child{ID: c.child, TenantID: c.tenant, DisplayName: "Kind"}, grant(c.guardian, c.child, c.tenant)); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []authz.GuardianGrant{grant("ben", "alex", "family-1"), grant("carla", "ada", "family-1"), grant("ben", "ada", "family-1")} {
		if err := s.BootstrapGuardian(g); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		guardian, tenant string
		want             []string
	}{
		{"anna", "family-1", []string{"ben", "carla"}},
		{"ben", "family-1", []string{"anna", "carla"}},
		{"carla", "family-1", []string{"anna", "ben"}},
		{"nora", "family-1", nil},
		{"zoe", "family-2", nil},
		{"sole", "family-3", nil},
		{"anna", "family-2", nil},
		{"unknown", "family-1", nil},
		{"", "family-1", nil},
		{"anna", "", nil},
	} {
		if got := s.CoGuardians(c.guardian, c.tenant); !reflect.DeepEqual(got, c.want) {
			t.Errorf("CoGuardians(%q, %q) = %v, want %v", c.guardian, c.tenant, got, c.want)
		}
	}
}

func TestCoGuardiansIgnoreInactiveGrants(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "family.json"))
	if err != nil {
		t.Fatal(err)
	}
	active := authz.GuardianGrant{GuardianID: "anna", ChildID: "alex", TenantID: "family-1", Active: true, CanManageDevices: true, CanApproveContacts: true}
	if err := s.Bootstrap(familydashboard.Child{ID: "alex", TenantID: "family-1", DisplayName: "Alex"}, active); err != nil {
		t.Fatal(err)
	}
	s.state.Grants = append(s.state.Grants, authz.GuardianGrant{GuardianID: "former", ChildID: "alex", TenantID: "family-1", Active: false, CanManageDevices: true, CanApproveContacts: true})
	if got := s.CoGuardians("anna", "family-1"); got != nil {
		t.Fatalf("revoked guardian listed: %v", got)
	}
	if got := s.CoGuardians("former", "family-1"); got != nil {
		t.Fatalf("revoked guardian sees co-guardians: %v", got)
	}
}
