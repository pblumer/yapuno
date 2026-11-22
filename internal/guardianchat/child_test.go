package guardianchat

import (
	"errors"
	"testing"

	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/messages"
)

func principal(a Actor) messages.Principal {
	return messages.Principal{Party: contacts.Party{TenantID: a.Participant.TenantID, ChildID: a.Participant.ID}, DeviceID: a.DeviceID}
}

func TestTheChildSideAppliesTheSameRulesInTheChildrensTypes(t *testing.T) {
	w := newWorld(t)
	side := w.store.ForChildren()
	id := w.thread(anna, alex)
	w.send(anna, id, "Znacht isch parat")
	me := principal(alex)
	if !side.Has(me, id) || side.Has(principal(ada), id) || side.Has(principal(bea), id) || side.Has(me, w.thread(anna, bruno)) || side.Has(me, "") {
		t.Fatal("Has does not follow the child's own threads")
	}
	if len(side.Threads(me)) != 2 || len(side.Threads(principal(Child("family-a", "alex", "other-phone")))) != 0 {
		t.Fatal("threads do not follow the device")
	}
	sent, dup, err := side.Send(me, id, "client-id-0000001", "Chume grad")
	if err != nil || dup || sent.Seq != 2 || sent.From != me.Party {
		t.Fatalf("send: %+v %v %v", sent, dup, err)
	}
	got, err := side.Recent(me, id, 50)
	if err != nil || len(got) != 2 {
		t.Fatalf("recent: %v %v", got, err)
	}
	// The guardian's message is never the child's own, the child's is.
	if got[0].From == me.Party || got[0].From.ChildID != "" || got[0].From.TenantID != "family-a" || got[1].From != me.Party {
		t.Fatalf("senders: %+v", got)
	}
	if after, err := side.History(me, id, 1, 50); err != nil || len(after) != 1 || after[0].Body != "Chume grad" {
		t.Fatalf("history: %v %v", after, err)
	}
	// Errors arrive as the children's errors, so the pages explain them.
	for name, check := range map[string]struct {
		err  error
		want error
	}{
		"foreign thread":  {func() error { _, _, err := side.Send(principal(ada), id, "client-id-0000002", "x"); return err }(), messages.ErrForbidden},
		"foreign read":    {func() error { _, err := side.Recent(principal(bea), id, 5); return err }(), messages.ErrForbidden},
		"foreign history": {func() error { _, err := side.History(principal(bea), id, 0, 5); return err }(), messages.ErrForbidden},
		"empty text":      {func() error { _, _, err := side.Send(me, id, "client-id-0000003", "  "); return err }(), messages.ErrInvalid},
		"conflict":        {func() error { _, _, err := side.Send(me, id, "client-id-0000001", "anders"); return err }(), messages.ErrConflict},
	} {
		if !errors.Is(check.err, check.want) {
			t.Errorf("%s: %v", name, check.err)
		}
	}
	for i := 1; i < MaxPerMinute; i++ {
		if _, _, err := side.Send(me, id, cid(), "viel"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := side.Send(me, id, cid(), "zu viel"); !errors.Is(err, messages.ErrRateLimited) {
		t.Fatalf("rate limit: %v", err)
	}
	w.store.failWrite = true
	w.clock.add(2 * 60 * 1e9)
	if _, _, err := side.Send(me, id, cid(), "verloren"); !errors.Is(err, messages.ErrUnavailable) {
		t.Fatalf("failed write: %v", err)
	}
}
