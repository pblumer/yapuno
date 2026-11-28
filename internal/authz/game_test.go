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
