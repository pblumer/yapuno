package practice

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/chess"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/games"
	"github.com/pblumer/yapuno/internal/play"
)

type world struct {
	path     string
	families *familystore.Store
	pairing  *devicepairing.Store
	settings *play.Settings
	store    *Store
	now      time.Time
	lea, mia Principal
}

var (
	leaParty = contacts.Party{TenantID: "family-a", ChildID: "lea"}
	miaParty = contacts.Party{TenantID: "family-b", ChildID: "mia"}
)

func (w *world) clock() time.Time { return w.now }

func (w *world) reopen(t *testing.T) {
	t.Helper()
	s, err := Open(w.path, w.pairing, w.families, w.settings, w.clock)
	if err != nil {
		t.Fatal(err)
	}
	s.rand = func(n int) int { return 0 }
	w.store = s
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	w := &world{now: time.Date(2026, 11, 28, 16, 0, 0, 0, time.UTC), path: filepath.Join(dir, "practice.json")}
	var err error
	if w.families, err = familystore.Open(filepath.Join(dir, "families.json")); err != nil {
		t.Fatal(err)
	}
	for p, g := range map[contacts.Party]string{leaParty: "anna", miaParty: "bruno"} {
		grant := authz.GuardianGrant{GuardianID: g, ChildID: p.ChildID, TenantID: p.TenantID, Active: true, CanManageDevices: true, CanApproveContacts: true}
		if err := w.families.Bootstrap(familydashboard.Child{ID: p.ChildID, TenantID: p.TenantID, DisplayName: p.ChildID}, grant); err != nil {
			t.Fatal(err)
		}
	}
	if w.pairing, err = devicepairing.Open(filepath.Join(dir, "pairing.json"), w.families, w.clock); err != nil {
		t.Fatal(err)
	}
	if w.settings, err = play.OpenSettings(filepath.Join(dir, "play.json"), w.families); err != nil {
		t.Fatal(err)
	}
	w.reopen(t)
	leaDevice, _ := pairingtest.Pair(t, w.pairing, "anna", "family-a", "lea")
	miaDevice, _ := pairingtest.Pair(t, w.pairing, "bruno", "family-b", "mia")
	w.lea, w.mia = Principal{Party: leaParty, DeviceID: leaDevice}, Principal{Party: miaParty, DeviceID: miaDevice}
	for _, c := range [][3]string{{"anna", "family-a", "lea"}, {"bruno", "family-b", "mia"}} {
		if err := w.settings.SetEnabled(c[0], c[1], c[2], play.GameChess, true); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func (w *world) act(p Principal, a games.Action) (View, error) { return w.store.Act(p, a) }

func TestPracticeAgainstTheComputer(t *testing.T) {
	w := newWorld(t)
	if _, ok, err := w.store.Current(w.lea); ok || err != nil {
		t.Fatalf("game before start: %v %v", ok, err)
	}
	v, err := w.store.Start(w.lea, Medium, "white")
	if err != nil || !v.YourTurn() || v.YourColor != chess.White || len(v.Moves) != 0 || v.Level != Medium || !v.Tips || v.TipsLeft != -1 {
		t.Fatalf("start: %+v %v", v, err)
	}
	v, err = w.act(w.lea, games.Action{Kind: games.Move, Move: "e2e4", Ply: 0})
	if err != nil || len(v.Moves) != 2 || !v.YourTurn() || !v.CanTakeback {
		t.Fatalf("after first move: %+v %v", v, err)
	}
	reply := v.Moves[1]
	// A retry of the same move changes nothing.
	again, err := w.act(w.lea, games.Action{Kind: games.Move, Move: "e2e4", Ply: 0})
	if err != nil || len(again.Moves) != 2 || again.Moves[1] != reply {
		t.Fatalf("retry: %+v %v", again, err)
	}
	for name, c := range map[string]struct {
		a    games.Action
		want error
	}{
		"illegal":      {games.Action{Kind: games.Move, Move: "e4e6", Ply: 2}, games.ErrIllegal},
		"stale":        {games.Action{Kind: games.Move, Move: "d2d4", Ply: 0}, games.ErrStale},
		"notation":     {games.Action{Kind: games.Move, Move: "d2-d4", Ply: 2}, games.ErrInvalid},
		"bad level":    {games.Action{Kind: games.Hint, Level: 3, Ply: 2}, games.ErrInvalid},
		"level 2 only": {games.Action{Kind: games.Hint, Level: 2, Ply: 2}, games.ErrNotNow},
		"draw offer":   {games.Action{Kind: games.OfferDraw}, games.ErrInvalid},
	} {
		if _, err := w.act(w.lea, c.a); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	v, err = w.act(w.lea, games.Action{Kind: games.Hint, Level: 1, Ply: 2})
	if err != nil || v.Tip == nil || v.Tip.Level != 1 {
		t.Fatalf("tip: %+v %v", v, err)
	}
	v, err = w.act(w.lea, games.Action{Kind: games.Hint, Level: 2, Ply: 2})
	if err != nil || v.Tip == nil || v.Tip.Level != 2 || !v.Position.Legal(v.Tip.Best) {
		t.Fatalf("tip level 2: %+v %v", v, err)
	}
	w.reopen(t)
	if v, ok, _ := w.store.Current(w.lea); !ok || len(v.Moves) != 2 || v.Tip == nil || v.Tip.Level != 2 {
		t.Fatalf("after restart: %+v", v)
	}
	// Taking back returns to before the own last move, without its tips.
	v, err = w.act(w.lea, games.Action{Kind: games.Move, Move: "d2d4", Ply: 2})
	if err != nil || len(v.Moves) != 4 {
		t.Fatalf("second move: %+v %v", v, err)
	}
	v, err = w.act(w.lea, games.Action{Kind: Takeback, Ply: 4})
	if err != nil || len(v.Moves) != 2 || v.Tip == nil {
		t.Fatalf("takeback: %+v %v", v, err)
	}
	v, err = w.act(w.lea, games.Action{Kind: Takeback, Ply: 2})
	if err != nil || len(v.Moves) != 0 || v.CanTakeback || v.Tip != nil {
		t.Fatalf("takeback to start: %+v %v", v, err)
	}
	if _, err := w.act(w.lea, games.Action{Kind: Takeback, Ply: 0}); !errors.Is(err, games.ErrNotNow) {
		t.Fatalf("takeback without own move: %v", err)
	}
	v, err = w.act(w.lea, games.Action{Kind: games.Resign})
	if err != nil || v.Phase != games.Finished || v.End != games.EndResigned || v.Winner != chess.Black {
		t.Fatalf("resign: %+v %v", v, err)
	}
	if _, err := w.act(w.lea, games.Action{Kind: games.Move, Move: "e2e4", Ply: 0}); !errors.Is(err, games.ErrNotNow) {
		t.Fatalf("move after resign: %v", err)
	}
	// Mia's practice is her own.
	if _, ok, _ := w.store.Current(w.mia); ok {
		t.Fatal("Mia sees Lea's game")
	}
}

func TestComputerMovesFirstWhenTheChildPlaysBlack(t *testing.T) {
	w := newWorld(t)
	v, err := w.store.Start(w.lea, Hard, "black")
	if err != nil || v.YourColor != chess.Black || len(v.Moves) != 1 || !v.YourTurn() || v.CanTakeback {
		t.Fatalf("start as black: %+v %v", v, err)
	}
	if _, err := w.store.Start(w.lea, Level("grandmaster"), "white"); !errors.Is(err, games.ErrInvalid) {
		t.Fatalf("unknown level: %v", err)
	}
	if _, err := w.store.Start(w.lea, Easy, "green"); !errors.Is(err, games.ErrInvalid) {
		t.Fatalf("unknown colour: %v", err)
	}
	w.store.rand = func(n int) int { return n - 1 }
	if v, err := w.store.Start(w.lea, Easy, "random"); err != nil || v.YourColor == chess.NoColor {
		t.Fatalf("random colour: %+v %v", v, err)
	}
}

func TestPracticeIsAuthorized(t *testing.T) {
	w := newWorld(t)
	extra, _ := pairingtest.Pair(t, w.pairing, "anna", "family-a", "lea")
	cases := map[string]func() Principal{
		"empty":                func() Principal { return Principal{} },
		"other child's device": func() Principal { return Principal{Party: leaParty, DeviceID: w.mia.DeviceID} },
		"revoked device": func() Principal {
			w.pairing.Revoke("anna", "family-a", "lea", extra)
			return Principal{Party: leaParty, DeviceID: extra}
		},
		"chess switched off": func() Principal {
			w.settings.SetEnabled("bruno", "family-b", "mia", play.GameChess, false)
			return w.mia
		},
	}
	for name, setup := range cases {
		p := setup()
		if _, err := w.store.Start(p, Easy, "white"); !errors.Is(err, games.ErrForbidden) {
			t.Errorf("%s Start: %v", name, err)
		}
		if _, _, err := w.store.Current(p); !errors.Is(err, games.ErrForbidden) {
			t.Errorf("%s Current: %v", name, err)
		}
		if _, err := w.act(p, games.Action{Kind: games.Resign}); !errors.Is(err, games.ErrForbidden) {
			t.Errorf("%s Act: %v", name, err)
		}
	}
	if _, err := os.Stat(w.path); !errors.Is(err, os.ErrNotExist) {
		t.Error("a refused call wrote the file")
	}
}

func TestRateLimitAndDurability(t *testing.T) {
	w := newWorld(t)
	if _, err := w.store.Start(w.lea, Easy, "white"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < MaxActionsPerMinute; i++ {
		if _, err := w.act(w.lea, games.Action{Kind: games.Hint, Level: 1, Ply: 0}); err != nil && !errors.Is(err, games.ErrNotNow) {
			t.Fatalf("action %d: %v", i, err)
		}
	}
	if _, err := w.act(w.lea, games.Action{Kind: games.Move, Move: "e2e4", Ply: 0}); !errors.Is(err, games.ErrRateLimited) {
		t.Fatalf("over the limit: %v", err)
	}
	w.now = w.now.Add(time.Minute)
	before, _ := os.ReadFile(w.path)
	w.store.write = func(string, []byte) error { return errors.New("disk full") }
	if _, err := w.act(w.lea, games.Action{Kind: games.Move, Move: "e2e4", Ply: 0}); !errors.Is(err, games.ErrUnavailable) {
		t.Fatalf("failed write acknowledged: %v", err)
	}
	if v, _, _ := w.store.Current(w.lea); len(v.Moves) != 0 {
		t.Fatal("unsaved move became visible")
	}
	if after, _ := os.ReadFile(w.path); string(after) != string(before) {
		t.Fatal("file changed")
	}
	w.store.write = writeFile
	if v, err := w.act(w.lea, games.Action{Kind: games.Move, Move: "e2e4", Ply: 0}); err != nil || len(v.Moves) != 2 {
		t.Fatalf("after recovery: %+v %v", v, err)
	}
	if info, _ := os.Stat(w.path); info.Mode().Perm() != 0600 {
		t.Fatalf("mode: %v", info.Mode())
	}
}

func TestOpenFailsClosedAndPurge(t *testing.T) {
	w := newWorld(t)
	w.store.Start(w.lea, Medium, "white")
	w.act(w.lea, games.Action{Kind: games.Move, Move: "e2e4", Ply: 0})
	w.store.Start(w.mia, Easy, "white")
	good, _ := os.ReadFile(w.path)
	for name, content := range map[string]string{
		"illegal move":  strings.Replace(string(good), `"e2e4"`, `"e2e5"`, 1),
		"unknown field": strings.Replace(string(good), `"level":"medium"`, `"level":"medium","x":1`, 1),
		"unknown level": strings.Replace(string(good), `"level":"medium"`, `"level":"super"`, 1),
		"corrupt":       string(good[:len(good)/2]),
	} {
		if content == string(good) {
			t.Fatalf("%s: no change", name)
		}
		os.WriteFile(w.path, []byte(content), 0600)
		if _, err := Open(w.path, w.pairing, w.families, w.settings, w.clock); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	os.WriteFile(w.path, good, 0644)
	os.Chmod(w.path, 0644)
	if _, err := Open(w.path, w.pairing, w.families, w.settings, w.clock); err == nil {
		t.Error("world-readable file opened")
	}
	os.Chmod(w.path, 0600)
	w.reopen(t)
	if err := w.store.PurgeChild("family-a", "lea"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := w.store.Current(w.lea); ok {
		t.Fatal("purge child")
	}
	if err := w.store.PurgeTenant("family-b"); err != nil {
		t.Fatal(err)
	}
	w.reopen(t)
	if _, ok, _ := w.store.Current(w.mia); ok {
		t.Fatal("purge tenant not durable")
	}
}
