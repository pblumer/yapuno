package authz

// GameAccess is the validated, current state behind one attempt to invite
// to, act in or read a game between two children. Every field must be loaded
// from trusted storage at the moment of the action.
type GameAccess struct {
	// Conversation is the same state a message between the two would need:
	// both profiles, the acting device and the stored contact grant.
	Conversation ConversationAccess
	// SelfGameEnabled and PeerGameEnabled say that the guardians of each
	// child currently allow this game for that child.
	SelfGameEnabled bool
	PeerGameEnabled bool
}

// CanPlayGame requires everything a message requires, both children being
// children, and the game being enabled for each of them by their own
// guardians. A game never opens a channel the contact would not allow.
func CanPlayGame(a GameAccess) bool {
	c := a.Conversation
	return c.Self.Kind == ParticipantChild && c.Peer.Kind == ParticipantChild &&
		a.SelfGameEnabled && a.PeerGameEnabled && c.allowed()
}
