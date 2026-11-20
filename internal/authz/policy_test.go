package authz

import "testing"

func approvedAccess() ConversationAccess {
	lea := Participant{TenantID: "family-a", ID: "lea", Kind: ParticipantChild}
	mia := Participant{TenantID: "family-b", ID: "mia", Kind: ParticipantChild}
	return ConversationAccess{
		Self: lea, Peer: mia, SelfEnabled: true, PeerEnabled: true, DeviceApproved: true,
		Contact: ContactGrant{First: mia, Second: lea, FirstApproved: true, SecondApproved: true, Active: true},
	}
}

func TestConversationAccessFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		change func(*ConversationAccess)
		want   bool
	}{
		{"all approvals across two families", func(*ConversationAccess) {}, true},
		{"self disabled", func(a *ConversationAccess) { a.SelfEnabled = false }, false},
		{"peer disabled", func(a *ConversationAccess) { a.PeerEnabled = false }, false},
		{"device revoked", func(a *ConversationAccess) { a.DeviceApproved = false }, false},
		{"contact revoked", func(a *ConversationAccess) { a.Contact.Active = false }, false},
		{"own side not approved", func(a *ConversationAccess) { a.Contact.SecondApproved = false }, false},
		{"peer side not approved", func(a *ConversationAccess) { a.Contact.FirstApproved = false }, false},
		{"no tenant", func(a *ConversationAccess) { a.Self.TenantID = "" }, false},
		{"no id", func(a *ConversationAccess) { a.Self.ID = "" }, false},
		{"unknown kind", func(a *ConversationAccess) { a.Self.Kind = ParticipantUnknown }, false},
		{"message to self", func(a *ConversationAccess) { a.Peer = a.Self }, false},
		{"self claims another tenant", func(a *ConversationAccess) { a.Self.TenantID = "family-b" }, false},
		{"same child id in a third family", func(a *ConversationAccess) { a.Peer.TenantID = "family-c" }, false},
		{"peer is not in the grant", func(a *ConversationAccess) { a.Peer.ID = "noah" }, false},
		{"grant is for two other people", func(a *ConversationAccess) {
			a.Contact.First.ID, a.Contact.Second.ID = "x", "y"
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := approvedAccess()
			tt.change(&a)
			if got := CanSendMessage(a); got != tt.want {
				t.Fatalf("CanSendMessage() = %t, want %t", got, tt.want)
			}
			if got := CanReadConversation(a); got != tt.want {
				t.Fatalf("CanReadConversation() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestConversationAccessZeroValueDenied(t *testing.T) {
	if CanSendMessage(ConversationAccess{}) || CanReadConversation(ConversationAccess{}) {
		t.Fatal("zero-value access must be denied")
	}
}
