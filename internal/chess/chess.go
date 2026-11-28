// Package chess implements the rules of chess for server-authoritative play:
// legal move generation, the position after a move, and the end of a game by
// checkmate, stalemate, insufficient material, threefold repetition or the
// fifty-move rule.
//
// For children the draw rules apply automatically: a third repetition or the
// hundredth half-move without capture or pawn move ends the game at once
// instead of waiting for a claim. See docs/adr/draft-yapuno-play-chess.md.
//
// The package has no notion of players, clocks, resignations or storage.
package chess

import (
	"errors"
	"sort"
	"strconv"
	"strings"
)

var (
	ErrInvalidFEN  = errors.New("invalid FEN")
	ErrInvalidMove = errors.New("invalid move notation")
	ErrIllegalMove = errors.New("illegal move")
	ErrGameOver    = errors.New("game is over")
)

// StartFEN is the initial position.
const StartFEN = "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"

// Color is a side. NoColor marks an empty square or a drawn game.
type Color uint8

const (
	NoColor Color = iota
	White
	Black
)

// Other returns the opponent.
func (c Color) Other() Color {
	switch c {
	case White:
		return Black
	case Black:
		return White
	}
	return NoColor
}

// PieceType is the kind of a piece.
type PieceType uint8

const (
	NoPiece PieceType = iota
	Pawn
	Knight
	Bishop
	Rook
	Queen
	King
)

// Piece is the content of one square; the zero value is an empty square.
type Piece struct {
	Type  PieceType
	Color Color
}

// Square is 0 (a1) to 63 (h8), file-major within each rank.
type Square int8

// File is 0 for the a-file up to 7 for the h-file.
func (s Square) File() int { return int(s) & 7 }

// Rank is 0 for the first rank up to 7 for the eighth.
func (s Square) Rank() int { return int(s) >> 3 }

func (s Square) String() string {
	if s < 0 || s > 63 {
		return "-"
	}
	return string([]byte{byte('a' + s.File()), byte('1' + s.Rank())})
}

// ParseSquare reads a square name such as "e4".
func ParseSquare(name string) (Square, bool) {
	if len(name) != 2 || name[0] < 'a' || name[0] > 'h' || name[1] < '1' || name[1] > '8' {
		return 0, false
	}
	return Square(int(name[1]-'1')*8 + int(name[0]-'a')), true
}

func at(file, rank int) (Square, bool) {
	if file < 0 || file > 7 || rank < 0 || rank > 7 {
		return 0, false
	}
	return Square(rank*8 + file), true
}

// Move is a move in coordinate form. Promotion is set only when a pawn
// reaches the last rank.
type Move struct {
	From, To  Square
	Promotion PieceType
}

var promotionLetters = map[byte]PieceType{'q': Queen, 'r': Rook, 'b': Bishop, 'n': Knight}

// ParseUCI reads a move in UCI notation, for example "e2e4" or "e7e8q".
// It checks the notation only; legality is decided by the position.
func ParseUCI(s string) (Move, error) {
	if len(s) != 4 && len(s) != 5 {
		return Move{}, ErrInvalidMove
	}
	from, ok1 := ParseSquare(s[0:2])
	to, ok2 := ParseSquare(s[2:4])
	if !ok1 || !ok2 || from == to {
		return Move{}, ErrInvalidMove
	}
	m := Move{From: from, To: to}
	if len(s) == 5 {
		p, ok := promotionLetters[s[4]]
		if !ok {
			return Move{}, ErrInvalidMove
		}
		m.Promotion = p
	}
	return m, nil
}

// String returns the UCI notation.
func (m Move) String() string {
	s := m.From.String() + m.To.String()
	switch m.Promotion {
	case Queen:
		s += "q"
	case Rook:
		s += "r"
	case Bishop:
		s += "b"
	case Knight:
		s += "n"
	}
	return s
}

const (
	castleWK uint8 = 1 << iota
	castleWQ
	castleBK
	castleBQ
)

const noSquare Square = -1

// Position is an immutable value: methods never change the receiver.
type Position struct {
	board    [64]Piece
	turn     Color
	castling uint8
	ep       Square // target square after a double step, as in FEN
	halfmove int
	fullmove int
	last     Move
}

// Turn is the side to move.
func (p Position) Turn() Color { return p.turn }

// PieceAt returns the piece on s, or the zero Piece.
func (p Position) PieceAt(s Square) Piece {
	if s < 0 || s > 63 {
		return Piece{}
	}
	return p.board[s]
}

// LastMove is the move that led to this position; the zero Move if none.
func (p Position) LastMove() Move { return p.last }

