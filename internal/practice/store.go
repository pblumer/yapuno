// Package practice stores one chess game per child against the computer.
//
// Practice involves no other child, so it needs no contact: a call is
// allowed for an authenticated child on an approved device whose guardians
// allow chess. The child may take back moves and take tips without limit.
// The computer's reply is computed on the server with a bounded search, and
// a move is acknowledged only after the child's move and the reply are
// written and synced together. See docs/adr/draft-yapuno-play-chess.md.
package practice

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/pblumer/yapuno/internal/chess"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/games"
	"github.com/pblumer/yapuno/internal/play"
	"github.com/pblumer/yapuno/internal/rating"
)

// Principal is a child on one device, taken from a verified child session.
type Principal = games.Principal

// Level is the computer's strength.
type Level string

const (
	Easy   Level = "easy"
	Medium Level = "medium"
	Hard   Level = "hard"
)

// Takeback undoes the child's last move and the computer's reply.
const Takeback games.ActionKind = "takeback"

// MaxActionsPerMinute bounds the server work one child can cause.
const MaxActionsPerMinute = 30

const maxFile = 8 << 20

func (l Level) options(rand func(int) int) (chess.SearchOptions, bool) {
	switch l {
	case Easy:
		return chess.SearchOptions{Depth: 1, Margin: 200, Rand: rand}, true
	case Medium:
		return chess.SearchOptions{Depth: 2, Quiescence: true, NodeLimit: 30000, Margin: 30, Rand: rand}, true
	case Hard:
		return chess.SearchOptions{Depth: 3, Quiescence: true, NodeLimit: 60000}, true
	}
	return chess.SearchOptions{}, false
}

// Devices confirms current device approval.
type Devices interface {
	games.Devices
}

// Children confirms that a profile exists.
type Children interface {
	games.Children
}

// Enablement says whether the guardians of a child currently allow chess.
type Enablement interface {
	games.Enablement
}

// View is the practice game as the child sees it.
type View struct {
	games.View
	Level       Level
	CanTakeback bool
	Assisted    bool
}

// Rater rates finished games. Record must be idempotent by game key.
type Rater interface {
	Record(gameKey string, a, b rating.Player, scoreA float64) error
}

// SetRater switches on ratings of unassisted practice games.
func (s *Store) SetRater(r Rater) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rater = r
}

// rate passes a finished, unassisted game to the rater once.
func (s *Store) rate(g *game) {
	if s.rater == nil || g.rated {
		return
	}
	st := g.rules.Status()
	if !g.rec.Resigned && st.Kind == chess.Ongoing {
		return
	}
	if g.rec.Assisted {
		g.rated = true
		return
	}
	score := 0.5
	switch {
	case g.rec.Resigned:
		score = 0
	case st.Winner == g.color():
		score = 1
	case st.Winner != chess.NoColor:
		score = 0
	}
	key := "practice:" + g.rec.TenantID + "\x00" + g.rec.ChildID + "\x00" + g.rec.Started.Format(time.RFC3339Nano)
	child := rating.Player{Kind: rating.Child, TenantID: g.rec.TenantID, ID: g.rec.ChildID}
	if s.rater.Record(key, child, rating.Computer(string(g.rec.Level)), score) == nil {
		g.rated = true
	}
}

type storedTip struct {
	Ply    int    `json:"ply"`
	Level  int    `json:"level"`
	Kind   uint8  `json:"kind"`
	Square int8   `json:"square"`
	Piece  uint8  `json:"piece"`
	Best   string `json:"best,omitempty"`
}

type record struct {
	TenantID string      `json:"tenant_id"`
	ChildID  string      `json:"child_id"`
	Level    Level       `json:"level"`
	Color    string      `json:"color"`
	Moves    []string    `json:"moves"`
	Tips     []storedTip `json:"tips"`
	Resigned bool        `json:"resigned,omitempty"`
	// Assisted says a tip or a takeback was used; the game is not rated.
	Assisted bool      `json:"assisted,omitempty"`
	Started  time.Time `json:"started"`
}

type file struct {
	Games []record `json:"games"`
}

type game struct {
	rec   record
	rules *chess.Game
	rated bool
}

func (g *game) color() chess.Color {
	if g.rec.Color == "black" {
		return chess.Black
	}
	return chess.White
}

