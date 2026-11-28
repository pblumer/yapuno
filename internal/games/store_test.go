package games

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/chess"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/play"
	"github.com/pblumer/yapuno/internal/rating"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var (
	leaParty  = contacts.Party{TenantID: "family-a", ChildID: "lea"}
	timParty  = contacts.Party{TenantID: "family-a", ChildID: "tim"}
	miaParty  = contacts.Party{TenantID: "family-b", ChildID: "mia"}
	noahParty = contacts.Party{TenantID: "family-c", ChildID: "lea"} // same child id as Lea
)

func guardianOf(p contacts.Party) string {
	return map[string]string{"family-a": "anna", "family-b": "bruno", "family-c": "carla"}[p.TenantID]
}

type world struct {
	dir      string
	path     string
	families *familystore.Store
	contacts *contacts.Store
	pairing  *devicepairing.Store
	settings *play.Settings
	store    *Store
	clock    *clock
	lea, mia Principal
	tim      Principal
	noah     Principal
	contact  string // between Lea and Mia
	ids      int
}

func (w *world) connect(t *testing.T, a, b contacts.Party) string {
	t.Helper()
	code, err := w.contacts.Invite(guardianOf(a), a.TenantID, a.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := w.contacts.Redeem(guardianOf(b), b.TenantID, b.ChildID, code)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.contacts.Confirm(guardianOf(a), a.TenantID, a.ChildID, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (w *world) device(t *testing.T, p contacts.Party) Principal {
	t.Helper()
	id, _ := pairingtest.Pair(t, w.pairing, guardianOf(p), p.TenantID, p.ChildID)
	return Principal{Party: p, DeviceID: id}
}

func (w *world) enable(t *testing.T, p contacts.Party, on bool) {
	t.Helper()
	if err := w.settings.SetEnabled(guardianOf(p), p.TenantID, p.ChildID, play.GameChess, on); err != nil {
		t.Fatal(err)
	}
}

func (w *world) reopen(t *testing.T) {
	t.Helper()
	if w.store != nil {
		w.store.Close()
	}
	s, err := Open(w.path, w.contacts, w.pairing, w.families, w.settings, w.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	// The inviter plays white unless a test says otherwise.
	s.coin = func() (bool, error) { return true, nil }
	w.store = s
}

// id returns a fresh valid client id.
func (w *world) id() string {
	w.ids++
	return fmt.Sprintf("client-id-%08d", w.ids)
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{dir: t.TempDir(), clock: &clock{t: time.Date(2026, 11, 28, 15, 0, 0, 0, time.UTC)}}
	var err error
	if w.families, err = familystore.Open(filepath.Join(w.dir, "families.json")); err != nil {
		t.Fatal(err)
	}
	for p, name := range map[contacts.Party]string{leaParty: "Lea", timParty: "Tim", miaParty: "Mia", noahParty: "Noah"} {
		grant := authz.GuardianGrant{GuardianID: guardianOf(p), ChildID: p.ChildID, TenantID: p.TenantID, Active: true, CanManageDevices: true, CanApproveContacts: true}
		if err := w.families.Bootstrap(familydashboard.Child{ID: p.ChildID, TenantID: p.TenantID, DisplayName: name}, grant); err != nil {
			t.Fatal(err)
		}
	}
	if w.contacts, err = contacts.Open(filepath.Join(w.dir, "contacts.json"), w.families, w.clock.now); err != nil {
		t.Fatal(err)
	}
	if w.pairing, err = devicepairing.Open(filepath.Join(w.dir, "pairing.json"), w.families, w.clock.now); err != nil {
		t.Fatal(err)
	}
	if w.settings, err = play.OpenSettings(filepath.Join(w.dir, "play.json"), w.families); err != nil {
		t.Fatal(err)
	}
	w.path = filepath.Join(w.dir, "games.log")
	w.reopen(t)
	w.lea, w.mia, w.tim, w.noah = w.device(t, leaParty), w.device(t, miaParty), w.device(t, timParty), w.device(t, noahParty)
	w.contact = w.connect(t, leaParty, miaParty)
	for _, p := range []contacts.Party{leaParty, miaParty, timParty, noahParty} {
		w.enable(t, p, true)
	}
	return w
}

// start invites Mia as Lea and lets Mia accept; Lea plays white.
func (w *world) start(t *testing.T, mode Mode) View {
	t.Helper()
	v, err := w.store.Invite(w.lea, w.contact, w.id(), mode, false)
	if err != nil {
		t.Fatal(err)
	}
	v, _, err = w.store.Act(w.mia, v.ID, Action{Kind: Accept, ClientID: w.id()})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (w *world) move(t *testing.T, p Principal, game, move string) View {
	t.Helper()
	cur, err := w.store.Game(p, game)
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := w.store.Act(p, game, Action{Kind: Move, ClientID: w.id(), Move: move, Ply: len(cur.Moves)})
	if err != nil {
		t.Fatalf("move %s: %v", move, err)
	}
	return v
}

func logSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func TestInviteAcceptAndPlayToCheckmate(t *testing.T) {
	w := newWorld(t)
	v, err := w.store.Invite(w.lea, w.contact, w.id(), Correspondence, false)
	if err != nil {
		t.Fatal(err)
	}
	if v.Phase != Invited || !v.YouInvited || v.YourColor != chess.White || v.Scope != w.contact || len(v.ID) != 32 {
		t.Fatalf("invitation view: %+v", v)
	}
	seen, err := w.store.Game(w.mia, v.ID)
	if err != nil || seen.YouInvited || seen.YourColor != chess.Black || seen.Phase != Invited {
		t.Fatalf("invitee view: %+v %v", seen, err)
	}
	if _, _, err := w.store.Act(w.lea, v.ID, Action{Kind: Accept, ClientID: w.id()}); !errors.Is(err, ErrNotNow) {
		t.Fatalf("inviter accepted own invitation: %v", err)
	}
	v, _, err = w.store.Act(w.mia, v.ID, Action{Kind: Accept, ClientID: w.id()})
	if err != nil || v.Phase != Running || v.YourTurn() {
		t.Fatalf("accept: %+v %v", v, err)
	}
	for i, m := range []string{"f2f3", "e7e5", "g2g4"} {
		p := w.lea
		if i%2 == 1 {
			p = w.mia
		}
		w.move(t, p, v.ID, m)
	}
	v = w.move(t, w.mia, v.ID, "d8h4")
	if v.Phase != Finished || v.End != EndCheckmate || v.Winner != chess.Black || v.YourColor != chess.Black {
		t.Fatalf("after mate: %+v", v)
	}
	lea, _ := w.store.Game(w.lea, v.ID)
	if lea.Winner != chess.Black || lea.YourColor != chess.White || len(lea.Moves) != 4 || lea.Version != 6 {
		t.Fatalf("loser view: %+v", lea)
	}
	if _, _, err := w.store.Act(w.lea, v.ID, Action{Kind: Resign, ClientID: w.id()}); !errors.Is(err, ErrNotNow) {
		t.Fatalf("resign after mate: %v", err)
	}
	// A new game may start once the last one has ended.
	if _, err := w.store.Invite(w.mia, w.contact, w.id(), Live10, false); err != nil {
		t.Fatalf("new game: %v", err)
	}
	latest, ok, err := w.store.Latest(w.lea, w.contact)
	if err != nil || !ok || latest.Phase != Invited || latest.Mode != Live10 || !latest.YourTurnToAnswer() {
		t.Fatalf("latest: %+v %v %v", latest, ok, err)
	}
}

func TestColourIsChosenByTheServer(t *testing.T) {
	w := newWorld(t)
	w.store.coin = func() (bool, error) { return false, nil }
	v, err := w.store.Invite(w.lea, w.contact, w.id(), Correspondence, false)
	if err != nil || v.YourColor != chess.Black {
		t.Fatalf("inviter colour: %+v %v", v, err)
	}
	w.store.Act(w.mia, v.ID, Action{Kind: Accept, ClientID: w.id()})
	if _, _, err := w.store.Act(w.lea, v.ID, Action{Kind: Move, ClientID: w.id(), Move: "e7e5", Ply: 0}); !errors.Is(err, ErrNotNow) {
		t.Fatalf("black moved first: %v", err)
	}
	w.move(t, w.mia, v.ID, "e2e4")
	w.reopen(t)
	if again, _ := w.store.Game(w.lea, v.ID); again.YourColor != chess.Black || !again.YourTurn() {
		t.Fatalf("colour after restart: %+v", again)
	}
	w.store.coin = func() (bool, error) { return false, errors.New("no randomness") }
	if _, err := w.store.Invite(w.lea, w.contact, w.id(), Correspondence, false); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy check: %v", err)
	}
}

func TestEveryEntryPointIsAuthorizedFromCurrentState(t *testing.T) {
	w := newWorld(t)
	game := w.start(t, Correspondence)
	sibling := w.device(t, leaParty)

	type caller struct {
		p       Principal
		contact string
		// contactOnly: p may play the game itself, only the contact is wrong.
		contactOnly bool
	}
	cases := map[string]func() caller{
		"outsider with own approved device": func() caller { return caller{p: w.noah, contact: w.contact} },
		"sibling in the same family":        func() caller { return caller{p: w.tim, contact: w.contact} },
		"forged party with Mia's device": func() caller {
			return caller{p: Principal{Party: leaParty, DeviceID: w.mia.DeviceID}, contact: w.contact}
		},
		"forged tenant": func() caller {
			return caller{p: Principal{Party: contacts.Party{TenantID: "family-c", ChildID: "lea"}, DeviceID: w.lea.DeviceID}, contact: w.contact}
		},
		"empty principal": func() caller { return caller{p: Principal{}, contact: w.contact} },
		"unknown contact": func() caller { return caller{p: w.lea, contact: strings.Repeat("0", 32), contactOnly: true} },
		"revoked device": func() caller {
			if err := w.pairing.Revoke("anna", "family-a", "lea", sibling.DeviceID); err != nil {
				t.Fatal(err)
			}
			return caller{p: sibling, contact: w.contact}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			c := setup()
			before := logSize(t, w.path)
			if _, err := w.store.Invite(c.p, c.contact, w.id(), Correspondence, false); !errors.Is(err, ErrForbidden) {
				t.Errorf("Invite: %v", err)
			}
			if _, _, err := w.store.Latest(c.p, c.contact); !errors.Is(err, ErrForbidden) {
				t.Errorf("Latest: %v", err)
			}
			if c.contactOnly {
				return
			}
			if _, err := w.store.Game(c.p, game.ID); !errors.Is(err, ErrForbidden) {
				t.Errorf("Game: %v", err)
			}
			for _, a := range []Action{{Kind: Move, Move: "e2e4"}, {Kind: Resign}, {Kind: OfferDraw}, {Kind: Abort}} {
				a.ClientID = w.id()
				if _, _, err := w.store.Act(c.p, game.ID, a); !errors.Is(err, ErrForbidden) {
					t.Errorf("Act %s: %v", a.Kind, err)
				}
			}
			if logSize(t, w.path) != before {
				t.Error("a refused call wrote to the log")
			}
		})
	}
	if _, err := w.store.Game(w.lea, strings.Repeat("f", 32)); !errors.Is(err, ErrForbidden) {
		t.Errorf("unknown game: %v", err)
	}
}

func TestRevocationStopsTheGame(t *testing.T) {
	revocations := map[string]func(w *world){
		"own guardian disables chess":   func(w *world) { w.settings.SetEnabled("anna", "family-a", "lea", play.GameChess, false) },
		"other guardian disables chess": func(w *world) { w.settings.SetEnabled("bruno", "family-b", "mia", play.GameChess, false) },
		"child blocks the contact":      func(w *world) { w.contacts.Block(miaParty, w.contact) },
		"guardian ends the contact":     func(w *world) { w.contacts.Remove("bruno", "family-b", "mia", w.contact) },
		"device is revoked":             func(w *world) { w.pairing.Revoke("anna", "family-a", "lea", w.lea.DeviceID) },
		"peer profile is removed":       func(w *world) { w.families.RemoveChild("bruno", "family-b", "mia") },
	}
	for name, revoke := range revocations {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			if name == "peer profile is removed" {
				// A family keeps at least one child.
				if err := w.families.Bootstrap(familydashboard.Child{ID: "max", TenantID: "family-b", DisplayName: "Max"},
					authz.GuardianGrant{GuardianID: "bruno", ChildID: "max", TenantID: "family-b", Active: true, CanManageDevices: true, CanApproveContacts: true}); err != nil {
					t.Fatal(err)
				}
			}
			game := w.start(t, Correspondence)
			revoke(w)
			if _, _, err := w.store.Act(w.lea, game.ID, Action{Kind: Move, ClientID: w.id(), Move: "e2e4", Ply: 0}); !errors.Is(err, ErrForbidden) {
				t.Errorf("move: %v", err)
			}
			if _, err := w.store.Game(w.lea, game.ID); !errors.Is(err, ErrForbidden) {
				t.Errorf("read: %v", err)
			}
			if _, err := w.store.Invite(w.lea, w.contact, w.id(), Correspondence, false); !errors.Is(err, ErrForbidden) {
				t.Errorf("invite: %v", err)
			}
		})
	}
}

func TestOneUnfinishedGamePerContactAndInvitationLimit(t *testing.T) {
	w := newWorld(t)
	v, err := w.store.Invite(w.lea, w.contact, w.id(), Correspondence, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.store.Invite(w.mia, w.contact, w.id(), Correspondence, false); !errors.Is(err, ErrBusy) {
		t.Fatalf("second open game: %v", err)
	}
	if _, _, err := w.store.Act(w.mia, v.ID, Action{Kind: Withdraw, ClientID: w.id()}); !errors.Is(err, ErrNotNow) {
		t.Fatalf("invitee withdrew: %v", err)
	}
	if _, _, err := w.store.Act(w.lea, v.ID, Action{Kind: Decline, ClientID: w.id()}); !errors.Is(err, ErrNotNow) {
		t.Fatalf("inviter declined: %v", err)
	}
	if _, _, err := w.store.Act(w.lea, v.ID, Action{Kind: Withdraw, ClientID: w.id()}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < MaxInvitesPerDay; i++ {
		v, err := w.store.Invite(w.lea, w.contact, w.id(), Correspondence, false)
		if err != nil {
			t.Fatalf("invitation %d: %v", i+1, err)
		}
		if _, _, err := w.store.Act(w.mia, v.ID, Action{Kind: Decline, ClientID: w.id()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.store.Invite(w.lea, w.contact, w.id(), Correspondence, false); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("too many invitations: %v", err)
	}
	// The other child is not limited by Lea's invitations.
	if _, err := w.store.Invite(w.mia, w.contact, w.id(), Correspondence, false); err != nil {
		t.Fatalf("Mia invites: %v", err)
	}
	if _, err := w.store.Invite(w.lea, w.contact, w.id(), Mode("live-99"), false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown mode: %v", err)
	}
	if _, err := w.store.Invite(w.lea, w.contact, "short", Correspondence, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad client id: %v", err)
	}
	w.clock.advance(24 * time.Hour)
	latest, _, _ := w.store.Latest(w.lea, w.contact)
	w.store.Act(w.lea, latest.ID, Action{Kind: Decline, ClientID: w.id()})
	if _, err := w.store.Invite(w.lea, w.contact, w.id(), Correspondence, false); err != nil {
		t.Fatalf("a day later: %v", err)
	}
}

func TestMovesAreIdempotentAndChecked(t *testing.T) {
	w := newWorld(t)
	game := w.start(t, Correspondence)
	id := w.id()
	v, dup, err := w.store.Act(w.lea, game.ID, Action{Kind: Move, ClientID: id, Move: "e2e4", Ply: 0})
	if err != nil || dup || len(v.Moves) != 1 {
		t.Fatalf("first move: %+v %v %v", v, dup, err)
	}
	size := logSize(t, w.path)
	again, dup, err := w.store.Act(w.lea, game.ID, Action{Kind: Move, ClientID: id, Move: "e2e4", Ply: 0})
	if err != nil || !dup || len(again.Moves) != 1 || logSize(t, w.path) != size {
		t.Fatalf("retry: %+v %v %v", again, dup, err)
	}
	if _, _, err := w.store.Act(w.lea, game.ID, Action{Kind: Move, ClientID: id, Move: "d2d4", Ply: 0}); !errors.Is(err, ErrConflict) {
		t.Fatalf("same id, other move: %v", err)
	}
	w.reopen(t)
	if _, dup, err := w.store.Act(w.lea, game.ID, Action{Kind: Move, ClientID: id, Move: "e2e4", Ply: 0}); err != nil || !dup {
		t.Fatalf("retry after restart: %v %v", dup, err)
	}
	checks := []struct {
		name string
		p    Principal
		a    Action
		want error
	}{
		{"not your turn", w.lea, Action{Kind: Move, Move: "d2d4", Ply: 1}, ErrNotNow},
		{"stale ply", w.mia, Action{Kind: Move, Move: "e7e5", Ply: 0}, ErrStale},
		{"future ply", w.mia, Action{Kind: Move, Move: "e7e5", Ply: 2}, ErrStale},
		{"illegal move", w.mia, Action{Kind: Move, Move: "e7e4", Ply: 1}, ErrIllegal},
		{"bad notation", w.mia, Action{Kind: Move, Move: "e7-e5", Ply: 1}, ErrInvalid},
		{"unknown action", w.mia, Action{Kind: "castle", Ply: 1}, ErrInvalid},
		{"bad client id", w.mia, Action{Kind: Move, Move: "e7e5", Ply: 1}, ErrInvalid},
	}
	for _, c := range checks {
		if c.name != "bad client id" {
			c.a.ClientID = w.id()
		}
		size := logSize(t, w.path)
		if _, _, err := w.store.Act(c.p, game.ID, c.a); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
		if logSize(t, w.path) != size {
			t.Errorf("%s: wrote to the log", c.name)
		}
	}
}

func TestConcurrentRetriesStoreOneMove(t *testing.T) {
	w := newWorld(t)
	game := w.start(t, Correspondence)
	id := w.id()
	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, results[i] = w.store.Act(w.lea, game.ID, Action{Kind: Move, ClientID: id, Move: "e2e4", Ply: 0})
		}(i)
	}
	wg.Wait()
	for _, err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if v, _ := w.store.Game(w.lea, game.ID); len(v.Moves) != 1 || v.Version != 3 {
		t.Fatalf("moves stored: %+v", v)
	}
}

func TestLiveClock(t *testing.T) {
	w := newWorld(t)
	game := w.start(t, Live5)
	if game.White != 5*time.Minute || game.Black != 5*time.Minute || game.Running != chess.NoColor {
		t.Fatalf("initial clocks: %+v", game)
	}
	// Each side's first move is untimed.
	w.clock.advance(time.Hour)
	w.move(t, w.lea, game.ID, "e2e4")
	w.clock.advance(time.Hour)
	v := w.move(t, w.mia, game.ID, "e7e5")
	if v.White != 5*time.Minute || v.Black != 5*time.Minute || v.Running != chess.White {
		t.Fatalf("after first moves: %+v", v)
	}
	w.clock.advance(10 * time.Second)
	v, _ = w.store.Game(w.mia, game.ID)
	if v.White != 5*time.Minute-10*time.Second || v.Running != chess.White {
		t.Fatalf("running clock: %+v", v)
	}
	v = w.move(t, w.lea, game.ID, "g1f3")
	if v.White != 5*time.Minute-10*time.Second+3*time.Second || v.Running != chess.Black {
		t.Fatalf("increment: %+v", v)
	}
	w.reopen(t)
	if again, _ := w.store.Game(w.lea, game.ID); again.White != v.White || again.Black != v.Black {
		t.Fatalf("clocks after restart: %+v", again)
	}
	w.clock.advance(5*time.Minute - time.Millisecond)
	if v, _ := w.store.Game(w.lea, game.ID); v.Phase != Running || v.Black != time.Millisecond {
		t.Fatalf("just before the flag: %+v", v)
	}
	w.clock.advance(time.Millisecond)
	v, _ = w.store.Game(w.lea, game.ID)
	if v.Phase != Finished || v.End != EndTimeout || v.Winner != chess.White || v.Black != 0 {
		t.Fatalf("time-out: %+v", v)
	}
	if _, _, err := w.store.Act(w.mia, game.ID, Action{Kind: Move, ClientID: w.id(), Move: "b8c6", Ply: 3}); !errors.Is(err, ErrNotNow) {
		t.Fatalf("move after the flag: %v", err)
	}
	if _, err := w.store.Invite(w.mia, w.contact, w.id(), Live5, false); err != nil {
		t.Fatalf("new game after time-out: %v", err)
	}
}

func TestTimeoutIsADrawWhenTheOpponentCannotMate(t *testing.T) {
	rules, err := chess.NewGameFromFEN("4k3/8/8/8/8/8/8/3NK3 b - - 0 40")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 28, 15, 0, 0, 0, time.UTC)
	g := &game{phase: Running, mode: Live5, rules: rules, moveAt: []time.Time{start, start}, last: start}
	g.left[chess.White], g.left[chess.Black] = time.Minute, time.Minute
	// Black is to move with two half-moves on record, so black's clock runs.
	if end, winner, _ := g.settle(start.Add(59 * time.Second)); end != "" {
		t.Fatalf("early: %s %v", end, winner)
	}
	end, winner, at := g.settle(start.Add(2 * time.Minute))
	if end != EndTimeout || winner != chess.NoColor || !at.Equal(start.Add(time.Minute)) {
		t.Fatalf("lone knight cannot win on time: %s %v %v", end, winner, at)
	}
}

func TestDeadlines(t *testing.T) {
	w := newWorld(t)
	v, _ := w.store.Invite(w.lea, w.contact, w.id(), Correspondence, false)
	w.clock.advance(InviteTTL - time.Second)
	if v, _ := w.store.Game(w.mia, v.ID); v.Phase != Invited || !v.Expires.Equal(w.clock.now().Add(time.Second)) {
		t.Fatalf("before expiry: %+v", v)
	}
	w.clock.advance(time.Second)
	if v, _ := w.store.Game(w.mia, v.ID); v.Phase != Finished || v.End != EndExpired {
		t.Fatalf("expired: %+v", v)
	}
	if _, _, err := w.store.Act(w.mia, v.ID, Action{Kind: Accept, ClientID: w.id()}); !errors.Is(err, ErrNotNow) {
		t.Fatalf("accept expired: %v", err)
	}
	game := w.start(t, Correspondence)
	w.move(t, w.lea, game.ID, "e2e4")
	w.clock.advance(AbandonAfter)
	if v, _ := w.store.Game(w.mia, game.ID); v.Phase != Finished || v.End != EndAbandoned || v.Winner != chess.NoColor {
		t.Fatalf("abandoned: %+v", v)
	}
}

func TestResignDrawAndAbort(t *testing.T) {
	w := newWorld(t)
	game := w.start(t, Live10)
	if _, _, err := w.store.Act(w.mia, game.ID, Action{Kind: Abort, ClientID: w.id()}); err != nil {
		t.Fatalf("abort before first moves: %v", err)
	}
	if v, _ := w.store.Game(w.lea, game.ID); v.End != EndAborted || v.Winner != chess.NoColor {
		t.Fatalf("aborted: %+v", v)
	}

	game = w.start(t, Correspondence)
	w.move(t, w.lea, game.ID, "e2e4")
	w.move(t, w.mia, game.ID, "e7e5")
	if _, _, err := w.store.Act(w.lea, game.ID, Action{Kind: Abort, ClientID: w.id()}); !errors.Is(err, ErrNotNow) {
		t.Fatalf("abort after both moved: %v", err)
	}
	if _, _, err := w.store.Act(w.mia, game.ID, Action{Kind: AcceptDraw, ClientID: w.id()}); !errors.Is(err, ErrNotNow) {
		t.Fatalf("accept without offer: %v", err)
	}
	v, _, err := w.store.Act(w.lea, game.ID, Action{Kind: OfferDraw, ClientID: w.id()})
	if err != nil || v.DrawOffer != chess.White {
		t.Fatalf("offer: %+v %v", v, err)
	}
	if _, _, err := w.store.Act(w.lea, game.ID, Action{Kind: AcceptDraw, ClientID: w.id()}); !errors.Is(err, ErrNotNow) {
		t.Fatalf("accept own offer: %v", err)
	}
	if _, _, err := w.store.Act(w.lea, game.ID, Action{Kind: OfferDraw, ClientID: w.id()}); !errors.Is(err, ErrNotNow) {
		t.Fatalf("second pending offer: %v", err)
	}
	// White moves; the offer stays for black.
	w.move(t, w.lea, game.ID, "g1f3")
	if v, _ := w.store.Game(w.mia, game.ID); v.DrawOffer != chess.White {
		t.Fatalf("offer after offerer's move: %+v", v)
	}
	// Black's move declines it.
	v = w.move(t, w.mia, game.ID, "b8c6")
	if v.DrawOffer != chess.NoColor {
		t.Fatalf("move did not decline: %+v", v)
	}
	w.store.Act(w.lea, game.ID, Action{Kind: OfferDraw, ClientID: w.id()})
	if v, _, err := w.store.Act(w.mia, game.ID, Action{Kind: DeclineDraw, ClientID: w.id()}); err != nil || v.DrawOffer != chess.NoColor {
		t.Fatalf("decline: %+v %v", v, err)
	}
	w.store.Act(w.lea, game.ID, Action{Kind: OfferDraw, ClientID: w.id()})
	w.store.Act(w.mia, game.ID, Action{Kind: DeclineDraw, ClientID: w.id()})
	if _, _, err := w.store.Act(w.lea, game.ID, Action{Kind: OfferDraw, ClientID: w.id()}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("fourth offer: %v", err)
	}
	v, _, err = w.store.Act(w.mia, game.ID, Action{Kind: OfferDraw, ClientID: w.id()})
	if err != nil {
		t.Fatal(err)
	}
	v, _, err = w.store.Act(w.lea, game.ID, Action{Kind: AcceptDraw, ClientID: w.id()})
	if err != nil || v.End != EndAgreed || v.Winner != chess.NoColor || v.Phase != Finished {
		t.Fatalf("agreed draw: %+v %v", v, err)
	}

	game = w.start(t, Correspondence)
	v, _, err = w.store.Act(w.lea, game.ID, Action{Kind: Resign, ClientID: w.id()})
	if err != nil || v.End != EndResigned || v.Winner != chess.Black {
		t.Fatalf("resign: %+v %v", v, err)
	}
	w.reopen(t)
	if v, _ := w.store.Game(w.mia, game.ID); v.End != EndResigned || v.Winner != chess.Black {
		t.Fatalf("resign after restart: %+v", v)
	}
}

func TestDurableBeforeAcknowledged(t *testing.T) {
	w := newWorld(t)
	game := w.start(t, Correspondence)
	size := logSize(t, w.path)
	realSync := w.store.syncFile
	for name, fail := range map[string]func(){
		"write": func() { w.store.failWrite = true },
		"sync": func() {
			w.store.syncFile = func(f *os.File) error {
				w.store.syncFile = realSync
				return errors.New("disk full")
			}
		},
	} {
		fail()
		id := w.id()
		if _, _, err := w.store.Act(w.lea, game.ID, Action{Kind: Move, ClientID: id, Move: "e2e4", Ply: 0}); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s failure acknowledged: %v", name, err)
		}
		if logSize(t, w.path) != size {
			t.Fatalf("%s failure left data", name)
		}
		if v, _ := w.store.Game(w.lea, game.ID); len(v.Moves) != 0 {
			t.Fatalf("%s failure became visible", name)
		}
	}
	id := w.id()
	if _, _, err := w.store.Act(w.lea, game.ID, Action{Kind: Move, ClientID: id, Move: "e2e4", Ply: 0}); err != nil {
		t.Fatal(err)
	}
	w.reopen(t)
	if v, _ := w.store.Game(w.lea, game.ID); len(v.Moves) != 1 {
		t.Fatalf("after restart: %+v", v)
	}
}

func TestOpenFailsClosed(t *testing.T) {
	w := newWorld(t)
	game := w.start(t, Correspondence)
	w.move(t, w.lea, game.ID, "e2e4")
	w.store.Close()
	good, err := os.ReadFile(w.path)
	if err != nil {
		t.Fatal(err)
	}
	reopen := func() error {
		_, err := Open(w.path, w.contacts, w.pairing, w.families, w.settings, w.clock.now)
		return err
	}
	// A partial final line is the trace of an unacknowledged append.
	if err := os.WriteFile(w.path, append(append([]byte{}, good...), `{"game":"`...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := reopen(); err != nil {
		t.Fatalf("partial line: %v", err)
	}
	if got, _ := os.ReadFile(w.path); string(got) != string(good) {
		t.Fatal("partial line not removed")
	}
	lines := strings.SplitAfter(string(good), "\n")
	bad := map[string]string{
		"illegal stored move": strings.Replace(string(good), `"move":"e2e4"`, `"move":"e2e5"`, 1),
		"unknown field":       strings.Replace(string(good), `"type":"accept"`, `"type":"accept","x":1`, 1),
		"skipped sequence":    lines[0] + lines[2],
		"duplicate line":      lines[0] + lines[0],
		"sequence gap":        strings.Replace(string(good), `"seq":2,`, `"seq":7,`, 1),
		"unknown type":        strings.Replace(string(good), `"type":"accept"`, `"type":"win"`, 1),
		"wrong actor":         strings.Replace(lines[0]+lines[1], `"child_id":"mia"`, `"child_id":"lea"`, 1),
	}
	for name, content := range bad {
		if content == string(good) {
			t.Fatalf("%s: replacement did not apply", name)
		}
		if err := os.WriteFile(w.path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if err := reopen(); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	if err := os.WriteFile(w.path, good, 0644); err != nil {
		t.Fatal(err)
	}
	os.Chmod(w.path, 0644)
	if err := reopen(); err == nil {
		t.Error("world-readable log opened")
	}
}

func TestCompactionDeletesEndedAndOrphanedGames(t *testing.T) {
	w := newWorld(t)
	other := w.connect(t, miaParty, noahParty)
	finished := w.start(t, Correspondence)
	w.store.Act(w.lea, finished.ID, Action{Kind: Resign, ClientID: w.id()})
	open, err := w.store.Invite(w.noah, other, w.id(), Correspondence, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.contacts.Remove("carla", "family-c", "lea", other); err != nil {
		t.Fatal(err)
	}
	if err := w.store.Compact(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(w.path)
	if strings.Contains(string(data), open.ID) || !strings.Contains(string(data), finished.ID) {
		t.Fatal("orphaned game kept or finished game dropped early")
	}
	w.clock.advance(Retention)
	if err := w.store.Compact(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(w.path)
	if strings.Contains(string(data), finished.ID) {
		t.Fatal("finished game kept beyond retention")
	}
	if _, ok, err := w.store.Latest(w.lea, w.contact); ok || err != nil {
		t.Fatalf("latest after purge: %v %v", ok, err)
	}
	w.reopen(t)
	if info, _ := os.Stat(w.path); info.Mode().Perm() != 0600 {
		t.Fatalf("mode after compaction: %v", info.Mode())
	}
}

func TestTips(t *testing.T) {
	w := newWorld(t)
	plain := w.start(t, Correspondence)
	if plain.Tips || plain.TipsLeft != 0 {
		t.Fatalf("tips in a game without tips: %+v", plain)
	}
	if _, _, err := w.store.Act(w.lea, plain.ID, Action{Kind: Hint, ClientID: w.id(), Level: 1, Ply: 0}); !errors.Is(err, ErrNotNow) {
		t.Fatalf("tip in a game without tips: %v", err)
	}
	w.store.Act(w.lea, plain.ID, Action{Kind: Resign, ClientID: w.id()})

	v, err := w.store.Invite(w.lea, w.contact, w.id(), Live10, true)
	if err != nil || !v.Tips {
		t.Fatalf("invite with tips: %+v %v", v, err)
	}
	game := v.ID
	w.store.Act(w.mia, game, Action{Kind: Accept, ClientID: w.id()})
	if v, _ := w.store.Game(w.mia, game); !v.Tips || v.TipsLeft != MaxTips {
		t.Fatalf("invitee sees tips: %+v", v)
	}
	checks := []struct {
		name string
		p    Principal
		a    Action
		want error
	}{
		{"not your turn", w.mia, Action{Kind: Hint, Level: 1, Ply: 0}, ErrNotNow},
		{"stale ply", w.lea, Action{Kind: Hint, Level: 1, Ply: 1}, ErrStale},
		{"move before level 1", w.lea, Action{Kind: Hint, Level: 2, Ply: 0}, ErrNotNow},
		{"unknown level", w.lea, Action{Kind: Hint, Level: 3, Ply: 0}, ErrInvalid},
		{"level on a move", w.lea, Action{Kind: Move, Move: "e2e4", Level: 1, Ply: 0}, ErrInvalid},
	}
	for _, c := range checks {
		c.a.ClientID = w.id()
		if _, _, err := w.store.Act(c.p, game, c.a); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	id := w.id()
	v, _, err = w.store.Act(w.lea, game, Action{Kind: Hint, ClientID: id, Level: 1, Ply: 0})
	if err != nil || v.Tip == nil || v.Tip.Level != 1 || v.Tip.Hint.Kind != chess.HintDevelop || v.TipsLeft != MaxTips-1 {
		t.Fatalf("level 1: %+v %v", v, err)
	}
	if _, dup, err := w.store.Act(w.lea, game, Action{Kind: Hint, ClientID: id, Level: 1, Ply: 0}); err != nil || !dup {
		t.Fatalf("retry: %v %v", dup, err)
	}
	if _, _, err := w.store.Act(w.lea, game, Action{Kind: Hint, ClientID: w.id(), Level: 1, Ply: 0}); !errors.Is(err, ErrNotNow) {
		t.Fatalf("second level 1 on the same move: %v", err)
	}
	v, _, err = w.store.Act(w.lea, game, Action{Kind: Hint, ClientID: w.id(), Level: 2, Ply: 0})
	if err != nil || v.Tip == nil || v.Tip.Level != 2 || !v.Position.Legal(v.Tip.Best) || v.TipsLeft != MaxTips-1 {
		t.Fatalf("level 2: %+v %v", v, err)
	}
	// The opponent sees that a tip was used, but not which.
	if m, _ := w.store.Game(w.mia, game); m.Tip != nil || m.TheirTipsUsed != 1 || m.TipsLeft != MaxTips {
		t.Fatalf("opponent view: %+v", m)
	}
	w.reopen(t)
	if again, _ := w.store.Game(w.lea, game); again.Tip == nil || again.Tip.Best != v.Tip.Best || again.TipsLeft != MaxTips-1 {
		t.Fatalf("after restart: %+v", again)
	}
	// The tip belongs to its move only.
	w.move(t, w.lea, game, "e2e4")
	if again, _ := w.store.Game(w.lea, game); again.Tip != nil {
		t.Fatal("old tip still shown")
	}
	for i := 0; i < MaxTips-1; i++ {
		w.move(t, w.mia, game, []string{"e7e5", "b8c6"}[i])
		cur, _ := w.store.Game(w.lea, game)
		if _, _, err := w.store.Act(w.lea, game, Action{Kind: Hint, ClientID: w.id(), Level: 1, Ply: len(cur.Moves)}); err != nil {
			t.Fatalf("tip %d: %v", i+2, err)
		}
		w.move(t, w.lea, game, []string{"g1f3", "f1c4"}[i])
	}
	w.move(t, w.mia, game, "g8f6")
	if _, _, err := w.store.Act(w.lea, game, Action{Kind: Hint, ClientID: w.id(), Level: 1, Ply: 6}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("fourth tip: %v", err)
	}
	// A stored tip must be consistent with the game on load.
	w.store.Close()
	data, _ := os.ReadFile(w.path)
	bad := strings.Replace(string(data), `"level":2`, `"level":5`, 1)
	os.WriteFile(w.path, []byte(bad), 0600)
	if _, err := Open(w.path, w.contacts, w.pairing, w.families, w.settings, w.clock.now); err == nil {
		t.Fatal("tampered tip opened")
	}
	for name, content := range map[string]string{
		"illegal best move": regexp.MustCompile(`"best":"[a-h1-8qrbn]+"`).ReplaceAllString(string(data), `"best":"e7e5"`),
		"tip removed":       regexp.MustCompile(`,"tip":\{[^}]*\}`).ReplaceAllString(string(data), ""),
	} {
		if content == string(data) {
			t.Fatalf("%s: replacement did not apply", name)
		}
		os.WriteFile(w.path, []byte(content), 0600)
		if _, err := Open(w.path, w.contacts, w.pairing, w.families, w.settings, w.clock.now); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	os.WriteFile(w.path, data, 0600)
	w.store = nil
	w.reopen(t)
}

func TestInvitationRetryMustMatchTips(t *testing.T) {
	w := newWorld(t)
	id := w.id()
	if _, err := w.store.Invite(w.lea, w.contact, id, Correspondence, true); err != nil {
		t.Fatal(err)
	}
	if _, err := w.store.Invite(w.lea, w.contact, id, Correspondence, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("same id without tips: %v", err)
	}
	if v, err := w.store.Invite(w.lea, w.contact, id, Correspondence, true); err != nil || !v.Tips {
		t.Fatalf("retry: %+v %v", v, err)
	}
}

type guardianDirectory struct{ locked map[string]bool }

func (d guardianDirectory) ActiveGuardian(_ context.Context, guardianID, tenantID string) bool {
	return !d.locked[tenantID+"/"+guardianID]
}

func TestFamilyGames(t *testing.T) {
	w := newWorld(t)
	anna := GuardianPrincipal{TenantID: "family-a", GuardianID: "anna"}
	bruno := GuardianPrincipal{TenantID: "family-b", GuardianID: "bruno"}
	if _, err := w.store.GuardianInvite(anna, "lea", w.id(), Correspondence, false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("family games before they are enabled: %v", err)
	}
	dir := guardianDirectory{locked: map[string]bool{}}
	if err := w.store.EnableFamilies(w.families, dir); err != nil {
		t.Fatal(err)
	}
	scope := FamilyScope("family-a", "anna", "lea")
	v, err := w.store.GuardianInvite(anna, "lea", w.id(), Live10, true)
	if err != nil || !v.Family || v.Scope != scope || v.YourColor != chess.White || !v.YouInvited {
		t.Fatalf("guardian invites: %+v %v", v, err)
	}
	game := v.ID
	seen, ok, err := w.store.Latest(w.lea, scope)
	if err != nil || !ok || !seen.YourTurnToAnswer() || seen.YourColor != chess.Black {
		t.Fatalf("child sees invitation: %+v %v %v", seen, ok, err)
	}
	if _, _, err := w.store.Act(w.lea, game, Action{Kind: Accept, ClientID: w.id()}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.store.GuardianAct(anna, game, Action{Kind: Move, ClientID: w.id(), Move: "e2e4"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.store.Act(w.lea, game, Action{Kind: Move, ClientID: w.id(), Move: "e7e5", Ply: 1}); err != nil {
		t.Fatal(err)
	}
	if v, err := w.store.GuardianGame(anna, game); err != nil || len(v.Moves) != 2 || !v.YourTurn() {
		t.Fatalf("guardian view: %+v %v", v, err)
	}
	// Nobody else reaches the game.
	if _, err := w.store.GuardianGame(bruno, game); !errors.Is(err, ErrForbidden) {
		t.Errorf("other family's guardian: %v", err)
	}
	if _, err := w.store.GuardianInvite(bruno, "lea", w.id(), Correspondence, false); !errors.Is(err, ErrForbidden) {
		t.Errorf("other family's guardian invites: %v", err)
	}
	if _, _, err := w.store.GuardianLatest(bruno, "lea"); !errors.Is(err, ErrForbidden) {
		t.Errorf("other family's guardian latest: %v", err)
	}
	for name, p := range map[string]Principal{"Mia": w.mia, "sibling Tim": w.tim, "Noah with the same child id": w.noah} {
		if _, err := w.store.Game(p, game); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s reads: %v", name, err)
		}
		if _, _, err := w.store.Latest(p, scope); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s latest: %v", name, err)
		}
	}
	// One unfinished game per pair; restart keeps the game.
	if _, err := w.store.Invite(w.lea, scope, w.id(), Correspondence, false); !errors.Is(err, ErrBusy) {
		t.Errorf("second game: %v", err)
	}
	w.reopen(t)
	if _, err := w.store.GuardianGame(anna, game); !errors.Is(err, ErrForbidden) {
		t.Fatalf("family game readable before EnableFamilies after restart: %v", err)
	}
	w.store.EnableFamilies(w.families, dir)
	if v, err := w.store.Game(w.lea, game); err != nil || len(v.Moves) != 2 || !v.Family {
		t.Fatalf("after restart: %+v %v", v, err)
	}
	// A locked guardian, a revoked device or chess switched off stop it.
	dir.locked["family-a/anna"] = true
	if _, err := w.store.GuardianGame(anna, game); !errors.Is(err, ErrForbidden) {
		t.Errorf("locked guardian: %v", err)
	}
	if _, err := w.store.Game(w.lea, game); !errors.Is(err, ErrForbidden) {
		t.Errorf("child with locked guardian: %v", err)
	}
	delete(dir.locked, "family-a/anna")
	w.enable(t, leaParty, false)
	if _, err := w.store.GuardianGame(anna, game); !errors.Is(err, ErrForbidden) {
		t.Errorf("chess off: %v", err)
	}
	w.enable(t, leaParty, true)
	if err := w.pairing.Revoke("anna", "family-a", "lea", w.lea.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.store.Game(w.lea, game); !errors.Is(err, ErrForbidden) {
		t.Errorf("revoked device: %v", err)
	}
	if _, err := w.store.GuardianGame(anna, game); err != nil {
		t.Errorf("the guardian does not need the child's device: %v", err)
	}
	// The child's profile goes: the game is deleted at compaction.
	if err := w.families.RemoveChild("anna", "family-a", "lea"); err != nil {
		t.Fatal(err)
	}
	if err := w.store.Compact(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(w.path); strings.Contains(string(data), game) {
		t.Fatal("family game of a removed child kept")
	}
}

func TestFamilyInvitationOnDiskIsChecked(t *testing.T) {
	w := newWorld(t)
	w.store.EnableFamilies(w.families, guardianDirectory{})
	v, err := w.store.GuardianInvite(GuardianPrincipal{TenantID: "family-a", GuardianID: "anna"}, "lea", w.id(), Correspondence, false)
	if err != nil {
		t.Fatal(err)
	}
	w.store.Close()
	good, _ := os.ReadFile(w.path)
	for name, content := range map[string]string{
		"scope of another pair": strings.Replace(string(good), v.Scope, FamilyScope("family-a", "anna", "tim"), 1),
		"two children":          strings.Replace(string(good), `"guardian_id":"anna"`, `"child_id":"tim"`, 1),
		"actor both":            strings.Replace(string(good), `"guardian_id":"anna"`, `"guardian_id":"anna","child_id":"tim"`, 1),
		"not marked family":     strings.Replace(string(good), `"family":true,`, ``, 1),
	} {
		if content == string(good) {
			t.Fatalf("%s: no change", name)
		}
		os.WriteFile(w.path, []byte(content), 0600)
		if _, err := Open(w.path, w.contacts, w.pairing, w.families, w.settings, w.clock.now); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
}

type recorder struct{ calls []string }

func (r *recorder) Record(key string, a, b rating.Player, scoreA float64) error {
	r.calls = append(r.calls, fmt.Sprintf("%s %s/%s %s/%s %.1f", key, a.Kind, a.ID, b.Kind, b.ID, scoreA))
	return nil
}

func TestFinishedGamesAreRatedOnce(t *testing.T) {
	w := newWorld(t)
	r := &recorder{}
	w.store.SetRater(r)
	resigned := w.start(t, Correspondence)
	w.store.Act(w.mia, resigned.ID, Action{Kind: Resign, ClientID: w.id()})
	w.store.Game(w.lea, resigned.ID)
	w.store.Game(w.mia, resigned.ID)
	if len(r.calls) != 1 || r.calls[0] != "game:"+resigned.ID+" child/lea child/mia 1.0" {
		t.Fatalf("resignation: %q", r.calls)
	}
	aborted := w.start(t, Correspondence)
	w.store.Act(w.lea, aborted.ID, Action{Kind: Abort, ClientID: w.id()})
	w.store.Game(w.lea, aborted.ID)
	if len(r.calls) != 1 {
		t.Fatalf("aborted game rated: %q", r.calls)
	}
	// An end that follows from time alone is rated when the game is read.
	live := w.start(t, Live5)
	w.move(t, w.lea, live.ID, "e2e4")
	w.move(t, w.mia, live.ID, "e7e5")
	w.clock.advance(6 * time.Minute)
	if len(r.calls) != 1 {
		t.Fatal("rated before anyone looked")
	}
	w.store.Latest(w.mia, w.contact)
	if len(r.calls) != 2 || r.calls[1] != "game:"+live.ID+" child/lea child/mia 0.0" {
		t.Fatalf("time-out: %q", r.calls)
	}
	// A restart forgets what was rated in memory; the rater is idempotent
	// by key, and the store asks again with the same key.
	w.reopen(t)
	r2 := &recorder{}
	w.store.SetRater(r2)
	for _, c := range r2.calls {
		if !strings.HasPrefix(c, "game:"+resigned.ID) && !strings.HasPrefix(c, "game:"+live.ID) {
			t.Fatalf("unexpected rating after restart: %q", c)
		}
	}
}

func TestTurnsAndInvitationsAreAnnounced(t *testing.T) {
	w := newWorld(t)
	var got []string
	w.store.SetNotifier(func(n Notice) {
		kind := "turn"
		if n.Invite {
			kind = "invite"
		}
		got = append(got, fmt.Sprintf("%s/%s %s", n.TenantID, n.ChildID, kind))
	})
	take := func() []string { out := got; got = nil; return out }
	v, err := w.store.Invite(w.lea, w.contact, w.id(), Correspondence, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := take(); len(n) != 1 || n[0] != "family-b/mia invite" {
		t.Fatalf("invitation: %q", n)
	}
	if !w.store.Waiting(w.mia, w.contact, true) || w.store.Waiting(w.mia, w.contact, false) || w.store.Waiting(w.lea, w.contact, true) {
		t.Fatal("waiting for an answer")
	}
	w.store.Act(w.mia, v.ID, Action{Kind: Accept, ClientID: w.id()})
	if n := take(); len(n) != 1 || n[0] != "family-a/lea turn" {
		t.Fatalf("accepted, white to move: %q", n)
	}
	id := w.id()
	w.store.Act(w.lea, v.ID, Action{Kind: Move, ClientID: id, Move: "e2e4"})
	if n := take(); len(n) != 1 || n[0] != "family-b/mia turn" {
		t.Fatalf("after a move: %q", n)
	}
	if !w.store.Waiting(w.mia, w.contact, false) || w.store.Waiting(w.lea, w.contact, false) {
		t.Fatal("waiting for a move")
	}
	w.store.Act(w.lea, v.ID, Action{Kind: Move, ClientID: id, Move: "e2e4"})
	w.store.Act(w.lea, v.ID, Action{Kind: OfferDraw, ClientID: w.id()})
	w.store.Act(w.mia, v.ID, Action{Kind: Resign, ClientID: w.id()})
	if n := take(); len(n) != 0 {
		t.Fatalf("retry, draw offer and resignation announced: %q", n)
	}
	if w.store.Waiting(w.noah, w.contact, false) || w.store.Waiting(w.mia, w.contact, false) {
		t.Fatal("waiting after the end or for an outsider")
	}
	// Family games: guardians are never told, the child is.
	w.store.EnableFamilies(w.families, guardianDirectory{})
	anna := GuardianPrincipal{TenantID: "family-a", GuardianID: "anna"}
	fg, err := w.store.GuardianInvite(anna, "lea", w.id(), Correspondence, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := take(); len(n) != 1 || n[0] != "family-a/lea invite" {
		t.Fatalf("guardian's invitation: %q", n)
	}
	w.store.Act(w.lea, fg.ID, Action{Kind: Accept, ClientID: w.id()})
	if n := take(); len(n) != 0 {
		t.Fatalf("guardian told: %q", n)
	}
	w.store.GuardianAct(anna, fg.ID, Action{Kind: Move, ClientID: w.id(), Move: "d2d4"})
	if n := take(); len(n) != 1 || n[0] != "family-a/lea turn" {
		t.Fatalf("guardian moved: %q", n)
	}
}