// HalfmoveClock counts half-moves since the last capture or pawn move.
func (p Position) HalfmoveClock() int { return p.halfmove }

var pieceLetters = map[byte]Piece{
	'P': {Pawn, White}, 'N': {Knight, White}, 'B': {Bishop, White}, 'R': {Rook, White}, 'Q': {Queen, White}, 'K': {King, White},
	'p': {Pawn, Black}, 'n': {Knight, Black}, 'b': {Bishop, Black}, 'r': {Rook, Black}, 'q': {Queen, Black}, 'k': {King, Black},
}

func letter(pc Piece) byte {
	for l, p := range pieceLetters {
		if p == pc {
			return l
		}
	}
	return '?'
}

// count reads a non-negative decimal without sign or leading zeros.
func count(s string) (int, bool) {
	if s == "" || len(s) > 5 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// ParseFEN reads a position in Forsyth-Edwards Notation. It is strict: the
// text must be canonical, castling rights must match the pieces, an en
// passant square must follow a double step, and the side that is not to
// move must not be in check.
func ParseFEN(fen string) (Position, error) {
	fields := strings.Split(fen, " ")
	if len(fields) != 6 {
		return Position{}, ErrInvalidFEN
	}
	var p Position
	ranks := strings.Split(fields[0], "/")
	if len(ranks) != 8 {
		return Position{}, ErrInvalidFEN
	}
	kings := map[Color]int{}
	for i, row := range ranks {
		rank, file := 7-i, 0
		digit := false
		for j := 0; j < len(row); j++ {
			c := row[j]
			if c >= '1' && c <= '8' {
				if digit {
					return Position{}, ErrInvalidFEN
				}
				digit = true
				file += int(c - '0')
				continue
			}
			digit = false
			pc, ok := pieceLetters[c]
			if !ok || file > 7 {
				return Position{}, ErrInvalidFEN
			}
			if pc.Type == Pawn && (rank == 0 || rank == 7) {
				return Position{}, ErrInvalidFEN
			}
			if pc.Type == King {
				kings[pc.Color]++
			}
			p.board[rank*8+file] = pc
			file++
		}
		if file != 8 {
			return Position{}, ErrInvalidFEN
		}
	}
	if kings[White] != 1 || kings[Black] != 1 {
		return Position{}, ErrInvalidFEN
	}
	switch fields[1] {
	case "w":
		p.turn = White
	case "b":
		p.turn = Black
	default:
		return Position{}, ErrInvalidFEN
	}
	if fields[2] != "-" {
		order := "KQkq"
		pos := 0
		for i := 0; i < len(fields[2]); i++ {
			k := strings.IndexByte(order[pos:], fields[2][i])
			if k < 0 {
				return Position{}, ErrInvalidFEN
			}
			pos += k + 1
			p.castling |= 1 << uint(pos-1)
		}
		if fields[2] == "" {
			return Position{}, ErrInvalidFEN
		}
	}
	if !p.castlingConsistent() {
		return Position{}, ErrInvalidFEN
	}
	p.ep = noSquare
	if fields[3] != "-" {
		s, ok := ParseSquare(fields[3])
		if !ok || !p.epConsistent(s) {
			return Position{}, ErrInvalidFEN
		}
		p.ep = s
	}
	var ok bool
	if p.halfmove, ok = count(fields[4]); !ok {
		return Position{}, ErrInvalidFEN
	}
	if p.fullmove, ok = count(fields[5]); !ok || p.fullmove < 1 {
		return Position{}, ErrInvalidFEN
	}
	if p.attacked(p.king(p.turn.Other()), p.turn) {
		return Position{}, ErrInvalidFEN
	}
	return p, nil
}

func (p Position) castlingConsistent() bool {
	need := []struct {
		right uint8
		king  Square
		rook  Square
		color Color
	}{{castleWK, 4, 7, White}, {castleWQ, 4, 0, White}, {castleBK, 60, 63, Black}, {castleBQ, 60, 56, Black}}
	for _, n := range need {
		if p.castling&n.right != 0 && (p.board[n.king] != Piece{King, n.color} || p.board[n.rook] != Piece{Rook, n.color}) {
			return false
		}
	}
	return true
}

// epConsistent checks that s is the square a pawn of the side not to move
// just passed with a double step.
func (p Position) epConsistent(s Square) bool {
	mover := p.turn.Other()
	rank, dir := 2, 1
	if mover == Black {
		rank, dir = 5, -1
	}
	if s.Rank() != rank || p.board[s] != (Piece{}) {
		return false
	}
	from, _ := at(s.File(), rank-dir)
	to, _ := at(s.File(), rank+dir)
	return p.board[from] == (Piece{}) && p.board[to] == Piece{Pawn, mover}
}

// FEN returns the position in Forsyth-Edwards Notation.
func (p Position) FEN() string {
	var b strings.Builder
	b.WriteString(p.placement())
	b.WriteByte(' ')
	b.WriteString(p.rights(p.ep))
	b.WriteByte(' ')
	b.WriteString(strconv.Itoa(p.halfmove))
	b.WriteByte(' ')
	b.WriteString(strconv.Itoa(p.fullmove))
	return b.String()
}

func (p Position) placement() string {
	var b strings.Builder
	for rank := 7; rank >= 0; rank-- {
		empty := 0
		for file := 0; file < 8; file++ {
			pc := p.board[rank*8+file]
			if pc.Type == NoPiece {
				empty++
				continue
			}
			if empty > 0 {
				b.WriteByte(byte('0' + empty))
				empty = 0
			}
			b.WriteByte(letter(pc))
		}
		if empty > 0 {
			b.WriteByte(byte('0' + empty))
		}
		if rank > 0 {
			b.WriteByte('/')
		}
	}
	return b.String()
}

// rights writes turn, castling rights and the given en passant square.
func (p Position) rights(ep Square) string {
	var b strings.Builder
	if p.turn == White {
		b.WriteString("w ")
	} else {
		b.WriteString("b ")
	}
	if p.castling == 0 {
		b.WriteByte('-')
	}
	for i, c := range "KQkq" {
		if p.castling&(1<<uint(i)) != 0 {
			b.WriteRune(c)
		}
	}
	b.WriteByte(' ')
	b.WriteString(ep.String())
	return b.String()
}

// RepetitionKey identifies the position for the repetition rule: placement,
// side to move, castling rights and an en passant square only when an en
// passant capture is actually legal.
func (p Position) RepetitionKey() string {
	ep := noSquare
	if p.ep != noSquare {
		for _, m := range p.LegalMoves() {
			if m.To == p.ep && p.board[m.From].Type == Pawn {
				ep = p.ep
				break
			}
		}
	}
	return p.placement() + " " + p.rights(ep)
}

func (p Position) king(c Color) Square {
	for s := Square(0); s < 64; s++ {
		if p.board[s] == (Piece{King, c}) {
			return s
		}
	}
	return noSquare
}

var (
	knightSteps = [8][2]int{{1, 2}, {2, 1}, {2, -1}, {1, -2}, {-1, -2}, {-2, -1}, {-2, 1}, {-1, 2}}
	kingSteps   = [8][2]int{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}}
	diagonals   = [4][2]int{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}}
	orthogonals = [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
)

