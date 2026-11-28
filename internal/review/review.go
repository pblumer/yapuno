// Package review looks back at a finished game from one side: where a mate
// was missed, where a move allowed the opponent to mate, and where a move
// lost material. It scores every legal move two half-moves deep with
// captures, which is a simple look for children, not a trainer's analysis.
package review

import (
	"sort"
	"strings"
	"sync"

	"github.com/pblumer/yapuno/internal/chess"
)

// Kind of finding.
type Kind uint8

const (
	MissedMate Kind = iota + 1
	AllowedMate
	LostMaterial
)

const (
	// MaxFindings keeps the review short.
	MaxFindings = 3
	// minLoss is the smallest loss, in centipawns, worth mentioning.
	minLoss = 150
	// nodes bounds the search per position.
	nodes   = 20000
	mateish = chess.MateScore - 100
)

// Finding is one moment of the game worth looking at again.
type Finding struct {
	// Ply is the index of the move in the game, from 0.
	Ply    int
	Kind   Kind
	Played chess.Move
	Better chess.Move
	// Before is the position in which Played was made.
	Before chess.Position
	// Loss is the estimated material lost, in centipawns (LostMaterial).
	Loss int
}

// Review returns at most MaxFindings findings for side, in game order. An
// illegal move list yields what was found up to that move.
func Review(moves []chess.Move, side chess.Color) []Finding {
	found := findings(moves, side)
	weight := func(f Finding) int {
		if f.Kind != LostMaterial {
			return chess.MateScore
		}
		return f.Loss
	}
	sort.SliceStable(found, func(i, j int) bool { return weight(found[i]) > weight(found[j]) })
	if len(found) > MaxFindings {
		found = found[:MaxFindings]
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Ply < found[j].Ply })
	return found
}

// findings returns every finding for side, in game order.
func findings(moves []chess.Move, side chess.Color) []Finding {
	g := chess.NewGame()
	found := []Finding{}
	for ply, m := range moves {
		pos := g.Position()
		if pos.Turn() == side && len(pos.LegalMoves()) > 1 {
			if f, ok := judge(pos, m, ply); ok {
				found = append(found, f)
			}
		}
		if g.Play(m) != nil {
			break
		}
	}
	return found
}

func judge(pos chess.Position, played chess.Move, ply int) (Finding, bool) {
	moves, scores := chess.ScoreMoves(pos, chess.SearchOptions{Depth: 2, Quiescence: true, NodeLimit: nodes})
	best, bestScore, playedScore, seen := chess.Move{}, -2*chess.MateScore, 0, false
	for i, m := range moves {
		if scores[i] > bestScore {
			best, bestScore = m, scores[i]
		}
		if m == played {
			playedScore, seen = scores[i], true
		}
	}
	if !seen || best == played {
		return Finding{}, false
	}
	f := Finding{Ply: ply, Played: played, Better: best, Before: pos}
	switch {
	case bestScore >= mateish && playedScore < mateish:
		f.Kind = MissedMate
	case playedScore <= -mateish && bestScore > -mateish:
		f.Kind = AllowedMate
	case bestScore < mateish && playedScore > -mateish && bestScore-playedScore >= minLoss:
		f.Kind, f.Loss = LostMaterial, bestScore-playedScore
	default:
		return Finding{}, false
	}
	return f, true
}

// cache keeps reviews of finished games: a game's moves never change once it
// has ended, so the same request costs no new search.
var cache = struct {
	sync.Mutex
	m map[string][]Finding
}{m: map[string][]Finding{}}

const maxCached = 512

// Cached is Review with a bounded cache keyed by the moves and the side.
// It always returns a fresh, non-nil slice.
func Cached(moves []chess.Move, side chess.Color) []Finding {
	var b strings.Builder
	b.WriteByte(byte('0' + side))
	for _, m := range moves {
		b.WriteByte(' ')
		b.WriteString(m.String())
	}
	key := b.String()
	cache.Lock()
	found, ok := cache.m[key]
	cache.Unlock()
	if !ok {
		found = Review(moves, side)
		cache.Lock()
		if len(cache.m) >= maxCached {
			cache.m = map[string][]Finding{}
		}
		cache.m[key] = found
		cache.Unlock()
	}
	return append([]Finding{}, found...)
}
