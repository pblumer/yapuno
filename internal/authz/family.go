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

// ContactGrant describes current mutual contact consent for two participants.
// Consent for child participants must be issued through authorized guardians.
// Caller must load these values from trusted storage.
type ContactGrant struct {
	TenantID	string
	FirstID	string
	SecondID	string
	FirstKind	ParticipantKind
	SecondKind	ParticipantKind
	FirstApproved	bool
	SecondApproved	bool
	Active	bool
}

// CanContact authorizes the relationship, not the message operation. Device,
// membership, sender status, and read/stream permissions need separate checks.
func CanContact(grant ContactGrant, tenantID, senderID, recipientID string) bool {
	if tenantID == "" || senderID == "" || recipientID == "" || senderID == recipientID ||
		grant.TenantID != tenantID || !grant.Active || !grant.FirstApproved || !grant.SecondApproved ||
		(grant.FirstKind != ParticipantAdult && grant.FirstKind != ParticipantChild) ||
		(grant.SecondKind != ParticipantAdult && grant.SecondKind != ParticipantChild) {
		return false
	}
	return (grant.FirstID == senderID && grant.SecondID == recipientID) ||
		(grant.SecondID == senderID && grant.FirstID == recipientID)
}
