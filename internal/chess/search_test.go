package chess

import "testing"

func mustFEN(t *testing.T, fen string) Position {
	t.Helper()
	p, err := ParseFEN(fen)
	if err != nil {
		t.Fatalf("%s: %v", fen, err)
	}
	return p
}

func TestSearchFindsMateAndMaterial(t *testing.T) {
	cases := []struct {
		name, fen, want string
		depth           int
	}{
		{"back-rank mate", "6k1/5ppp/8/8/8/8/8/R5K1 w - - 0 1", "a1a8", 1},
		{"undefended queen", "4k3/8/8/3q4/8/8/3R4/4K3 w - - 0 1", "d2d5", 1},
	}
	for _, c := range cases {
		m, _, ok := Search(mustFEN(t, c.fen), SearchOptions{Depth: c.depth})
		if !ok || m.String() != c.want {
			t.Errorf("%s: %v, want %s", c.name, m, c.want)
		}
	}
	m, score, _ := Search(mustFEN(t, "6k1/5ppp/8/8/8/8/8/R5K1 w - - 0 1"), SearchOptions{Depth: 2, Quiescence: true})
	if m.String() != "a1a8" || score < MateScore-10 {
		t.Errorf("mate in one at depth 2: %v %d", m, score)
	}
}

func TestSearchSavesAnAttackedQueen(t *testing.T) {
	p := mustFEN(t, "4k3/8/8/2p5/1Q6/8/8/4K3 w - - 0 1")
	m, score, ok := Search(p, SearchOptions{Depth: 2, Quiescence: true})
	// Keeping the queen leaves white about a queen ahead; losing it would
	// score below zero.
	if !ok || score < 600 {
		t.Fatalf("queen not saved: %v %d", m, score)
	}
}

func TestSearchIsDeterministicAndBounded(t *testing.T) {
	p := mustFEN(t, "r1bqkbnr/pppp1ppp/2n5/4p3/4P3/5N2/PPPP1PPP/RNBQKB1R w KQkq - 2 3")
	a, sa, _ := Search(p, SearchOptions{Depth: 3, Quiescence: true, NodeLimit: 20000})
	b, sb, _ := Search(p, SearchOptions{Depth: 3, Quiescence: true, NodeLimit: 20000})
	if a != b || sa != sb || !p.Legal(a) {
		t.Fatalf("not deterministic or illegal: %v %v", a, b)
	}
	m, _, ok := Search(p, SearchOptions{Depth: 6, Quiescence: true, NodeLimit: 50})
	if !ok || !p.Legal(m) {
		t.Fatalf("tiny node limit: %v %v", m, ok)
	}
	if _, _, ok := Search(mustFEN(t, "7k/5Q2/6K1/8/8/8/8/8 b - - 0 1"), SearchOptions{Depth: 2}); ok {
		t.Fatal("move found in stalemate")
	}
}

func TestSearchRandomnessStaysWithinMargin(t *testing.T) {
	p := StartPosition()
	seen := map[Move]bool{}
	for i := 0; i < 20; i++ {
		i := i
		m, _, ok := Search(p, SearchOptions{Depth: 1, Margin: 1000, Rand: func(n int) int { return (i * 7) % n }})
		if !ok || !p.Legal(m) {
			t.Fatal("illegal random move")
		}
		seen[m] = true
	}
	if len(seen) < 3 {
		t.Fatalf("no variety: %v", seen)
	}
	// A huge blunder is never within a small margin.
	q := mustFEN(t, "4k3/8/8/3q4/8/8/3R4/4K3 w - - 0 1")
	for i := 0; i < 10; i++ {
		i := i
		m, _, _ := Search(q, SearchOptions{Depth: 1, Margin: 100, Rand: func(n int) int { return i % n }})
		if m.String() != "d2d5" {
			t.Fatalf("left the queen: %v", m)
		}
	}
}

func TestAnalyze(t *testing.T) {
	cases := []struct {
		name, fen string
		want      Hint
	}{
		{"mate in one", "6k1/5ppp/8/8/8/8/8/R5K1 w - - 0 1", Hint{Kind: HintMateInOne}},
		{"in check", "4k3/8/8/8/8/8/4r3/4K3 w - - 0 1", Hint{Kind: HintCheck}},
		{"threatened mate", "3r2k1/5ppp/8/8/8/8/5PPP/6K1 w - - 0 1", Hint{Kind: HintThreatMate}},
		{"queen in danger", "4k3/8/8/8/8/2n5/8/3QK3 w - - 0 1", Hint{Kind: HintHanging, Square: 3, Piece: Queen}},
		{"piece to win", "4k3/8/8/3q4/8/8/3R4/4K3 w - - 0 1", Hint{Kind: HintWinPiece, Square: 35, Piece: Queen}},
		{"develop", StartFEN, Hint{Kind: HintDevelop, Square: 6, Piece: Knight}},
		{"develop black", "rnbqkbnr/pppppppp/8/8/4P3/8/PPPP1PPP/RNBQKBNR b KQkq e3 0 1", Hint{Kind: HintDevelop, Square: 62, Piece: Knight}},
		{"castle", "r1bqk2r/pppp1ppp/2n2n2/2b1p3/2B1P3/2NP1N2/PPPB1PPP/R2QK2R w KQkq - 0 6", Hint{Kind: HintCastle}},
		{"quiet", "4k3/8/8/8/8/8/4P3/4K3 w - - 0 40", Hint{Kind: HintActivate}},
	}
	for _, c := range cases {
		if got := Analyze(mustFEN(t, c.fen)); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
	// A defended piece attacked by a more valuable one is not in danger.
	if got := Analyze(mustFEN(t, "4k3/8/8/3q4/8/8/3R4/3K4 w - - 0 30")); got.Kind == HintHanging {
		t.Errorf("defended rook reported: %+v", got)
	}
}

func TestSearchPrefersFasterMateAndSeesRecaptures(t *testing.T) {
	_, score, _ := Search(mustFEN(t, "6k1/5ppp/8/8/8/8/8/R5K1 w - - 0 1"), SearchOptions{Depth: 3, Quiescence: true})
	if score != MateScore-1 {
		t.Errorf("mate in one scores %d, want %d", score, MateScore-1)
	}
	// The pawn on d5 is defended: taking it with the queen loses the queen.
	poisoned := mustFEN(t, "4k3/8/2p5/3p4/8/8/8/3QK3 w - - 0 1")
	if m, _, _ := Search(poisoned, SearchOptions{Depth: 1}); m.String() != "d1d5" {
		t.Fatalf("precondition: without quiescence the horizon hides the recapture, got %v", m)
	}
	if m, _, _ := Search(poisoned, SearchOptions{Depth: 1, Quiescence: true}); m.String() == "d1d5" {
		t.Error("quiescence did not see the recapture")
	}
}
