package webpush

import (
	"context"
	"net/http"
	"testing"
	"time"
)

const game = "fedcba9876543210fedcba9876543210"

func TestNoticesSayWhatIsWaitingOnlyToTheDeviceThatWasTold(t *testing.T) {
	r := newRig(t)
	for _, d := range []Device{alexPhone, alexTablet} {
		if err := r.store.Subscribe(d, appleEndpoint+d.DeviceID); err != nil {
			t.Fatal(err)
		}
	}
	// The phone may see the chat and the game; the tablet only the chat.
	allowed := map[Device]map[Notice]bool{
		alexPhone:  {{Kind: KindMessage, ID: chat}: true, {Kind: KindTurn, ID: game}: true},
		alexTablet: {{Kind: KindMessage, ID: chat}: true},
	}
	r.n.may = func(d Device, n Notice) bool { return allowed[d][n] }
	r.n.Notify("tenant-a", "child-a", chat)
	r.n.Send("tenant-a", "child-a", Notice{Kind: KindTurn, ID: game})
	r.n.Flush(context.Background())
	if got := len(r.client.sent()); got != 2 {
		t.Fatalf("one merged push per device, got %d", got)
	}
	if got := r.n.Take(alexPhone); got != KindMessage|KindTurn {
		t.Fatalf("phone: %b", got)
	}
	if got := r.n.Take(alexPhone); got != 0 {
		t.Fatalf("taken twice: %b", got)
	}
	if got := r.n.Take(alexTablet); got != KindMessage {
		t.Fatalf("tablet: %b", got)
	}
	if got := r.n.Take(beaPhone); got != 0 {
		t.Fatalf("another child's device: %b", got)
	}
	// A notice nobody may see sends nothing and records nothing.
	r.n.Send("tenant-a", "child-a", Notice{Kind: KindInvite, ID: game})
	r.n.Flush(context.Background())
	if got := len(r.client.sent()); got != 2 || r.n.Take(alexPhone) != 0 {
		t.Fatalf("denied notice: %d pushes", got)
	}
}

func TestNoticesExpireAndAreBounded(t *testing.T) {
	r := newRig(t)
	if err := r.store.Subscribe(alexPhone, appleEndpoint); err != nil {
		t.Fatal(err)
	}
	r.readers.allow(alexPhone, game)
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	r.n.now = func() time.Time { return now }
	r.n.Send("tenant-a", "child-a", Notice{Kind: KindInvite, ID: game})
	r.n.Flush(context.Background())
	now = now.Add(NoticeLifetime)
	if got := r.n.Take(alexPhone); got != 0 {
		t.Fatalf("expired notice: %b", got)
	}
	// Invalid notices are ignored.
	for _, n := range []Notice{{Kind: 0, ID: game}, {Kind: KindTurn, ID: ""}, {Kind: 1 << 6, ID: game}, {Kind: KindTurn | KindInvite, ID: game}} {
		r.n.Send("tenant-a", "child-a", n)
	}
	r.n.Flush(context.Background())
	if got := len(r.client.sent()); got != 1 {
		t.Fatalf("invalid notices sent: %d", got)
	}
	// A push that fails still leaves the notice for the device to ask.
	r.client.status = http.StatusInternalServerError
	r.n.Send("tenant-a", "child-a", Notice{Kind: KindTurn, ID: game})
	r.n.Flush(context.Background())
	if got := r.n.Take(alexPhone); got != KindTurn {
		t.Fatalf("after a failed push: %b", got)
	}
}

func TestNoticesNotYetTakenAreMerged(t *testing.T) {
	r := newRig(t)
	if err := r.store.Subscribe(alexPhone, appleEndpoint); err != nil {
		t.Fatal(err)
	}
	r.readers.allow(alexPhone, game)
	r.n.Send("tenant-a", "child-a", Notice{Kind: KindInvite, ID: game})
	r.n.Flush(context.Background())
	r.n.Send("tenant-a", "child-a", Notice{Kind: KindTurn, ID: game})
	r.n.Flush(context.Background())
	if got := r.n.Take(alexPhone); got != KindInvite|KindTurn {
		t.Fatalf("merged: %b", got)
	}
}
