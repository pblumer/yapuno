package authz

// GuardianGrant is an authoritative, persisted relationship between one adult
// and one child. One adult may hold grants for multiple children and vice versa.
type GuardianGrant struct {
	GuardianID         string
	ChildID            string
	TenantID           string
	Active             bool
	CanManageDevices   bool
	CanApproveContacts bool
}

// CanManageChildDevice verifies a specific authenticated guardian-child grant.
// A chat relationship must never imply administrative authority.
func CanManageChildDevice(authenticatedGuardianID, childID, tenantID string, grant GuardianGrant) bool {
	return authenticatedGuardianID != "" && childID != "" && tenantID != "" &&
		grant.GuardianID == authenticatedGuardianID &&
		grant.ChildID == childID &&
		grant.TenantID == tenantID &&
		grant.Active && grant.CanManageDevices
}

// CanApproveChildContacts checks contact-management authority for one child.
func CanApproveChildContacts(authenticatedGuardianID, childID, tenantID string, grant GuardianGrant) bool {
	return authenticatedGuardianID != "" && childID != "" && tenantID != "" &&
		grant.GuardianID == authenticatedGuardianID &&
		grant.ChildID == childID &&
		grant.TenantID == tenantID &&
		grant.Active && grant.CanApproveContacts
}

// ParticipantKind distinguishes chat identities; neither kind alone grants
// access to another participant's inbox or administration.
type ParticipantKind uint8

const (
	ParticipantUnknown ParticipantKind = iota
	ParticipantChild
	ParticipantAdult
)

// Participant is a tenant-qualified chat identity. The same ID in another
// tenant is a different participant.
type Participant struct {
	TenantID string
	ID       string
	Kind     ParticipantKind
}

func (p Participant) valid() bool {
	return p.TenantID != "" && p.ID != "" && (p.Kind == ParticipantAdult || p.Kind == ParticipantChild)
}

// ContactGrant describes current mutual contact consent for exactly two
// tenant-qualified participants, who may belong to different family tenants.
// It is the only cross-family relationship and grants nothing beyond this
// pair: no tenant-wide access, no discovery, no guardian authority.
// Consent for child participants must be issued through authorized guardians.
// Caller must load these values from trusted storage.
type ContactGrant struct {
	First          Participant
	Second         Participant
	FirstApproved  bool
	SecondApproved bool
	Active         bool
	// Blocked says that at least one of the two has blocked the other. A
	// block suspends the contact for both; it does not end the consent.
	Blocked bool
}

// CanContact authorizes the relationship, not the message operation. Device,
// membership, sender status, and read/stream permissions need separate checks.
func CanContact(grant ContactGrant, sender, recipient Participant) bool {
	if !sender.valid() || !recipient.valid() || sender == recipient ||
		!grant.First.valid() || !grant.Second.valid() || grant.First == grant.Second ||
		!grant.Active || !grant.FirstApproved || !grant.SecondApproved || grant.Blocked {
		return false
	}
	return (grant.First == sender && grant.Second == recipient) ||
		(grant.Second == sender && grant.First == recipient)
}
