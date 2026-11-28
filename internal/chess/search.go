package chess

import "sort"

// MateScore is the score of giving mate now; mate in n half-moves scores
// MateScore-n.
const MateScore = 100000

var pieceValues = [7]int{NoPiece: 0, Pawn: 100, Knight: 320, Bishop: 330, Rook: 500, Queen: 900, King: 0}

// Value is the conventional material value of a piece type in centipawns.
func Value(t PieceType) int { return pieceValues[t] }

// SearchOptions bounds a search. The search is deterministic unless Margin
// and Rand are set.
type SearchOptions struct {
	// Depth in half-moves, at least 1.
	Depth int
	// Quiescence extends the search with captures at the horizon.
	Quiescence bool
	// NodeLimit stops the search; the result of the last completed depth
	// is used. Zero means no limit.
	NodeLimit int
	// Margin and Rand choose at random among root moves scoring within
	// Margin centipawns of the best one. Rand(n) returns 0 <= i < n.
	Margin int
	Rand   func(n int) int
}

// StartPosition is the initial position.
func StartPosition() Position {
	p, err := ParseFEN(StartFEN)
	if err != nil {
		panic(err)
	}
	return p
}

// Search returns a good move for the side to move and its score from that
// side's view. ok is false when there is no legal move. It is a small
// alpha-beta search for children's practice and hints, not a strong engine.
func Search(p Position, o SearchOptions) (Move, int, bool) {
	moves := p.LegalMoves()
	if len(moves) == 0 {
		return Move{}, 0, false
	}
	orderMoves(p, moves)
	if o.Depth < 1 {
		o.Depth = 1
	}
	s := &searcher{limit: o.NodeLimit, quiesce: o.Quiescence}
	best, bestScore := moves[0], 0
	scores := make([]int, len(moves))
	for depth := 1; depth <= o.Depth; depth++ {
		alpha := -2 * MateScore
		depthBest, depthScore := Move{}, -2*MateScore
		done := true
		for i, m := range moves {
			window := alpha
			if o.Margin > 0 {
				window = -2 * MateScore // exact scores for the random choice
			}
			score := -s.negamax(p.apply(m), depth-1, -2*MateScore, -window, 1)
			if s.aborted {
				done = false
				break
			}
			scores[i] = score
			if score > depthScore {
				depthBest, depthScore = m, score
			}
			if score > alpha {
				alpha = score
			}
		}
		if !done {
			break
		}
		best, bestScore = depthBest, depthScore
		// Search the best move first at the next depth.
		for i, m := range moves {
			if m == best {
				copy(moves[1:i+1], moves[:i])
				moves[0] = best
				s := scores[i]
				copy(scores[1:i+1], scores[:i])
				scores[0] = s
				break
			}
		}
		if o.Margin > 0 && o.Rand != nil && depth == o.Depth {
			var near []Move
			for i, m := range moves {
				if scores[i] >= bestScore-o.Margin {
					near = append(near, m)
				}
			}
			best = near[o.Rand(len(near))]
		}
	}
	return best, bestScore, true
}

type searcher struct {
	nodes, limit int
	aborted      bool
	quiesce      bool
}

func (s *searcher) tick() bool {
	s.nodes++
	if s.limit > 0 && s.nodes > s.limit {
		s.aborted = true
	}
	return s.aborted
}

func (s *searcher) negamax(p Position, depth, alpha, beta, ply int) int {
	if s.tick() {
		return 0
	}
	moves := p.LegalMoves()
	if len(moves) == 0 {
		if p.InCheck() {
			return -MateScore + ply
		}
		return 0
	}
	if p.halfmove >= 100 || p.InsufficientMaterial() {
		return 0
	}
	if depth <= 0 {
		if s.quiesce {
			return s.quiescence(p, moves, alpha, beta, 4)
		}
		return evaluate(p)
	}
	orderMoves(p, moves)
	for _, m := range moves {
		score := -s.negamax(p.apply(m), depth-1, -beta, -alpha, ply+1)
		if s.aborted {
			return 0
		}
		if score >= beta {
			return score
		}
		if score > alpha {
			alpha = score
		}
	}
	return alpha
}

// quiescence follows captures and promotions only, so the evaluation is not
// taken in the middle of an exchange.
func (s *searcher) quiescence(p Position, moves []Move, alpha, beta, left int) int {
	stand := evaluate(p)
	if stand >= beta || left == 0 {
		return stand
	}
	if stand > alpha {
		alpha = stand
	}
	var tactical []Move
	for _, m := range moves {
		if p.board[m.To].Type != NoPiece || m.Promotion != NoPiece || (m.To == p.ep && p.board[m.From].Type == Pawn) {
			tactical = append(tactical, m)
		}
	}
	orderMoves(p, tactical)
	for _, m := range tactical {
		if s.tick() {
			return 0
		}
		n := p.apply(m)
		replies := n.LegalMoves()
		var score int
		if len(replies) == 0 {
			if n.InCheck() {
				score = MateScore
			}
		} else {
			score = -s.quiescence(n, replies, -beta, -alpha, left-1)
		}
		if s.aborted {
			return 0
		}
		if score >= beta {
			return score
		}
		if score > alpha {
			alpha = score
		}
	}
	return alpha
}

// orderMoves puts promotions and valuable captures by cheap pieces first.
// The order is stable, so the search stays deterministic.
func orderMoves(p Position, moves []Move) {
	key := func(m Move) int {
		k := 0
		if c := p.board[m.To]; c.Type != NoPiece {
			k = 10*pieceValues[c.Type] - pieceValues[p.board[m.From].Type]
		}
		return k + pieceValues[m.Promotion]*10
	}
	sort.SliceStable(moves, func(i, j int) bool { return key(moves[i]) > key(moves[j]) })
}

// evaluate scores material and simple placement from the side to move.
func evaluate(p Position) int {
	score := 0
	for s := Square(0); s < 64; s++ {
		pc := p.board[s]
		if pc.Type == NoPiece {
			continue
		}
		v := pieceValues[pc.Type] + placement(pc, s)
		if pc.Color == p.turn {
			score += v
		} else {
			score -= v
		}
	}
	return score
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func placement(pc Piece, s Square) int {
	f, r := s.File(), s.Rank()
	if pc.Color == Black {
		r = 7 - r
	}
	center := 7 - max(abs(2*f-7), abs(2*r-7)) // 0 at the edge, 6 in the middle
	switch pc.Type {
	case Pawn:
		if f >= 2 && f <= 5 {
			return r * 8
		}
		return r * 4
	case Knight, Bishop:
		return center * 4
	case Rook, Queen:
		return center
	case King:
		if r == 0 && (f <= 2 || f >= 6) {
			return 20
		}
	}
	return 0
}

// ScoreMoves scores every legal move from the mover's view to o.Depth
// half-moves, without pruning at the root, so scores can be compared. When
// the node limit stops it, only the moves scored so far are returned.
func ScoreMoves(p Position, o SearchOptions) ([]Move, []int) {
	moves := p.LegalMoves()
	orderMoves(p, moves)
	if o.Depth < 1 {
		o.Depth = 1
	}
	s := &searcher{limit: o.NodeLimit, quiesce: o.Quiescence}
	scores := make([]int, 0, len(moves))
	for _, m := range moves {
		score := -s.negamax(p.apply(m), o.Depth-1, -2*MateScore, 2*MateScore, 1)
		if s.aborted {
			return moves[:len(scores)], scores
		}
		scores = append(scores, score)
	}
	return moves, scores
}
