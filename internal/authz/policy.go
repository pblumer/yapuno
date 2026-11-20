// Package authz implements small, server-authoritative authorization predicates.
// Callers must load all values from trusted persistence, never from a client payload.
package authz

// ConversationAccess is the validated, current state behind one attempt to
// send to or read a one-to-one conversation. It is not a token or a
// client-supplied request body: every field must be loaded from trusted
// storage at the moment of the action.
type ConversationAccess struct {
	// Self is the authenticated participant acting on the conversation.
	Self Participant
	// Peer is the other participant, taken from the stored contact.
	Peer Participant
	// SelfEnabled and PeerEnabled say that both profiles currently exist.
	SelfEnabled bool
	PeerEnabled bool
	// DeviceApproved says that the device used by Self is currently approved.
	DeviceApproved bool
	// Contact is the stored mutual-consent record for exactly these two.
	Contact ContactGrant
}

func (a ConversationAccess) allowed() bool {
	return a.SelfEnabled && a.PeerEnabled && a.DeviceApproved && CanContact(a.Contact, a.Self, a.Peer)
}

// CanSendMessage is intentionally fail-closed. Self and Peer may belong to
// different family tenants; the tenant-qualified contact grant is the only
// thing that links them. Authentication and loading the current state are
// separate required steps before this predicate can authorize an action.
func CanSendMessage(a ConversationAccess) bool { return a.allowed() }

// CanReadConversation applies to history and to every delivery of a message,
// including realtime streams. Today it requires the same state as sending;
// it is a separate predicate so the two can diverge deliberately.
func CanReadConversation(a ConversationAccess) bool { return a.allowed() }