// replay rebuilds the rules state from the record; an illegal move fails.
func replay(rec record) (*game, error) {
	if rec.TenantID == "" || rec.ChildID == "" || (rec.Color != "white" && rec.Color != "black") || rec.Started.IsZero() {
		return nil, games.ErrInvalid
	}
	if _, ok := rec.Level.options(nil); !ok {
		return nil, games.ErrInvalid
	}
	g := &game{rec: rec, rules: chess.NewGame()}
	positions := []chess.Position{g.rules.Position()}
	for _, s := range rec.Moves {
		m, err := chess.ParseUCI(s)
		if err != nil || g.rules.Play(m) != nil {
			return nil, games.ErrInvalid
		}
		positions = append(positions, g.rules.Position())
	}
	for _, t := range rec.Tips {
		if t.Ply < 0 || t.Ply >= len(positions) || (t.Level != 1 && t.Level != 2) || t.Kind == 0 || t.Kind > uint8(chess.HintActivate) || t.Square < 0 || t.Square > 63 {
			return nil, games.ErrInvalid
		}
		if t.Level == 2 {
			m, err := chess.ParseUCI(t.Best)
			if err != nil || !positions[t.Ply].Legal(m) {
				return nil, games.ErrInvalid
			}
		}
	}
	return g, nil
}

func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".yapuno-practice-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Store is single-writer per file.
type Store struct {
	mu      sync.Mutex
	path    string
	devices Devices
	kids    Children
	enabled Enablement
	now     func() time.Time
	games   map[contacts.Party]*game
	recent  map[contacts.Party][]time.Time

	// rand drives the easy levels and the random colour; write replaces
	// the file. Both are fields so tests can control them.
	rand  func(n int) int
	write func(path string, data []byte) error
	rater Rater
}

// Open loads the file or prepares a new one. It fails closed on insecure
// permissions, unknown fields and any game that does not replay.
func Open(path string, d Devices, ch Children, en Enablement, now func() time.Time) (*Store, error) {
	if path == "" || d == nil || ch == nil || en == nil || now == nil {
		return nil, games.ErrInvalid
	}
	s := &Store{path: path, devices: d, kids: ch, enabled: en, now: now, games: map[contacts.Party]*game{},
		recent: map[contacts.Party][]time.Time{}, rand: rand.IntN, write: writeFile}
	stat, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 || stat.Size() > maxFile {
		return nil, fmt.Errorf("%w: practice file must be a private regular file", games.ErrInvalid)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f file
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%w: decode practice file", games.ErrInvalid)
	}
	for _, rec := range f.Games {
		g, err := replay(rec)
		party := contacts.Party{TenantID: rec.TenantID, ChildID: rec.ChildID}
		if err != nil || s.games[party] != nil {
			return nil, fmt.Errorf("%w: practice game", games.ErrInvalid)
		}
		s.games[party] = g
	}
	return s, nil
}

func (s *Store) authorized(p Principal) bool {
	if p.DeviceID == "" || p.Party.TenantID == "" || p.Party.ChildID == "" {
		return false
	}
	_, device := s.devices.ActiveDevice(p.Party.TenantID, p.Party.ChildID, p.DeviceID)
	_, exists := s.kids.ChildName(p.Party.TenantID, p.Party.ChildID)
	return device && exists && s.enabled.Enabled(p.Party.TenantID, p.Party.ChildID, play.GameChess)
}

