package chess

// HintKind is the most important thing a child should notice in a position.
type HintKind uint8

const (
	HintNone HintKind = iota
	HintMateInOne
	HintCheck
	HintThreatMate
	HintHanging
	HintWinPiece
	HintDevelop
	HintCastle
	HintActivate
)

// Hint points at one idea. Square and Piece are set for HintHanging,
// HintWinPiece and HintDevelop.
type Hint struct {
	Kind   HintKind
	Square Square
	Piece  PieceType
}

// Analyze looks at the position from the side to move. It is a heuristic
// for children: attackers and defenders are counted without exchanges,
// x-rays or pins, so a hint can be wrong.
func Analyze(p Position) Hint {
	us, them := p.turn, p.turn.Other()
	if mateInOne(p) {
		return Hint{Kind: HintMateInOne}
	}
	if p.InCheck() {
		return Hint{Kind: HintCheck}
	}
	threat := p
	threat.turn, threat.ep = them, noSquare
	if mateInOne(threat) {
		return Hint{Kind: HintThreatMate}
	}
	// An own piece in danger: the most valuable one.
	var hanging Hint
	for s := Square(0); s < 64; s++ {
		pc := p.board[s]
		if pc.Color != us || pc.Type == King || !p.attacked(s, them) {
			continue
		}
		if !p.attacked(s, us) || p.cheapestAttacker(s, them) < pieceValues[pc.Type] {
			if pieceValues[pc.Type] > pieceValues[hanging.Piece] {
				hanging = Hint{Kind: HintHanging, Square: s, Piece: pc.Type}
			}
		}
	}
	if hanging.Kind != HintNone {
		return hanging
	}
	// Something to win: the most valuable undefended or underdefended target.
	var win Hint
	for _, m := range p.LegalMoves() {
		target := p.board[m.To]
		if target.Color != them {
			continue
		}
		if !p.attacked(m.To, them) || pieceValues[target.Type] > pieceValues[p.board[m.From].Type] {
			if pieceValues[target.Type] > pieceValues[win.Piece] {
				win = Hint{Kind: HintWinPiece, Square: m.To, Piece: target.Type}
			}
		}
	}
	if win.Kind != HintNone {
		return win
	}
	if p.fullmove <= 12 {
		home := []struct {
			s Square
			t PieceType
		}{{6, Knight}, {1, Knight}, {5, Bishop}, {2, Bishop}}
		for _, h := range home {
			s := h.s
			if us == Black {
				s += 56
			}
			if p.board[s] == (Piece{h.t, us}) {
				return Hint{Kind: HintDevelop, Square: s, Piece: h.t}
			}
		}
	}
	if p.fullmove <= 20 {
		for _, m := range p.LegalMoves() {
			if p.board[m.From].Type == King && abs(int(m.To)-int(m.From)) == 2 {
				return Hint{Kind: HintCastle}
			}
		}
	}
	return Hint{Kind: HintActivate}
}

func mateInOne(p Position) bool {
	for _, m := range p.LegalMoves() {
		n := p.apply(m)
		if n.InCheck() && len(n.LegalMoves()) == 0 {
			return true
		}
	}
	return false
}

// cheapestAttacker is the value of the cheapest piece of colour by that
// attacks s, or a value above any piece if there is none.
func (p Position) cheapestAttacker(s Square, by Color) int {
	best := 1 << 30
	for from := Square(0); from < 64; from++ {
		pc := p.board[from]
		if pc.Color == by && p.attacksFrom(from, s) {
			v := pieceValues[pc.Type]
			if pc.Type == King {
				v = 10000
			}
			if v < best {
				best = v
			}
		}
	}
	return best
}

// attacksFrom reports whether the piece on from attacks s.
func (p Position) attacksFrom(from, s Square) bool {
	pc := p.board[from]
	df, dr := s.File()-from.File(), s.Rank()-from.Rank()
	switch pc.Type {
	case Pawn:
		dir := 1
		if pc.Color == Black {
			dir = -1
		}
		return dr == dir && abs(df) == 1
	case Knight:
		return (abs(df) == 1 && abs(dr) == 2) || (abs(df) == 2 && abs(dr) == 1)
	case King:
		return max(abs(df), abs(dr)) == 1
	}
	diagonal, straight := abs(df) == abs(dr) && df != 0, (df == 0) != (dr == 0)
	if !(pc.Type == Bishop && diagonal || pc.Type == Rook && straight || pc.Type == Queen && (diagonal || straight)) {
		return false
	}
	sf, sr := sign(df), sign(dr)
	for f, r := from.File()+sf, from.Rank()+sr; f != s.File() || r != s.Rank(); f, r = f+sf, r+sr {
		if p.board[r*8+f].Type != NoPiece {
			return false
		}
	}
	return true
}

func sign(x int) int {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	}
	return 0
}
