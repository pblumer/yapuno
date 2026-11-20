package authz

import "testing"

func TestBlockedContactGrantsNothingInEitherDirection(t *testing.T) {
	a := Participant{TenantID: "family-a", ID: "lea", Kind: ParticipantChild}
	b := Participant{TenantID: "family-b", ID: "mia", Kind: ParticipantChild}
	grant := ContactGrant{First: a, Second: b, FirstApproved: true, SecondApproved: true, Active: true}
	if !CanContact(grant, a, b) || !CanContact(grant, b, a) {
		t.Fatal("precondition: approved contact")
	}
	grant.Blocked = true
	if CanContact(grant, a, b) || CanContact(grant, b, a) {
		t.Fatal("a blocked contact still allows contact")
	}
	access := ConversationAccess{Self: a, Peer: b, SelfEnabled: true, PeerEnabled: true, DeviceApproved: true, Contact: grant}
	if CanSendMessage(access) || CanReadConversation(access) {
		t.Fatal("a blocked contact still allows sending or reading")
	}
	access.Self, access.Peer = b, a
	if CanSendMessage(access) || CanReadConversation(access) {
		t.Fatal("the blocked side can still send or read")
	}
}
