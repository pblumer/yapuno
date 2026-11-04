package authz

import "testing"

func TestGuardianGrantScopedToChild(t *testing.T) {
	grants := []GuardianGrant{
		{GuardianID: "parent-a", ChildID: "child-1", TenantID: "family", Active: true, CanManageDevices: true, CanApproveContacts: true},
		{GuardianID: "parent-a", ChildID: "child-2", TenantID: "family", Active: true, CanManageDevices: false, CanApproveContacts: true},
		{GuardianID: "parent-b", ChildID: "child-1", TenantID: "family", Active: true, CanManageDevices: true},
	}
	tests := []struct {
		name string
		guardian string
		child string
		tenant string
		grant GuardianGrant
		wantDevice bool
		wantContact bool
	}{
		{"first child", "parent-a", "child-1", "family", grants[0], true, true},
		{"second child restricted", "parent-a", "child-2", "family", grants[1], false, true},
		{"second guardian", "parent-b", "child-1", "family", grants[2], true, false},
		{"wrong child", "parent-a", "child-3", "family", grants[0], false, false},
		{"wrong guardian", "parent-b", "child-1", "family", grants[0], false, false},
		{"cross tenant", "parent-a", "child-1", "other", grants[0], false, false},
		{"no identity", "", "child-1", "family", grants[0], false, false},
		{"revoked", "parent-a", "child-1", "family", GuardianGrant{GuardianID: "parent-a", ChildID: "child-1", TenantID: "family", CanManageDevices: true, CanApproveContacts: true}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanManageChildDevice(tt.guardian, tt.child, tt.tenant, tt.grant); got != tt.wantDevice {
				t.Fatalf("device access = %v, want %v", got, tt.wantDevice)
			}
			if got := CanApproveChildContacts(tt.guardian, tt.child, tt.tenant, tt.grant); got != tt.wantContact {
				t.Fatalf("contact access = %v, want %v", got, tt.wantContact)
			}
		})
	}
}

func TestContactPairsAndRevocation(t *testing.T) {
	for _, pair := range []struct {
		name string
		a ParticipantKind
		b ParticipantKind
	}{
		{"child-child", ParticipantChild, ParticipantChild},
		{"adult-child", ParticipantAdult, ParticipantChild},
		{"adult-adult", ParticipantAdult, ParticipantAdult},
	} {
		t.Run(pair.name, func(t *testing.T) {
			g := ContactGrant{TenantID: "family", FirstID: "one", SecondID: "two", FirstKind: pair.a, SecondKind: pair.b, FirstApproved: true, SecondApproved: true, Active: true}
			if !CanContact(g, "family", "one", "two") || !CanContact(g, "family", "two", "one") {
				t.Fatal("mutually approved pair denied")
			}
			if CanContact(g, "other", "one", "two") || CanContact(g, "family", "one", "stranger") {
				t.Fatal("cross-tenant or stranger contact allowed")
			}
			g.SecondApproved = false
			if CanContact(g, "family", "one", "two") {
				t.Fatal("one-sided consent allowed")
			}
			g.SecondApproved = true
			g.Active = false
			if CanContact(g, "family", "one", "two") {
				t.Fatal("revoked contact allowed")
			}
			g.Active = true
			g.FirstKind = ParticipantUnknown
			if CanContact(g, "family", "one", "two") {
				t.Fatal("unknown participant type allowed")
			}
		})
	}
}
