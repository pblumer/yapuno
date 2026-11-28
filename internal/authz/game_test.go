package authz

import "testing"

func TestGameAccessFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		change func(*GameAccess)
		want   bool
	}{
		{"both children enabled across two families", func(*GameAccess) {}, true},
		{"game not enabled for self", func(a *GameAccess) { a.SelfGameEnabled = false }, false},
		{"game not enabled for peer", func(a *GameAccess) { a.PeerGameEnabled = false }, false},
		{"device revoked", func(a *GameAccess) { a.Conversation.DeviceApproved = false }, false},
		{"peer profile removed", func(a *GameAccess) { a.Conversation.PeerEnabled = false }, false},
		{"contact blocked", func(a *GameAccess) { a.Conversation.Contact.Blocked = true }, false},
		{"contact ended", func(a *GameAccess) { a.Conversation.Contact.Active = false }, false},
		{"peer side not approved", func(a *GameAccess) { a.Conversation.Contact.FirstApproved = false }, false},
		{"peer is not in the grant", func(a *GameAccess) { a.Conversation.Peer.ID = "noah" }, false},
		{"adult participant", func(a *GameAccess) { a.Conversation.Self.Kind = ParticipantAdult }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := GameAccess{Conversation: approvedAccess(), SelfGameEnabled: true, PeerGameEnabled: true}
			tt.change(&a)
			if got := CanPlayGame(a); got != tt.want {
				t.Fatalf("CanPlayGame() = %t, want %t", got, tt.want)
			}
		})
	}
	if CanPlayGame(GameAccess{}) {
		t.Fatal("zero-value access must be denied")
	}
}

func TestFamilyGameAccess(t *testing.T) {
	guardian := Participant{TenantID: "family-a", ID: "anna", Kind: ParticipantAdult}
	child := Participant{TenantID: "family-a", ID: "lea", Kind: ParticipantChild}
	base := func() FamilyGameAccess {
		return FamilyGameAccess{Chat: FamilyChatAccess{Self: child, Guardian: guardian, Child: child,
			Grant:          GuardianGrant{GuardianID: "anna", ChildID: "lea", TenantID: "family-a", Active: true},
			GuardianActive: true, ChildEnabled: true, DeviceApproved: true}, ChildGameEnabled: true}
	}
	tests := []struct {
		name   string
		change func(*FamilyGameAccess)
		want   bool
	}{
		{"child with own guardian", func(*FamilyGameAccess) {}, true},
		{"guardian acting", func(a *FamilyGameAccess) { a.Chat.Self, a.Chat.DeviceApproved = guardian, false }, true},
		{"chess not allowed", func(a *FamilyGameAccess) { a.ChildGameEnabled = false }, false},
		{"guardian locked", func(a *FamilyGameAccess) { a.Chat.GuardianActive = false }, false},
		{"grant ended", func(a *FamilyGameAccess) { a.Chat.Grant.Active = false }, false},
		{"device revoked", func(a *FamilyGameAccess) { a.Chat.DeviceApproved = false }, false},
		{"guardian of another family", func(a *FamilyGameAccess) {
			a.Chat.Guardian.TenantID, a.Chat.Grant.TenantID = "family-b", "family-b"
		}, false},
		{"grant for another child", func(a *FamilyGameAccess) { a.Chat.Grant.ChildID = "tim" }, false},
		{"outsider acting", func(a *FamilyGameAccess) {
			a.Chat.Self = Participant{TenantID: "family-a", ID: "tim", Kind: ParticipantChild}
		}, false},
	}
	for _, tt := range tests {
		a := base()
		tt.change(&a)
		if got := CanPlayFamilyGame(a); got != tt.want {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
	if CanPlayFamilyGame(FamilyGameAccess{}) {
		t.Error("zero value allowed")
	}
}
