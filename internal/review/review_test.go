package review

import (
	"testing"

	"github.com/pblumer/yapuno/internal/chess"
)

func moves(t *testing.T, uci ...string) []chess.Move {
	t.Helper()
	g := chess.NewGame()
	var out []chess.Move
	for _, s := range uci {
		m, err := chess.ParseUCI(s)
		if err != nil || g.Play(m) != nil {
			t.Fatalf("move %s", s)
		}
		out = append(out, m)
	}
	return out
}

func TestMissedAndAllowedMate(t *testing.T) {
	// 3...Nf6 allows Qxf7#, and white plays 4.a3 instead.
	game := moves(t, "e2e4", "e7e5", "d1h5", "b8c6", "f1c4", "g8f6", "a2a3")
	white := Review(game, chess.White)
	if len(white) != 1 || white[0].Kind != MissedMate || white[0].Ply != 6 || white[0].Better.String() != "h5f7" || white[0].Played.String() != "a2a3" {
		t.Fatalf("white: %+v", white)
	}
	black := Review(game, chess.Black)
	found := false
	for _, f := range black {
		if f.Kind == AllowedMate && f.Ply == 5 && f.Played.String() == "g8f6" {
			found = true
			if !f.Before.Legal(f.Better) || f.Better == f.Played {
				t.Errorf("better move: %+v", f)
			}
		}
	}
	if !found {
		t.Fatalf("black: %+v", black)
	}
}

func TestLostMaterial(t *testing.T) {
	// 3.Qxe5+ loses the queen to Nxe5.
	game := moves(t, "e2e4", "e7e5", "d1h5", "b8c6", "h5e5", "c6e5")
	white := Review(game, chess.White)
	if len(white) != 1 || white[0].Kind != LostMaterial || white[0].Ply != 4 || white[0].Loss < 500 {
		t.Fatalf("white: %+v", white)
	}
	if words := white[0].Loss; words <= 0 {
		t.Fatal("loss")
	}
}

func TestQuietGameHasNoFindingsAndAtMostThree(t *testing.T) {
	if f := Review(moves(t, "e2e4", "e7e5", "g1f3", "b8c6", "f1c4", "f8c5"), chess.White); len(f) != 0 {
		t.Fatalf("quiet game: %+v", f)
	}
	// White gives away a piece on every move: only the three worst are kept,
	// in the order they were played.
	game := moves(t, "b1c3", "e7e5", "c3d5", "d7d6", "d5c7", "d8c7", "g1f3", "c7c2", "f3e5", "d6e5", "d1c2")
	f := Review(game, chess.White)
	if len(f) > MaxFindings {
		t.Fatalf("too many: %d", len(f))
	}
	for i := 1; i < len(f); i++ {
		if f[i].Ply <= f[i-1].Ply {
			t.Fatalf("not in order: %+v", f)
		}
	}
}

func TestCachedIsTheSameAndCopies(t *testing.T) {
	game := moves(t, "e2e4", "e7e5", "d1h5", "b8c6", "h5e5", "c6e5")
	a := Cached(game, chess.White)
	b := Cached(game, chess.White)
	if len(a) != 1 || len(b) != 1 || a[0] != b[0] {
		t.Fatalf("cache: %+v %+v", a, b)
	}
	a[0].Ply = 99
	if Cached(game, chess.White)[0].Ply == 99 {
		t.Fatal("cache shares its slice")
	}
	if Cached(game, chess.Black) == nil {
		t.Fatal("nil instead of an empty result")
	}
}

func TestOnlyTheWorstFindingsAreKept(t *testing.T) {
	// A deterministic, careless game with many mistakes on both sides.
	g := chess.NewGame()
	var game []chess.Move
	for i := 0; i < 60 && g.Status().Kind == chess.Ongoing; i++ {
		legal := g.Position().LegalMoves()
		m := legal[(i*7+3)%len(legal)]
		g.Play(m)
		game = append(game, m)
	}
	all := findings(game, chess.White)
	if len(all) <= MaxFindings {
		t.Fatalf("precondition: %d findings", len(all))
	}
	kept := Review(game, chess.White)
	if len(kept) != MaxFindings {
		t.Fatalf("kept %d", len(kept))
	}
	worst := 0
	for _, f := range all {
		if f.Kind == LostMaterial && f.Loss > worst {
			worst = f.Loss
		}
	}
	hasWorst := false
	for _, f := range kept {
		hasWorst = hasWorst || f.Kind != LostMaterial || f.Loss == worst
	}
	if !hasWorst {
		t.Fatalf("worst finding dropped: %+v of %+v", kept, all)
	}
}