// spend records one action of the child and reports whether it is within
// the limit.
func (s *Store) spend(p contacts.Party) bool {
	now := s.now()
	kept := s.recent[p][:0]
	for _, t := range s.recent[p] {
		if t.After(now.Add(-time.Minute)) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= MaxActionsPerMinute {
		s.recent[p] = kept
		return false
	}
	s.recent[p] = append(kept, now)
	return true
}

// commit writes all games with next in place of p's game (nil removes it)
// and only then changes the state in memory.
func (s *Store) commit(p contacts.Party, next *game) error {
	all := map[contacts.Party]*game{}
	for k, v := range s.games {
		all[k] = v
	}
	if next == nil {
		delete(all, p)
	} else {
		all[p] = next
	}
	f := file{Games: make([]record, 0, len(all))}
	for _, g := range all {
		f.Games = append(f.Games, g.rec)
	}
	sort.Slice(f.Games, func(i, j int) bool {
		return f.Games[i].TenantID+"\x00"+f.Games[i].ChildID < f.Games[j].TenantID+"\x00"+f.Games[j].ChildID
	})
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := s.write(s.path, data); err != nil {
		return err
	}
	s.games = all
	return nil
}

func (s *Store) view(g *game) View {
	s.rate(g)
	pos := g.rules.Position()
	v := View{Level: g.rec.Level, Assisted: g.rec.Assisted}
	v.Mode, v.Phase, v.YourColor, v.Position, v.Moves = games.Correspondence, games.Running, g.color(), pos, g.rules.Moves()
	v.Version, v.At, v.Tips, v.TipsLeft = uint64(len(g.rec.Moves)), s.now(), true, -1
	st := g.rules.Status()
	switch {
	case g.rec.Resigned:
		v.Phase, v.End, v.Winner = games.Finished, games.EndResigned, g.color().Other()
	case st.Kind != chess.Ongoing:
		v.Phase, v.Winner = games.Finished, st.Winner
		v.End = map[chess.StatusKind]games.End{chess.Checkmate: games.EndCheckmate, chess.Stalemate: games.EndStalemate,
			chess.InsufficientMaterial: games.EndInsufficient, chess.Repetition: games.EndRepetition, chess.FiftyMoves: games.EndFiftyMoves}[st.Kind]
	}
	if v.Phase == games.Running {
		for _, t := range g.rec.Tips {
			if t.Ply == len(g.rec.Moves) {
				tip := &games.Tip{Level: t.Level, Hint: chess.Hint{Kind: chess.HintKind(t.Kind), Square: chess.Square(t.Square), Piece: chess.PieceType(t.Piece)}}
				if t.Level == 2 {
					tip.Best, _ = chess.ParseUCI(t.Best)
				}
				if v.Tip == nil || tip.Level > v.Tip.Level {
					v.Tip = tip
				}
			}
		}
	}
	v.CanTakeback = !g.rec.Resigned && ownMoves(g) > 0
	return v
}

// ownMoves counts the child's moves.
func ownMoves(g *game) int {
	n := len(g.rec.Moves)
	if g.color() == chess.White {
		return (n + 1) / 2
	}
	return n / 2
}

// Current returns the child's practice game, if there is one.
func (s *Store) Current(p Principal) (View, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.authorized(p) {
		return View{}, false, games.ErrForbidden
	}
	g, ok := s.games[p.Party]
	if !ok {
		return View{}, false, nil
	}
	return s.view(g), true, nil
}

// clone copies a game so a change can be prepared without touching it.
func clone(g *game) *game {
	rec := g.rec
	rec.Moves = append([]string(nil), g.rec.Moves...)
	rec.Tips = append([]storedTip(nil), g.rec.Tips...)
	c, _ := replay(rec)
	return c
}

// reply lets the computer move if it is its turn and the game goes on.
func (s *Store) reply(g *game) {
	if g.rules.Status().Kind != chess.Ongoing || g.rules.Position().Turn() == g.color() {
		return
	}
	opts, _ := g.rec.Level.options(s.rand)
	if m, _, ok := chess.Search(g.rules.Position(), opts); ok && g.rules.Play(m) == nil {
		g.rec.Moves = append(g.rec.Moves, m.String())
	}
}

// Start replaces the child's practice game with a new one. color is
// "white", "black" or "random".
func (s *Store) Start(p Principal, level Level, color string) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.authorized(p) {
		return View{}, games.ErrForbidden
	}
	if _, ok := level.options(nil); !ok {
		return View{}, games.ErrInvalid
	}
	switch color {
	case "white", "black":
	case "random":
		color = []string{"white", "black"}[s.rand(2)]
	default:
		return View{}, games.ErrInvalid
	}
	if !s.spend(p.Party) {
		return View{}, games.ErrRateLimited
	}
	g := &game{rec: record{TenantID: p.Party.TenantID, ChildID: p.Party.ChildID, Level: level, Color: color,
		Moves: []string{}, Tips: []storedTip{}, Started: s.now().UTC()}, rules: chess.NewGame()}
	s.reply(g)
	if err := s.commit(p.Party, g); err != nil {
		return View{}, games.ErrUnavailable
	}
	return s.view(g), nil
}

