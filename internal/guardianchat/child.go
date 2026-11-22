package guardianchat

import (
	"errors"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/messages"
)

// ChildSide presents the store to the children's chat pages, which speak the
// types of package messages. It changes no rule: every call is authorized by
// the store for the child and device of the principal.
type ChildSide struct{ store *Store }

// ForChildren returns the child-facing view of the store.
func (s *Store) ForChildren() ChildSide { return ChildSide{store: s} }

func actor(p messages.Principal) Actor {
	return Child(p.Party.TenantID, p.Party.ChildID, p.DeviceID)
}

// Threads lists the child's conversations with its own guardians.
func (c ChildSide) Threads(p messages.Principal) []Thread {
	return c.store.Threads(actor(p))
}

// Has reports whether the id names one of the child's conversations with a
// guardian right now. Callers use it to choose the store for an id.
func (c ChildSide) Has(p messages.Principal, id string) bool {
	for _, t := range c.Threads(p) {
		if t.ID == id {
			return true
		}
	}
	return false
}

// translate maps a message to the children's type. A guardian has no child
// id, so a guardian's message never compares equal to the child itself.
func translate(m Message) messages.Message {
	from := contacts.Party{TenantID: m.From.TenantID}
	if m.From.Kind == authz.ParticipantChild {
		from.ChildID = m.From.ID
	}
	return messages.Message{Seq: m.Seq, From: from, ClientID: m.ClientID, Body: m.Body, At: m.At}
}

func translateAll(in []Message, err error) ([]messages.Message, error) {
	if err != nil {
		return nil, translateError(err)
	}
	out := make([]messages.Message, 0, len(in))
	for _, m := range in {
		out = append(out, translate(m))
	}
	return out, nil
}

func translateError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrForbidden):
		return messages.ErrForbidden
	case errors.Is(err, ErrInvalid):
		return messages.ErrInvalid
	case errors.Is(err, ErrConflict):
		return messages.ErrConflict
	case errors.Is(err, ErrRateLimited):
		return messages.ErrRateLimited
	}
	return messages.ErrUnavailable
}

func (c ChildSide) Send(p messages.Principal, id, clientID, body string) (messages.Message, bool, error) {
	m, duplicate, err := c.store.Send(actor(p), id, clientID, body)
	if err != nil {
		return messages.Message{}, false, translateError(err)
	}
	return translate(m), duplicate, nil
}

func (c ChildSide) History(p messages.Principal, id string, after uint64, limit int) ([]messages.Message, error) {
	return translateAll(c.store.History(actor(p), id, after, limit))
}

func (c ChildSide) Recent(p messages.Principal, id string, limit int) ([]messages.Message, error) {
	return translateAll(c.store.Recent(actor(p), id, limit))
}
