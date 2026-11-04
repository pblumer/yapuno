package authz

import "testing"

func approved() MessageAccess {
	return MessageAccess{
		TenantID: "community-1", SenderTenantID: "community-1",
		RecipientTenantID: "community-1", SenderID: "child-a",
		RecipientID: "child-b", SenderEnabled: true, RecipientEnabled: true,
		SenderDeviceApproved: true, SenderContactApproved: true,
		RecipientContactApproved: true,
	}
}

func TestCanSendMessageFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		change func(*MessageAccess)
		want   bool
	}{
		{"all approvals", func(*MessageAccess) {}, true},
		{"no tenant", func(g *MessageAccess) { g.TenantID = "" }, false},
		{"sender wrong tenant", func(g *MessageAccess) { g.SenderTenantID = "foreign" }, false},
		{"recipient wrong tenant", func(g *MessageAccess) { g.RecipientTenantID = "foreign" }, false},
		{"sender missing", func(g *MessageAccess) { g.SenderID = "" }, false},
		{"recipient missing", func(g *MessageAccess) { g.RecipientID = "" }, false},
		{"self message", func(g *MessageAccess) { g.RecipientID = g.SenderID }, false},
		{"sender revoked", func(g *MessageAccess) { g.SenderEnabled = false }, false},
		{"recipient revoked", func(g *MessageAccess) { g.RecipientEnabled = false }, false},
		{"device revoked", func(g *MessageAccess) { g.SenderDeviceApproved = false }, false},
		{"sender contact not approved", func(g *MessageAccess) { g.SenderContactApproved = false }, false},
		{"recipient contact not approved", func(g *MessageAccess) { g.RecipientContactApproved = false }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := approved()
			tt.change(&g)
			if got := CanSendMessage(g); got != tt.want {
				t.Fatalf("CanSendMessage() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestCanSendMessageZeroValueDenied(t *testing.T) {
	if CanSendMessage(MessageAccess{}) {
		t.Fatal("zero-value access must be denied")
	}
}
