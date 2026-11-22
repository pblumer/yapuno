package authz

import "testing"

var (
	anna  = Participant{TenantID: "family-a", ID: "anna", Kind: ParticipantAdult}
	alex  = Participant{TenantID: "family-a", ID: "alex", Kind: ParticipantChild}
	bruno = Participant{TenantID: "family-b", ID: "bruno", Kind: ParticipantAdult}
	bea   = Participant{TenantID: "family-b", ID: "bea", Kind: ParticipantChild}
)

func grantFor(g, c Participant) GuardianGrant {
	return GuardianGrant{GuardianID: g.ID, ChildID: c.ID, TenantID: c.TenantID, Active: true, CanManageDevices: true, CanApproveContacts: true}
}

func familyChat() FamilyChatAccess {
	return FamilyChatAccess{Self: anna, Guardian: anna, Child: alex, Grant: grantFor(anna, alex), GuardianActive: true, ChildEnabled: true}
}

func TestGuardianAndOwnChildMayChat(t *testing.T) {
	if !CanUseFamilyChat(familyChat()) {
		t.Fatal("guardian denied with own child")
	}
	asChild := familyChat()
	asChild.Self, asChild.DeviceApproved = alex, true
	if !CanUseFamilyChat(asChild) {
		t.Fatal("child denied with own guardian")
	}
	// Chatting needs no management capability, only a current relationship.
	plain := familyChat()
	plain.Grant.CanManageDevices, plain.Grant.CanApproveContacts = false, false
	if !CanUseFamilyChat(plain) {
		t.Fatal("a guardian without management rights cannot talk to the own child")
	}
}

func TestFamilyChatIsDeniedWithoutACurrentRelationship(t *testing.T) {
	cases := map[string]func(*FamilyChatAccess){
		"grant revoked":               func(a *FamilyChatAccess) { a.Grant.Active = false },
		"grant for another guardian":  func(a *FamilyChatAccess) { a.Grant.GuardianID = "mallory" },
		"grant for another child":     func(a *FamilyChatAccess) { a.Grant.ChildID = "other" },
		"grant from another tenant":   func(a *FamilyChatAccess) { a.Grant.TenantID = "family-b" },
		"forged: no grant at all":     func(a *FamilyChatAccess) { a.Grant = GuardianGrant{} },
		"guardian locked or inactive": func(a *FamilyChatAccess) { a.GuardianActive = false },
		"child profile gone":          func(a *FamilyChatAccess) { a.ChildEnabled = false },
		"child of another family":     func(a *FamilyChatAccess) { a.Child = bea; a.Grant = grantFor(anna, bea) },
		"same id in another tenant":   func(a *FamilyChatAccess) { a.Guardian.TenantID, a.Self.TenantID = "family-b", "family-b" },
		"two adults":                  func(a *FamilyChatAccess) { a.Child.Kind = ParticipantAdult },
		"two children":                func(a *FamilyChatAccess) { a.Guardian.Kind, a.Self.Kind = ParticipantChild, ParticipantChild },
		"unknown kind":                func(a *FamilyChatAccess) { a.Child.Kind = ParticipantUnknown },
		"acting as a third party":     func(a *FamilyChatAccess) { a.Self = bruno },
		"acting as the other family's child": func(a *FamilyChatAccess) {
			a.Self, a.DeviceApproved = bea, true
		},
		"empty guardian": func(a *FamilyChatAccess) {
			a.Guardian, a.Self = Participant{Kind: ParticipantAdult}, Participant{Kind: ParticipantAdult}
		},
	}
	for name, change := range cases {
		a := familyChat()
		change(&a)
		if CanUseFamilyChat(a) {
			t.Errorf("%s: allowed", name)
		}
	}
	// The child always needs a currently approved device; the guardian's
	// session is not a device and needs none.
	asChild := familyChat()
	asChild.Self = alex
	if CanUseFamilyChat(asChild) {
		t.Error("child without an approved device allowed")
	}
}

func parentsChat() ParentsChatAccess {
	return ParentsChatAccess{
		Self: anna, Peer: bruno, SelfActive: true, PeerActive: true,
		Contact:   ContactGrant{First: alex, Second: bea, FirstApproved: true, SecondApproved: true, Active: true},
		SelfGrant: grantFor(anna, alex), PeerGrant: grantFor(bruno, bea),
	}
}

func TestGuardiansOfConnectedChildrenMayChat(t *testing.T) {
	if !CanUseParentsChat(parentsChat()) {
		t.Fatal("guardians of connected children denied")
	}
	// The stored order of the two children does not matter.
	swapped := parentsChat()
	swapped.Contact.First, swapped.Contact.Second = bea, alex
	if !CanUseParentsChat(swapped) {
		t.Fatal("order of the contact matters")
	}
	// A block between the children pauses their chat, not the parents':
	// that is when the adults need to talk.
	blocked := parentsChat()
	blocked.Contact.Blocked = true
	if !CanUseParentsChat(blocked) {
		t.Fatal("a block between the children silenced the parents")
	}
	if CanContact(blocked.Contact, alex, bea) {
		t.Fatal("precondition: the children's contact is paused")
	}
}

