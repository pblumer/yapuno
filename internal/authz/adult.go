package authz

// FamilyChatAccess is the current state behind one attempt to use the
// conversation between a guardian and that guardian's own child. Every field
// must be loaded from trusted storage at the moment of the action.
type FamilyChatAccess struct {
	// Self is the authenticated participant: the guardian or the child.
	Self     Participant
	Guardian Participant
	Child    Participant
	// Grant is the stored relationship between exactly these two.
	Grant GuardianGrant
	// GuardianActive says the guardian identity is active and not locked.
	GuardianActive bool
	// ChildEnabled says the child profile currently exists.
	ChildEnabled bool
	// DeviceApproved says the device used by the child is currently
	// approved. It is required only when Self is the child.
	DeviceApproved bool
}

// CanUseFamilyChat authorizes sending to and reading the conversation of a
// guardian with their own child. It needs a current guardian grant for
// exactly this child, but no management capability: talking to one's child
// is not administration. It never reaches a child of another family.
func CanUseFamilyChat(a FamilyChatAccess) bool {
	if !a.Guardian.valid() || !a.Child.valid() || a.Guardian.Kind != ParticipantAdult || a.Child.Kind != ParticipantChild ||
		a.Guardian.TenantID != a.Child.TenantID || !a.GuardianActive || !a.ChildEnabled {
		return false
	}
	if a.Grant.GuardianID != a.Guardian.ID || a.Grant.ChildID != a.Child.ID || a.Grant.TenantID != a.Child.TenantID || !a.Grant.Active {
		return false
	}
	switch a.Self {
	case a.Guardian:
		return true
	case a.Child:
		return a.DeviceApproved
	}
	return false
}

// ParentsChatAccess is the current state behind one attempt to use the
// conversation between two guardians of different families.
type ParentsChatAccess struct {
	Self Participant
	Peer Participant
	// SelfActive and PeerActive say each identity is active and not locked.
	SelfActive bool
	PeerActive bool
	// Contact is a stored contact between one child of each family.
	Contact ContactGrant
	// SelfGrant and PeerGrant are each guardian's stored grant for their
	// family's child in Contact.
	SelfGrant GuardianGrant
	PeerGrant GuardianGrant
}

// CanUseParentsChat authorizes the conversation between two guardians whose
// children are connected: an approved, not ended contact between a child of
// each, for which each guardian currently holds contact authority. There is
// no other way for adults of different families to reach each other.
//
// A block between the children does not end it: the children's contact is
// paused, and that is when the adults need to talk. It grants nothing
// towards either child.
func CanUseParentsChat(a ParentsChatAccess) bool {
	if !a.Self.valid() || !a.Peer.valid() || a.Self.Kind != ParticipantAdult || a.Peer.Kind != ParticipantAdult ||
		a.Self.TenantID == a.Peer.TenantID || !a.SelfActive || !a.PeerActive {
		return false
	}
	c := a.Contact
	if !c.First.valid() || !c.Second.valid() || c.First.Kind != ParticipantChild || c.Second.Kind != ParticipantChild ||
		!c.Active || !c.FirstApproved || !c.SecondApproved {
		return false
	}
	mine, theirs := c.First, c.Second
	if mine.TenantID != a.Self.TenantID {
		mine, theirs = theirs, mine
	}
	if mine.TenantID != a.Self.TenantID || theirs.TenantID != a.Peer.TenantID {
		return false
	}
	return CanApproveChildContacts(a.Self.ID, mine.ID, mine.TenantID, a.SelfGrant) &&
		CanApproveChildContacts(a.Peer.ID, theirs.ID, theirs.TenantID, a.PeerGrant)
}