// attacked reports whether a piece of colour by attacks s.
func (p Position) attacked(s Square, by Color) bool {
	if s == noSquare {
		return false
	}
	f, r := s.File(), s.Rank()
	pawnRank := r - 1
	if by == Black {
		pawnRank = r + 1
	}
	for _, df := range []int{-1, 1} {
		if t, ok := at(f+df, pawnRank); ok && p.board[t] == (Piece{Pawn, by}) {
			return true
		}
	}
	for _, d := range knightSteps {
		if t, ok := at(f+d[0], r+d[1]); ok && p.board[t] == (Piece{Knight, by}) {
			return true
		}
	}
	for _, d := range kingSteps {
		if t, ok := at(f+d[0], r+d[1]); ok && p.board[t] == (Piece{King, by}) {
			return true
		}
	}
	slide := func(dirs [4][2]int, a, b PieceType) bool {
		for _, d := range dirs {
			for i := 1; ; i++ {
				t, ok := at(f+d[0]*i, r+d[1]*i)
				if !ok {
					break
				}
				pc := p.board[t]
				if pc.Type == NoPiece {
					continue
				}
				if pc.Color == by && (pc.Type == a || pc.Type == b) {
					return true
				}
				break
			}
		}
		return false
	}
	return slide(diagonals, Bishop, Queen) || slide(orthogonals, Rook, Queen)
}

// InCheck reports whether the side to move is in check.
func (p Position) InCheck() bool { return p.attacked(p.king(p.turn), p.turn.Other()) }