func TestParentsChatIsDeniedWithoutAConnectionThroughTheirChildren(t *testing.T) {
	cases := map[string]func(*ParentsChatAccess){
		"contact ended":                  func(a *ParentsChatAccess) { a.Contact.Active = false },
		"contact not approved by first":  func(a *ParentsChatAccess) { a.Contact.FirstApproved = false },
		"contact not approved by second": func(a *ParentsChatAccess) { a.Contact.SecondApproved = false },
		"no contact":                     func(a *ParentsChatAccess) { a.Contact = ContactGrant{} },
		"own grant revoked":              func(a *ParentsChatAccess) { a.SelfGrant.Active = false },
		"peer grant revoked":             func(a *ParentsChatAccess) { a.PeerGrant.Active = false },
		"no contact authority (self)":    func(a *ParentsChatAccess) { a.SelfGrant.CanApproveContacts = false },
		"no contact authority (peer)":    func(a *ParentsChatAccess) { a.PeerGrant.CanApproveContacts = false },
		"forged grant for another child": func(a *ParentsChatAccess) { a.SelfGrant.ChildID = "someone" },
		"grant of another guardian":      func(a *ParentsChatAccess) { a.PeerGrant.GuardianID = "mallory" },
		"self locked or inactive":        func(a *ParentsChatAccess) { a.SelfActive = false },
		"peer locked or inactive":        func(a *ParentsChatAccess) { a.PeerActive = false },
		"same family": func(a *ParentsChatAccess) {
			a.Peer = Participant{TenantID: "family-a", ID: "albert", Kind: ParticipantAdult}
		},
		"with oneself":               func(a *ParentsChatAccess) { a.Peer = a.Self },
		"adult with the other child": func(a *ParentsChatAccess) { a.Peer = bea },
		"child acting as adult":      func(a *ParentsChatAccess) { a.Self = alex },
		"contact between other children": func(a *ParentsChatAccess) {
			a.Contact.First = Participant{TenantID: "family-c", ID: "cem", Kind: ParticipantChild}
		},
		"contact children in one family": func(a *ParentsChatAccess) {
			a.Contact.Second = Participant{TenantID: "family-a", ID: "ada", Kind: ParticipantChild}
		},
		"contact between adults": func(a *ParentsChatAccess) { a.Contact.First, a.Contact.Second = anna, bruno },
		"grants swapped":         func(a *ParentsChatAccess) { a.SelfGrant, a.PeerGrant = a.PeerGrant, a.SelfGrant },
		// Ids are tenant-qualified: "anna" in family c is another person,
		// and her grant must not serve anna of family a.
		"same guardian id in another tenant": func(a *ParentsChatAccess) {
			cem := Participant{TenantID: "family-c", ID: "cem", Kind: ParticipantChild}
			a.Contact.First, a.Contact.Second = bea, cem
			a.SelfGrant = GuardianGrant{GuardianID: "anna", ChildID: "cem", TenantID: "family-c", Active: true, CanManageDevices: true, CanApproveContacts: true}
		},
		"co-guardians through a contact of siblings": func(a *ParentsChatAccess) {
			ada := Participant{TenantID: "family-a", ID: "ada", Kind: ParticipantChild}
			albert := Participant{TenantID: "family-a", ID: "albert", Kind: ParticipantAdult}
			a.Peer, a.Contact.Second, a.PeerGrant = albert, ada, grantFor(albert, ada)
		},
		"a child that carries a guardian's id":      func(a *ParentsChatAccess) { a.Self.Kind = ParticipantChild },
		"a peer child that carries a guardian's id": func(a *ParentsChatAccess) { a.Peer.Kind = ParticipantChild },
		"contact participants marked as adults": func(a *ParentsChatAccess) {
			a.Contact.First.Kind, a.Contact.Second.Kind = ParticipantAdult, ParticipantAdult
		},
		"empty self": func(a *ParentsChatAccess) { a.Self = Participant{Kind: ParticipantAdult} },
	}
	for name, change := range cases {
		a := parentsChat()
		change(&a)
		if CanUseParentsChat(a) {
			t.Errorf("%s: allowed", name)
		}
	}
}

func TestTheThreePairTypesStaySeparate(t *testing.T) {
	// A guardian's authority over a child is no contact with that child's
	// friend, and a contact between children is no authority for an adult.
	contact := ContactGrant{First: alex, Second: bea, FirstApproved: true, SecondApproved: true, Active: true}
	if CanContact(contact, anna, bea) || CanContact(contact, anna, bruno) {
		t.Fatal("a child contact was usable by an adult")
	}
	stranger := FamilyChatAccess{Self: anna, Guardian: anna, Child: bea, Grant: grantFor(anna, alex), GuardianActive: true, ChildEnabled: true}
	if CanUseFamilyChat(stranger) {
		t.Fatal("an adult reached another family's child")
	}
}
