// Package rating keeps a private chess rating per child: Elo with the
// junior factor K = 40, starting at 1000. A child's rating is shown only to
// that child. Guardians and the computer are fixed anchors: a guardian
// always counts as 1000 and nothing is stored about guardians; the computer
// plays at fixed, uncalibrated ratings. See docs/adr/draft-yapuno-play-chess.md.
//
// Ratings live apart from games because games are deleted after their
// retention period. Each game is rated exactly once, by its key.
package rating

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var ErrInvalid = errors.New("invalid rating request")

const (
	// Start is the rating of a player without rated games.
	Start = 1000
	// K is the FIDE development factor for juniors.
	K = 40
	// Provisional is how many rated games a rating is marked provisional for.
	Provisional = 10
	// keep is how long the keys of rated games are kept: longer than any
	// game is stored, so a game cannot be rated twice.
	keep    = 120 * 24 * time.Hour
	maxFile = 8 << 20
)

// Kind of player.
type Kind string

const (
	Child        Kind = "child"
	Guardian     Kind = "guardian"
	ComputerKind Kind = "computer"
)

// ComputerRatings are assumptions, not measurements: they make results
// against the computer count, but they are not calibrated against any
// official scale.
var ComputerRatings = map[string]float64{"easy": 600, "medium": 900, "hard": 1200}

// GuardianRating is what every guardian counts as. A fixed value means no
// rating of an adult is kept.
const GuardianRating = 1000

// Player is one rated party.
type Player struct {
	Kind     Kind
	TenantID string
	ID       string
}

// Computer is the computer at one level.
func Computer(level string) Player { return Player{Kind: ComputerKind, ID: level} }

func (p Player) valid() bool {
	switch p.Kind {
	case Child, Guardian:
		return p.TenantID != "" && p.ID != ""
	case ComputerKind:
		_, ok := ComputerRatings[p.ID]
		return ok && p.TenantID == ""
	}
	return false
}

// Rating is what a player sees of their own rating.
type Rating struct {
	Value       int
	Games       int
	Provisional bool
}

type row struct {
	Kind     Kind    `json:"kind"`
	TenantID string  `json:"tenant_id"`
	ID       string  `json:"id"`
	Rating   float64 `json:"rating"`
	Games    int     `json:"games"`
}

type recorded struct {
	Key string    `json:"key"` // SHA-256 of the game key
	At  time.Time `json:"at"`
}

type file struct {
	Players  []row      `json:"players"`
	Recorded []recorded `json:"recorded"`
}

type state struct {
	rating float64
	games  int
}

// Store is single-writer per file.
type Store struct {
	mu      sync.Mutex
	path    string
	now     func() time.Time
	players map[Player]state
	keys    map[string]time.Time
}

// Open loads the file or prepares a new one. It fails closed on insecure
// permissions, unknown fields and invalid rows.
func Open(path string, now func() time.Time) (*Store, error) {
	if path == "" || now == nil {
		return nil, ErrInvalid
	}
	s := &Store{path: path, now: now, players: map[Player]state{}, keys: map[string]time.Time{}}
	stat, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 || stat.Size() > maxFile {
		return nil, fmt.Errorf("%w: ratings must be a private regular file", ErrInvalid)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f file
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%w: decode ratings", ErrInvalid)
	}
	for _, r := range f.Players {
		p := Player{Kind: r.Kind, TenantID: r.TenantID, ID: r.ID}
		if p.Kind == Guardian && p.valid() {
			// Written before guardians became an anchor: dropped, and gone
			// from the file at the next write.
			continue
		}
		if !p.valid() || p.Kind != Child || r.Games < 1 || math.IsNaN(r.Rating) || math.IsInf(r.Rating, 0) {
			return nil, fmt.Errorf("%w: rating row", ErrInvalid)
		}
		if _, dup := s.players[p]; dup {
			return nil, fmt.Errorf("%w: duplicate rating row", ErrInvalid)
		}
		s.players[p] = state{rating: r.Rating, games: r.Games}
	}
	for _, r := range f.Recorded {
		if len(r.Key) != 64 || r.At.IsZero() {
			return nil, fmt.Errorf("%w: recorded game", ErrInvalid)
		}
		s.keys[r.Key] = r.At
	}
	return s, nil
}

