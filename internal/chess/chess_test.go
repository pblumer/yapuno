package chess

import (
	"errors"
	"testing"
)

// Reference counts from the chess programming community's perft tables
// (https://www.chessprogramming.org/Perft_Results). They cover castling,
// en passant, promotion, pins and checks exhaustively.
var perftCases = []struct {
	name   string
	fen    string
	counts []uint64 // counts[d-1] is perft(d)
	long   int      // depths above this run only without -short
}{
	{"start", StartFEN, []uint64{20, 400, 8902, 197281}, 3},
	{"kiwipete", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", []uint64{48, 2039, 97862}, 2},
	{"position 3", "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", []uint64{14, 191, 2812, 43238, 674624}, 4},
	{"position 4", "r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1", []uint64{6, 264, 9467, 422333}, 3},
	{"position 4 mirrored", "r2q1rk1/pP1p2pp/Q4n2/bbp1p3/Np6/1B3NBn/pPPP1PPP/R3K2R b KQ - 0 1", []uint64{6, 264, 9467, 422333}, 3},
	{"position 5", "rnbq1k1r/pp1Pbppp/2p5/8/2B5/8/PPP1NnPP/RNBQK2R w KQ - 1 8", []uint64{44, 1486, 62379}, 2},
	{"position 6", "r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10", []uint64{46, 2079, 89890}, 2},
}

func TestPerft(t *testing.T) {
	for _, tc := range perftCases {
		pos, err := ParseFEN(tc.fen)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		for d, want := range tc.counts {
			depth := d + 1
			if depth > tc.long && testing.Short() {
				continue
			}
			if got := Perft(pos, depth); got != want {
				t.Errorf("%s perft(%d) = %d, want %d", tc.name, depth, got, want)
			}
		}
	}
}

func TestFENRoundTrip(t *testing.T) {
	for _, tc := range perftCases {
		pos, err := ParseFEN(tc.fen)
		if err != nil {
			t.Fatal(err)
		}
		if got := pos.FEN(); got != tc.fen {
			t.Errorf("FEN() = %q, want %q", got, tc.fen)
		}
	}
}

func TestParseFENRejectsMalformed(t *testing.T) {
	for _, fen := range []string{
		"",
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq -",           // missing counters
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBN w KQkq - 0 1",        // short rank
		"rnbqkbnr/pppppppp/9/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",       // long rank
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR x KQkq - 0 1",       // turn
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkx - 0 1",       // castling
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq e9 0 1",      // en passant
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - -1 1",      // halfmove
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQQBNR w KQkq - 0 1",       // no white king
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBKKBNR w KQkq - 0 1",       // two white kings
		"rnbqkbnP/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",       // pawn on last rank
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1 extra", // trailing field
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR b KQkq - 0 1 ",      // trailing space
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 0",       // fullmove zero
	} {
		if _, err := ParseFEN(fen); !errors.Is(err, ErrInvalidFEN) {
			t.Errorf("ParseFEN(%q) err = %v, want ErrInvalidFEN", fen, err)
		}
	}
	// The side not to move must not be in check.
	if _, err := ParseFEN("4k3/8/8/8/8/8/4R3/4K3 w - - 0 1"); !errors.Is(err, ErrInvalidFEN) {
		t.Errorf("opponent in check accepted: %v", err)
	}
}

func TestParseUCI(t *testing.T) {
	good := map[string]Move{
		"e2e4":  {From: sq("e2"), To: sq("e4")},
		"e7e8q": {From: sq("e7"), To: sq("e8"), Promotion: Queen},
		"a2a1n": {From: sq("a2"), To: sq("a1"), Promotion: Knight},
	}
	for s, want := range good {
		got, err := ParseUCI(s)
		if err != nil || got != want {
			t.Errorf("ParseUCI(%q) = %v, %v; want %v", s, got, err, want)
		}
		if got.String() != s {
			t.Errorf("String() = %q, want %q", got.String(), s)
		}
	}
	for _, s := range []string{"", "e2", "e2e", "e2e9", "i2e4", "e2e4k", "e2e4p", "e2e4Q", "E2E4", "e2e2", "e2e4qq", " e2e4"} {
		if _, err := ParseUCI(s); !errors.Is(err, ErrInvalidMove) {
			t.Errorf("ParseUCI(%q) err = %v, want ErrInvalidMove", s, err)
		}
	}
}

func play(t *testing.T, g *Game, moves ...string) {
	t.Helper()
	for _, s := range moves {
		m, err := ParseUCI(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if err := g.Play(m); err != nil {
			t.Fatalf("play %s: %v", s, err)
		}
	}
}

func TestIllegalMovesAreRejected(t *testing.T) {
	cases := []struct {
		name, fen, move string
	}{
		{"empty square", StartFEN, "e3e4"},
		{"opponent piece", StartFEN, "e7e5"},
		{"blocked rook", StartFEN, "a1a3"},
		{"pawn three squares", StartFEN, "e2e5"},
		{"into check", "4k3/8/8/8/8/8/3r4/4K3 w - - 0 1", "e1d1"},
		{"pinned piece", "4k3/4r3/8/8/8/8/4B3/4K3 w - - 0 1", "e2d3"},
		{"castle through check", "4k3/8/8/8/8/8/5r2/R3K2R w KQ - 0 1", "e1g1"},
		{"castle out of check", "4k3/8/8/8/8/8/4r3/R3K2R w KQ - 0 1", "e1c1"},
		{"castle without right", "4k3/8/8/8/8/8/8/R3K2R w Q - 0 1", "e1g1"},
		{"castle blocked", "4k3/8/8/8/8/8/8/RN2K2R w KQ - 0 1", "e1c1"},
		{"promotion without piece", "4k3/P7/8/8/8/8/8/4K3 w - - 0 1", "a7a8"},
		{"promotion piece on normal move", StartFEN, "e2e4q"},
		{"en passant expired", "4k3/8/8/3pP3/8/8/8/4K3 w - - 0 1", "e5d6"},
	}
	for _, tc := range cases {
		g, err := NewGameFromFEN(tc.fen)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		m, err := ParseUCI(tc.move)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		before := g.Position().FEN()
		if err := g.Play(m); !errors.Is(err, ErrIllegalMove) {
			t.Errorf("%s: Play(%s) err = %v, want ErrIllegalMove", tc.name, tc.move, err)
		}
		if g.Position().FEN() != before || len(g.Moves()) != 0 {
			t.Errorf("%s: rejected move changed the game", tc.name)
		}
	}
}

func TestSpecialMoves(t *testing.T) {
	g, _ := NewGameFromFEN("r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 0 1")
	play(t, g, "e1g1", "e8c8")
	if got, want := g.Position().FEN(), "2kr3r/8/8/8/8/8/8/R4RK1 w - - 2 2"; got != want {
		t.Errorf("castling: %s, want %s", got, want)
	}
	g, _ = NewGameFromFEN("4k3/8/8/3pP3/8/8/8/4K3 w - d6 0 1")
	play(t, g, "e5d6")
	if got, want := g.Position().FEN(), "4k3/8/3P4/8/8/8/8/4K3 b - - 0 1"; got != want {
		t.Errorf("en passant: %s, want %s", got, want)
	}
	g, _ = NewGameFromFEN("1n2k3/P7/8/8/8/8/8/4K3 w - - 0 1")
	play(t, g, "a7b8n")
	if got, want := g.Position().FEN(), "1N2k3/8/8/8/8/8/8/4K3 b - - 0 1"; got != want {
		t.Errorf("promotion: %s, want %s", got, want)
	}
	// Capturing a rook on its home square removes that castling right.
	g, _ = NewGameFromFEN("r3k2r/8/8/8/8/8/6B1/R3K2R w KQkq - 0 1")
	play(t, g, "g2a8")
	if got, want := g.Position().FEN(), "B3k2r/8/8/8/8/8/8/R3K2R b KQk - 0 1"; got != want {
		t.Errorf("rook capture: %s, want %s", got, want)
	}
	g, _ = NewGameFromFEN("r3k2r/8/8/8/8/8/1B6/R3K2R w KQkq - 0 1")
	play(t, g, "b2h8")
	if got, want := g.Position().FEN(), "r3k2B/8/8/8/8/8/8/R3K2R b KQq - 0 1"; got != want {
		t.Errorf("rook capture h8: %s, want %s", got, want)
	}
	g, _ = NewGameFromFEN("r3k2r/6b1/8/8/8/8/8/R3K2R b KQkq - 0 1")
	play(t, g, "g7a1")
	if got, want := g.Position().FEN(), "r3k2r/8/8/8/8/8/8/b3K2R w Kkq - 0 2"; got != want {
		t.Errorf("rook capture a1: %s, want %s", got, want)
	}
	// The en passant square is written only after a double step.
	g = NewGame()
	play(t, g, "e2e4")
	if got, want := g.Position().FEN(), "rnbqkbnr/pppppppp/8/8/4P3/8/PPPP1PPP/RNBQKBNR b KQkq e3 0 1"; got != want {
		t.Errorf("double step: %s, want %s", got, want)
	}
}

func TestOutcomes(t *testing.T) {
	g := NewGame()
	play(t, g, "f2f3", "e7e5", "g2g4", "d8h4")
	if s := g.Status(); s.Kind != Checkmate || s.Winner != Black {
		t.Errorf("fool's mate: %+v", s)
	}
	if err := g.Play(Move{From: sq("a2"), To: sq("a3")}); !errors.Is(err, ErrGameOver) {
		t.Errorf("move after mate: %v", err)
	}

	g, _ = NewGameFromFEN("7k/5Q2/6K1/8/8/8/8/8 w - - 0 1")
	play(t, g, "f7f6")
	if s := g.Status(); s.Kind != Ongoing {
		t.Errorf("not yet stalemate: %+v", s)
	}
	g, _ = NewGameFromFEN("7k/8/6K1/8/8/8/8/5Q2 w - - 0 1")
	play(t, g, "f1f7")
	if s := g.Status(); s.Kind != Stalemate || s.Winner != NoColor {
		t.Errorf("stalemate: %+v", s)
	}

	g, _ = NewGameFromFEN("4k3/8/8/8/8/8/3q4/4K3 w - - 0 1")
	play(t, g, "e1d2")
	if s := g.Status(); s.Kind != InsufficientMaterial {
		t.Errorf("king against king: %+v", s)
	}

	g, _ = NewGameFromFEN("4k3/8/8/8/8/8/8/4K1NR w - - 99 80")
	play(t, g, "h1h2")
	if s := g.Status(); s.Kind != FiftyMoves {
		t.Errorf("fifty moves: %+v", s)
	}
	// Mate on the hundredth half-move wins over the fifty-move rule.
	g, _ = NewGameFromFEN("7k/8/6K1/8/8/8/8/R7 w - - 99 80")
	play(t, g, "a1a8")
	if s := g.Status(); s.Kind != Checkmate || s.Winner != White {
		t.Errorf("mate on move hundred: %+v", s)
	}

	g = NewGame()
	play(t, g, "g1f3", "g8f6", "f3g1", "f6g8", "g1f3", "g8f6", "f3g1")
	if s := g.Status(); s.Kind != Ongoing {
		t.Errorf("second occurrence is not a repetition: %+v", s)
	}
	play(t, g, "f6g8")
	if s := g.Status(); s.Kind != Repetition {
		t.Errorf("threefold repetition: %+v", s)
	}
}

// A position after a double step only counts as the same as a later one if
// en passant was not actually possible in either.
func TestRepetitionAndEnPassant(t *testing.T) {
	g, _ := NewGameFromFEN("4k3/8/8/8/3p4/8/4P3/4K1N1 w - - 0 1")
	// After e2e4 black could capture en passant, so this position differs
	// from the same arrangement reached later without that right.
	play(t, g, "e2e4", "e8d8", "g1f3", "d8e8", "f3g1", "e8d8", "g1f3", "d8e8", "f3g1")
	if s := g.Status(); s.Kind != Ongoing {
		t.Errorf("en passant position counted: %+v", s)
	}
	// The king move that follows reaches its third occurrence.
	play(t, g, "e8d8")
	if s := g.Status(); s.Kind != Repetition {
		t.Errorf("third occurrence: %+v", s)
	}
	// A double step without an enemy pawn beside it creates no right, so
	// the FEN of the position does not name an en passant square either.
	g = NewGame()
	play(t, g, "e2e4", "g8f6")
	if got := g.Position().RepetitionKey(); got != "rnbqkb1r/pppppppp/5n2/8/4P3/8/PPPP1PPP/RNBQKBNR w KQkq -" {
		t.Errorf("repetition key = %q", got)
	}
}

func TestInsufficientMaterial(t *testing.T) {
	cases := map[string]bool{
		"4k3/8/8/8/8/8/8/4K3 w - - 0 1":     true,  // K v K
		"4k3/8/8/8/8/8/8/4KB2 w - - 0 1":    true,  // K+B v K
		"4k3/8/8/8/8/8/8/4KN2 w - - 0 1":    true,  // K+N v K
		"4kb2/8/8/8/8/8/8/2B1K3 w - - 0 1":  true,  // bishops on same colour
		"4k1b1/8/8/8/8/8/8/2B1K3 w - - 0 1": false, // bishops on different colours
		"4k3/8/8/8/8/8/8/4KNN1 w - - 0 1":   false, // two knights can mate with help
		"4kn2/8/8/8/8/8/8/4KN2 w - - 0 1":   false, // knight each
		"4k3/8/8/8/8/8/4P3/4K3 w - - 0 1":   false, // pawn
		"4k3/8/8/8/8/8/8/4K2R w - - 0 1":    false, // rook
	}
	for fen, want := range cases {
		p, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		if got := p.InsufficientMaterial(); got != want {
			t.Errorf("%s: InsufficientMaterial = %v, want %v", fen, got, want)
		}
	}
	// One side alone: a lone king cannot win on time against anything.
	p, _ := ParseFEN("4k3/8/8/8/8/8/8/3QK3 w - - 0 1")
	if !p.CannotMate(Black) || p.CannotMate(White) {
		t.Errorf("CannotMate: black %v white %v", p.CannotMate(Black), p.CannotMate(White))
	}
	p, _ = ParseFEN("4k3/8/8/8/8/8/8/3NK3 w - - 0 1")
	if !p.CannotMate(White) {
		t.Error("king and knight alone cannot mate")
	}
}

func TestGameReplayAndMoves(t *testing.T) {
	g := NewGame()
	play(t, g, "e2e4", "e7e5", "g1f3")
	got := g.Moves()
	if len(got) != 3 || got[2].String() != "g1f3" {
		t.Fatalf("Moves() = %v", got)
	}
	got[0] = Move{}
	if g.Moves()[0].String() != "e2e4" {
		t.Error("Moves() exposes internal state")
	}
	if g.Position().Turn() != Black {
		t.Error("turn after three moves")
	}
	if p := g.Position().PieceAt(sq("e4")); p != (Piece{Type: Pawn, Color: White}) {
		t.Errorf("PieceAt(e4) = %+v", p)
	}
	if g.Position().LastMove() != (Move{From: sq("g1"), To: sq("f3")}) {
		t.Error("last move")
	}
	targets := g.Position().TargetsFrom(sq("b8"))
	if len(targets) != 2 {
		t.Errorf("TargetsFrom(b8) = %v", targets)
	}
}

func TestSquareNames(t *testing.T) {
	for _, name := range []string{"a1", "h1", "a8", "h8", "e4"} {
		s, ok := ParseSquare(name)
		if !ok || s.String() != name {
			t.Errorf("square %s: %v %v", name, s, ok)
		}
	}
	if s := sq("a1"); s.File() != 0 || s.Rank() != 0 {
		t.Error("a1 coordinates")
	}
	if s := sq("h8"); s.File() != 7 || s.Rank() != 7 {
		t.Error("h8 coordinates")
	}
	for _, name := range []string{"", "a", "a0", "a9", "i1", "A1", "a10"} {
		if _, ok := ParseSquare(name); ok {
			t.Errorf("ParseSquare(%q) accepted", name)
		}
	}
}

func sq(name string) Square {
	s, ok := ParseSquare(name)
	if !ok {
		panic(name)
	}
	return s
}