func (p Position) pseudoMoves() []Move {
	us, them := p.turn, p.turn.Other()
	moves := make([]Move, 0, 48)
	add := func(from, to Square) { moves = append(moves, Move{From: from, To: to}) }
	for s := Square(0); s < 64; s++ {
		pc := p.board[s]
		if pc.Color != us {
			continue
		}
		f, r := s.File(), s.Rank()
		switch pc.Type {
		case Pawn:
			dir, start, last := 1, 1, 7
			if us == Black {
				dir, start, last = -1, 6, 0
			}
			push := func(to Square) {
				if to.Rank() == last {
					for _, promo := range []PieceType{Queen, Rook, Bishop, Knight} {
						moves = append(moves, Move{From: s, To: to, Promotion: promo})
					}
					return
				}
				add(s, to)
			}
			if one, ok := at(f, r+dir); ok && p.board[one].Type == NoPiece {
				push(one)
				if two, ok := at(f, r+2*dir); r == start && ok && p.board[two].Type == NoPiece {
					add(s, two)
				}
			}
			for _, df := range []int{-1, 1} {
				t, ok := at(f+df, r+dir)
				if ok && (p.board[t].Color == them || t == p.ep) {
					push(t)
				}
			}
		case Knight, King:
			steps := knightSteps
			if pc.Type == King {
				steps = kingSteps
			}
			for _, d := range steps {
				if t, ok := at(f+d[0], r+d[1]); ok && p.board[t].Color != us {
					add(s, t)
				}
			}
		case Bishop, Rook, Queen:
			var dirs [][2]int
			if pc.Type != Rook {
				dirs = append(dirs, diagonals[:]...)
			}
			if pc.Type != Bishop {
				dirs = append(dirs, orthogonals[:]...)
			}
			for _, d := range dirs {
				for i := 1; ; i++ {
					t, ok := at(f+d[0]*i, r+d[1]*i)
					if !ok || p.board[t].Color == us {
						break
					}
					add(s, t)
					if p.board[t].Color == them {
						break
					}
				}
			}
		}
	}
	// Castling: the king may not be in check, pass through or land on an
	// attacked square (the landing square is checked with every move).
	home, kRight, qRight := Square(4), castleWK, castleWQ
	if us == Black {
		home, kRight, qRight = 60, castleBK, castleBQ
	}
	empty := func(squares ...Square) bool {
		for _, s := range squares {
			if p.board[s].Type != NoPiece {
				return false
			}
		}
		return true
	}
	if p.castling&(kRight|qRight) != 0 && !p.attacked(home, them) {
		if p.castling&kRight != 0 && empty(home+1, home+2) && !p.attacked(home+1, them) {
			add(home, home+2)
		}
		if p.castling&qRight != 0 && empty(home-1, home-2, home-3) && !p.attacked(home-1, them) {
			add(home, home-2)
		}
	}
	return moves
}

// apply plays a pseudo-legal move without checking legality.
func (p Position) apply(m Move) Position {
	n := p
	pc := n.board[m.From]
	captured := n.board[m.To]
	n.board[m.From] = Piece{}
	n.board[m.To] = pc
	n.ep = noSquare
	n.halfmove++
	if pc.Type == Pawn {
		n.halfmove = 0
		if m.To == p.ep {
			// En passant: the captured pawn stands behind the target square.
			behind, _ := at(m.To.File(), m.From.Rank())
			n.board[behind] = Piece{}
		}
		if d := m.To.Rank() - m.From.Rank(); d == 2 || d == -2 {
			n.ep, _ = at(m.From.File(), (m.From.Rank()+m.To.Rank())/2)
		}
		if m.Promotion != NoPiece {
			n.board[m.To] = Piece{m.Promotion, pc.Color}
		}
	}
	if captured.Type != NoPiece {
		n.halfmove = 0
	}
	if pc.Type == King {
		if m.To-m.From == 2 {
			n.board[m.From+1], n.board[m.From+3] = n.board[m.From+3], Piece{}
		} else if m.From-m.To == 2 {
			n.board[m.From-1], n.board[m.From-4] = n.board[m.From-4], Piece{}
		}
		if pc.Color == White {
			n.castling &^= castleWK | castleWQ
		} else {
			n.castling &^= castleBK | castleBQ
		}
	}
	for _, s := range []Square{m.From, m.To} {
		switch s {
		case 0:
			n.castling &^= castleWQ
		case 7:
			n.castling &^= castleWK
		case 56:
			n.castling &^= castleBQ
		case 63:
			n.castling &^= castleBK
		}
	}
	if p.turn == Black {
		n.fullmove++
	}
	n.turn = p.turn.Other()
	n.last = m
	return n
}

// LegalMoves lists every legal move of the side to move.
func (p Position) LegalMoves() []Move {
	pseudo := p.pseudoMoves()
	legal := pseudo[:0]
	for _, m := range pseudo {
		n := p.apply(m)
		if !n.attacked(n.king(p.turn), n.turn) {
			legal = append(legal, m)
		}
	}
	return legal
}