// anchor returns the fixed rating of a guardian or the computer.
func anchor(p Player) (float64, bool) {
	switch p.Kind {
	case Guardian:
		return GuardianRating, true
	case ComputerKind:
		return ComputerRatings[p.ID], true
	}
	return 0, false
}

func (s *Store) current(p Player) state {
	if r, ok := anchor(p); ok {
		return state{rating: r}
	}
	if st, ok := s.players[p]; ok {
		return st
	}
	return state{rating: Start}
}

func expected(a, b float64) float64 { return 1 / (1 + math.Pow(10, (b-a)/400)) }

// Of returns a player's rating.
func (s *Store) Of(p Player) Rating {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.current(p)
	return Rating{Value: int(math.Round(st.rating)), Games: st.games, Provisional: p.Kind == Child && st.games < Provisional}
}

func digest(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Record rates one finished game once. scoreA is 1 if a won, 0.5 for a
// draw and 0 if b won. Only children's ratings change.
func (s *Store) Record(gameKey string, a, b Player, scoreA float64) error {
	if gameKey == "" || !a.valid() || !b.valid() || a == b || (scoreA != 0 && scoreA != 0.5 && scoreA != 1) ||
		(a.Kind != Child && b.Kind != Child) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := digest(gameKey)
	if _, done := s.keys[key]; done {
		return nil
	}
	ra, rb := s.current(a), s.current(b)
	ea := expected(ra.rating, rb.rating)
	next := map[Player]state{}
	for p, st := range s.players {
		next[p] = st
	}
	if a.Kind == Child {
		next[a] = state{rating: ra.rating + K*(scoreA-ea), games: ra.games + 1}
	}
	if b.Kind == Child {
		next[b] = state{rating: rb.rating + K*((1-scoreA)-(1-ea)), games: rb.games + 1}
	}
	keys := map[string]time.Time{key: s.now().UTC()}
	for k, at := range s.keys {
		if s.now().Sub(at) < keep {
			keys[k] = at
		}
	}
	return s.commit(next, keys)
}

// PurgeChild removes one child's rating. It is a trusted call for the
// deletion of a child profile.
func (s *Store) PurgeChild(tenantID, childID string) error {
	if tenantID == "" || childID == "" {
		return ErrInvalid
	}
	return s.purge(func(p Player) bool { return p.Kind == Child && p.TenantID == tenantID && p.ID == childID })
}

// PurgeTenant removes the ratings of every child of a family.
func (s *Store) PurgeTenant(tenantID string) error {
	if tenantID == "" {
		return ErrInvalid
	}
	return s.purge(func(p Player) bool { return p.TenantID == tenantID })
}

func (s *Store) purge(match func(Player) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := map[Player]state{}
	for p, st := range s.players {
		if !match(p) {
			next[p] = st
		}
	}
	if len(next) == len(s.players) {
		return nil
	}
	return s.commit(next, s.keys)
}

// commit writes the file atomically and only then changes the state.
func (s *Store) commit(players map[Player]state, keys map[string]time.Time) error {
	f := file{Players: []row{}, Recorded: []recorded{}}
	for p, st := range players {
		f.Players = append(f.Players, row{Kind: p.Kind, TenantID: p.TenantID, ID: p.ID, Rating: st.rating, Games: st.games})
	}
	sort.Slice(f.Players, func(i, j int) bool {
		a, b := f.Players[i], f.Players[j]
		return string(a.Kind)+"\x00"+a.TenantID+"\x00"+a.ID < string(b.Kind)+"\x00"+b.TenantID+"\x00"+b.ID
	})
	for k, at := range keys {
		f.Recorded = append(f.Recorded, recorded{Key: k, At: at})
	}
	sort.Slice(f.Recorded, func(i, j int) bool { return f.Recorded[i].Key < f.Recorded[j].Key })
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".yapuno-ratings-*")
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
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	s.players, s.keys = players, keys
	return nil
}
