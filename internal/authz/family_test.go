package authz

import "testing"

func TestGuardianGrantScopedToChild(t *testing.T) {
	grants := []GuardianGrant{
		{GuardianID: "parent-a", ChildID: "child-1", TenantID: "family", Active: true, CanManageDevices: true, CanApproveContacts: true},
		{GuardianID: "parent-a", ChildID: "child-2", TenantID: "family", Active: true, CanManageDevices: false, CanApproveContacts: true},
		{GuardianID: "parent-b", ChildID: "child-1", TenantID: "family", Active: true, CanManageDevices: true},
	}
	tests := []struct {
		name        string
		guardian    string
		child       string
		tenant      string
		grant       GuardianGrant
		wantDevice  bool
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
		a    ParticipantKind
		b    ParticipantKind
	}{
		{"child-child", ParticipantChild, ParticipantChild},
		{"adult-child", ParticipantAdult, ParticipantChild},
		{"adult-adult", ParticipantAdult, ParticipantAdult},
	} {
		t.Run(pair.name, func(t *testing.T) {
			one := Participant{TenantID: "family", ID: "one", Kind: pair.a}
			two := Participant{TenantID: "family", ID: "two", Kind: pair.b}
			g := ContactGrant{First: one, Second: two, FirstApproved: true, SecondApproved: true, Active: true}
			if !CanContact(g, one, two) || !CanContact(g, two, one) {
				t.Fatal("mutually approved pair denied")
			}
			stranger := Participant{TenantID: "family", ID: "stranger", Kind: pair.b}
			if CanContact(g, one, stranger) || CanContact(g, stranger, two) {
				t.Fatal("stranger contact allowed")
			}
			g.SecondApproved = false
			if CanContact(g, one, two) {
				t.Fatal("one-sided consent allowed")
			}
			g.SecondApproved = true
			g.Active = false
			if CanContact(g, one, two) {
				t.Fatal("revoked contact allowed")
			}
			g.Active = true
			g.First.Kind = ParticipantUnknown
			if CanContact(g, one, two) {
				t.Fatal("unknown participant type allowed")
			}
		})
	}
}

// Contacts may span two family tenants, but only for the exact tenant-qualified
// pair in the grant. A matching ID in another tenant is a different person.
func TestCrossFamilyContactIsTenantQualified(t *testing.T) {
	lea := Participant{TenantID: "family-a", ID: "child-1", Kind: ParticipantChild}
	mia := Participant{TenantID: "family-b", ID: "child-1", Kind: ParticipantChild}
	g := ContactGrant{First: lea, Second: mia, FirstApproved: true, SecondApproved: true, Active: true}
	if !CanContact(g, lea, mia) || !CanContact(g, mia, lea) {
		t.Fatal("approved cross-family pair denied")
	}
	denied := map[string][2]Participant{
		"same id in third tenant":   {lea, {TenantID: "family-c", ID: "child-1", Kind: ParticipantChild}},
		"sender tenant swapped":     {{TenantID: "family-b", ID: "child-1", Kind: ParticipantChild}, mia},
		"self contact":              {lea, lea},
		"empty tenant":              {{ID: "child-1", Kind: ParticipantChild}, mia},
		"empty id":                  {{TenantID: "family-a", Kind: ParticipantChild}, mia},
		"kind differs from grant":   {{TenantID: "family-a", ID: "child-1", Kind: ParticipantAdult}, mia},
		"recipient kind is unknown": {lea, {TenantID: "family-b", ID: "child-1"}},
	}
	for name, pair := range denied {
		if CanContact(g, pair[0], pair[1]) {
			t.Errorf("%s: contact allowed", name)
		}
	}
	if CanContact(ContactGrant{}, Participant{}, Participant{}) {
		t.Fatal("zero-value grant must be denied")
	}
	same := ContactGrant{First: lea, Second: lea, FirstApproved: true, SecondApproved: true, Active: true}
	if CanContact(same, lea, lea) {
		t.Fatal("grant from a participant to itself must be denied")
	}
}