// TargetsFrom lists the squares the piece on from may legally move to.
func (p Position) TargetsFrom(from Square) []Square {
	seen := map[Square]bool{}
	var out []Square
	for _, m := range p.LegalMoves() {
		if m.From == from && !seen[m.To] {
			seen[m.To] = true
			out = append(out, m.To)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Legal reports whether m is a legal move here.
func (p Position) Legal(m Move) bool {
	for _, l := range p.LegalMoves() {
		if l == m {
			return true
		}
	}
	return false
}

// Play returns the position after a legal move.
func (p Position) Play(m Move) (Position, error) {
	if !p.Legal(m) {
		return p, ErrIllegalMove
	}
	return p.apply(m), nil
}

// InsufficientMaterial reports the standard dead positions: kings only, a
// single knight or bishop, or bishops only, all on squares of one colour.
func (p Position) InsufficientMaterial() bool {
	minors, bishops := 0, 0
	colours := map[int]bool{}
	for s := Square(0); s < 64; s++ {
		switch p.board[s].Type {
		case NoPiece, King:
		case Knight:
			minors++
		case Bishop:
			minors++
			bishops++
			colours[(s.File()+s.Rank())%2] = true
		default:
			return false
		}
	}
	if minors <= 1 {
		return true
	}
	return bishops == minors && len(colours) == 1
}

// CannotMate reports whether c certainly cannot give mate: c has a lone king,
// or the position is insufficient material for both. It decides whether a
// time-out by c's opponent is a draw instead of a loss.
func (p Position) CannotMate(c Color) bool {
	for s := Square(0); s < 64; s++ {
		if pc := p.board[s]; pc.Color == c && pc.Type != King {
			return p.InsufficientMaterial()
		}
	}
	return true
}

// Perft counts the leaf nodes of the legal move tree to the given depth.
func Perft(p Position, depth int) uint64 {
	if depth <= 0 {
		return 1
	}
	moves := p.LegalMoves()
	if depth == 1 {
		return uint64(len(moves))
	}
	var n uint64
	for _, m := range moves {
		n += Perft(p.apply(m), depth-1)
	}
	return n
}

// StatusKind says whether and how a game ended by the rules.
type StatusKind uint8

const (
	Ongoing StatusKind = iota
	Checkmate
	Stalemate
	InsufficientMaterial
	Repetition
	FiftyMoves
)

// Status is the rules outcome. Winner is set for checkmate only.
type Status struct {
	Kind   StatusKind
	Winner Color
}

// Game is a sequence of legal moves from a start position. It is not safe
// for concurrent use.
type Game struct {
	pos    Position
	moves  []Move
	seen   map[string]int
	status Status
}

// NewGame starts from the initial position.
func NewGame() *Game {
	g, err := NewGameFromFEN(StartFEN)
	if err != nil {
		panic(err)
	}
	return g
}

// NewGameFromFEN starts from a given position.
func NewGameFromFEN(fen string) (*Game, error) {
	p, err := ParseFEN(fen)
	if err != nil {
		return nil, err
	}
	g := &Game{pos: p, seen: map[string]int{}}
	g.seen[p.RepetitionKey()]++
	g.status = g.evaluate()
	return g, nil
}

func (g *Game) evaluate() Status {
	p := g.pos
	if len(p.LegalMoves()) == 0 {
		if p.InCheck() {
			return Status{Kind: Checkmate, Winner: p.turn.Other()}
		}
		return Status{Kind: Stalemate}
	}
	switch {
	case p.InsufficientMaterial():
		return Status{Kind: InsufficientMaterial}
	case g.seen[p.RepetitionKey()] >= 3:
		return Status{Kind: Repetition}
	case p.halfmove >= 100:
		return Status{Kind: FiftyMoves}
	}
	return Status{}
}

// Check reports whether Play(m) would succeed, without changing the game.
func (g *Game) Check(m Move) error {
	if g.status.Kind != Ongoing {
		return ErrGameOver
	}
	if !g.pos.Legal(m) {
		return ErrIllegalMove
	}
	return nil
}

// Play makes a legal move and updates the status.
func (g *Game) Play(m Move) error {
	if err := g.Check(m); err != nil {
		return err
	}
	g.pos = g.pos.apply(m)
	g.moves = append(g.moves, m)
	g.seen[g.pos.RepetitionKey()]++
	g.status = g.evaluate()
	return nil
}

// Position is the current position.
func (g *Game) Position() Position { return g.pos }

// Moves returns a copy of the moves played.
func (g *Game) Moves() []Move { return append([]Move(nil), g.moves...) }

// Status is the rules outcome after the last move.
func (g *Game) Status() Status { return g.status }