// Act performs a move (followed by the computer's reply), a tip, a
// takeback or a resignation.
func (s *Store) Act(p Principal, a games.Action) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.authorized(p) {
		return View{}, games.ErrForbidden
	}
	g, ok := s.games[p.Party]
	if !ok {
		return View{}, games.ErrNotNow
	}
	switch a.Kind {
	case games.Move, games.Hint, Takeback, games.Resign:
	default:
		return View{}, games.ErrInvalid
	}
	if a.Kind != games.Move && a.Move != "" || a.Kind != games.Hint && a.Level != 0 ||
		a.Kind == games.Hint && a.Level != 1 && a.Level != 2 || a.Kind == games.Resign && a.Ply != 0 {
		return View{}, games.ErrInvalid
	}
	if !s.spend(p.Party) {
		return View{}, games.ErrRateLimited
	}
	n := len(g.rec.Moves)
	over := g.rec.Resigned || g.rules.Status().Kind != chess.Ongoing
	next := clone(g)
	switch a.Kind {
	case games.Resign:
		if g.rec.Resigned {
			return s.view(g), nil
		}
		if over {
			return View{}, games.ErrNotNow
		}
		next.rec.Resigned = true
	case games.Move:
		m, err := chess.ParseUCI(a.Move)
		if err != nil {
			return View{}, games.ErrInvalid
		}
		// A retry of a move that was already played changes nothing.
		if a.Ply < n && a.Ply >= n-2 && g.rec.Moves[a.Ply] == a.Move && a.Ply%2 == colorParity(g) {
			return s.view(g), nil
		}
		if over {
			return View{}, games.ErrNotNow
		}
		if a.Ply != n {
			return View{}, games.ErrStale
		}
		if next.rules.Play(m) != nil {
			return View{}, games.ErrIllegal
		}
		next.rec.Moves = append(next.rec.Moves, a.Move)
		s.reply(next)
	case games.Hint:
		if over || g.rules.Position().Turn() != g.color() {
			return View{}, games.ErrNotNow
		}
		if a.Ply != n {
			return View{}, games.ErrStale
		}
		taken := 0
		for _, t := range g.rec.Tips {
			if t.Ply == n && t.Level > taken {
				taken = t.Level
			}
		}
		if taken >= a.Level {
			return s.view(g), nil
		}
		if a.Level == 2 && taken != 1 {
			return View{}, games.ErrNotNow
		}
		pos := g.rules.Position()
		h := chess.Analyze(pos)
		t := storedTip{Ply: n, Level: a.Level, Kind: uint8(h.Kind), Square: int8(h.Square), Piece: uint8(h.Piece)}
		if a.Level == 2 {
			best, _, ok := chess.Search(pos, chess.SearchOptions{Depth: 3, Quiescence: true, NodeLimit: 60000})
			if !ok {
				return View{}, games.ErrNotNow
			}
			t.Best = best.String()
		}
		next.rec.Tips = append(next.rec.Tips, t)
		next.rec.Assisted = true
	case Takeback:
		if g.rec.Resigned || ownMoves(g) == 0 {
			return View{}, games.ErrNotNow
		}
		if a.Ply != n {
			return View{}, games.ErrStale
		}
		// Back to the position before the child's last move.
		keep := n - 1
		if keep%2 != colorParity(g) {
			keep--
		}
		rec := next.rec
		rec.Assisted = true
		rec.Moves = rec.Moves[:keep]
		var tips []storedTip
		for _, t := range rec.Tips {
			if t.Ply <= keep {
				tips = append(tips, t)
			}
		}
		rec.Tips = append([]storedTip{}, tips...)
		var err error
		if next, err = replay(rec); err != nil {
			return View{}, games.ErrUnavailable
		}
	}
	if err := s.commit(p.Party, next); err != nil {
		return View{}, games.ErrUnavailable
	}
	return s.view(next), nil
}

// colorParity is the parity of the half-moves the child makes.
func colorParity(g *game) int {
	if g.color() == chess.White {
		return 0
	}
	return 1
}

// PurgeChild removes the practice game of one child. It is a trusted call
// for the deletion of a child profile.
func (s *Store) PurgeChild(tenantID, childID string) error {
	if tenantID == "" || childID == "" {
		return games.ErrInvalid
	}
	return s.purge(func(p contacts.Party) bool { return p.TenantID == tenantID && p.ChildID == childID })
}

// PurgeTenant removes the practice games of every child of one family.
func (s *Store) PurgeTenant(tenantID string) error {
	if tenantID == "" {
		return games.ErrInvalid
	}
	return s.purge(func(p contacts.Party) bool { return p.TenantID == tenantID })
}

func (s *Store) purge(match func(contacts.Party) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for p := range s.games {
		if match(p) {
			if err := s.commit(p, nil); err != nil {
				return err
			}
		}
	}
	return nil
}
