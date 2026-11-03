// Package authz implements small, server-authoritative authorization predicates.
// Callers must load all values from trusted persistence, never from a client payload.
package authz

// MessageAccess represents the validated, current grant state of a potential
// one-to-one message. It is not a token or a client-supplied request body.
type MessageAccess struct {
	TenantID                 string
	SenderTenantID           string
	RecipientTenantID        string
	SenderID                 string
	RecipientID              string
	SenderEnabled            bool
	RecipientEnabled         bool
	SenderDeviceApproved     bool
	SenderContactApproved    bool
	RecipientContactApproved bool
}

// CanSendMessage is intentionally fail-closed. Authentication, loading current
// grants, membership, and enforcement for reads/streams are separate required
// checks before this predicate can authorize a real HTTP or websocket action.
func CanSendMessage(g MessageAccess) bool {
	return g.TenantID != "" &&
		g.SenderTenantID == g.TenantID &&
		g.RecipientTenantID == g.TenantID &&
		g.SenderID != "" &&
		g.RecipientID != "" &&
		g.SenderID != g.RecipientID &&
		g.SenderEnabled && g.RecipientEnabled &&
		g.SenderDeviceApproved &&
		g.SenderContactApproved && g.RecipientContactApproved
}
