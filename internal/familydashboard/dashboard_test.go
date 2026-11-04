package familydashboard

import (
	"reflect"
	"testing"

	"github.com/pblumer/yapuno/internal/authz"
)

func TestBuildScopesAndPermissions(t *testing.T) {
	children := []Child{
		{ID: "c2", TenantID: "t1", DisplayName: "Second"},
		{ID: "c1", TenantID: "t1", DisplayName: "First"},
		{ID: "c3", TenantID: "t2", DisplayName: "Other family"},
		{ID: "c4", TenantID: "t1", DisplayName: "Unrelated"},
	}
	grants := []authz.GuardianGrant{
		{GuardianID: "g1", ChildID: "c1", TenantID: "t1", Active: true, CanManageDevices: true, CanApproveContacts: true},
		{GuardianID: "g1", ChildID: "c2", TenantID: "t1", Active: true, CanApproveContacts: true},
		{GuardianID: "g1", ChildID: "c3", TenantID: "t2", Active: true, CanManageDevices: true},
		{GuardianID: "g2", ChildID: "c4", TenantID: "t1", Active: true, CanManageDevices: true},
	}
	want := []ChildCard{
		{ID: "c1", DisplayName: "First", Permissions: Permissions{ManageDevices: true, ApproveContacts: true}},
		{ID: "c2", DisplayName: "Second", Permissions: Permissions{ApproveContacts: true}},
	}
	if got := Build("g1", "t1", children, grants); !reflect.DeepEqual(got, want) {
		t.Fatalf("Build = %#v, want %#v", got, want)
	}
	if got := Build("g2", "t2", children, grants); len(got) != 0 {
		t.Fatalf("unexpected cross-tenant children: %#v", got)
	}
	if got := Build("", "t1", children, grants); len(got) != 0 {
		t.Fatalf("anonymous guardian has children: %#v", got)
	}
}

func TestBuildRevokedAndConflictingGrants(t *testing.T) {
	children := []Child{{ID: "c1", TenantID: "t", DisplayName: "Child"}}
	grants := []authz.GuardianGrant{
		{GuardianID: "g", ChildID: "c1", TenantID: "t", Active: true, CanManageDevices: true, CanApproveContacts: true},
		{GuardianID: "g", ChildID: "c1", TenantID: "t", Active: true, CanManageDevices: false, CanApproveContacts: true},
		{GuardianID: "g", ChildID: "c1", TenantID: "t", Active: false, CanManageDevices: true},
	}
	got := Build("g", "t", children, grants)
	if len(got) != 1 || got[0].Permissions.ManageDevices || !got[0].Permissions.ApproveContacts {
		t.Fatalf("conflicting grants did not restrict permission: %#v", got)
	}
	grants[0].Active = false
	grants[1].Active = false
	if got := Build("g", "t", children, grants); len(got) != 0 {
		t.Fatalf("revoked grants still visible: %#v", got)
	}
}
