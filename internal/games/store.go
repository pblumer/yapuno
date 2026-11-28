// Package games stores chess games between two children whose families
// approved their contact and whose guardians enabled chess for each of them.
//
// Every invitation, action and read is authorized again from current state
// with authz.CanPlayGame. Rules and clocks are decided by the server: a
// client proposes a move, never its legality, the result or the time left.
// An action is acknowledged only after it has been appended to the log and
// synced to disk; retries with the same client id are idempotent.
//
// Ends that follow from time alone (an expired invitation, a time-out, an
// abandoned game) are derived from recorded server times whenever a game is
// read or acted on; nothing needs to be written for them. See
// docs/adr/draft-yapuno-play-chess.md.
package games

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/chess"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/play"
)

var (
	// ErrForbidden covers every authorization failure, including unknown
	// games, so callers cannot probe which games exist.
	ErrForbidden = errors.New("not allowed in this game")
	ErrInvalid   = errors.New("invalid game request")
	// ErrConflict means the client id was already used for another action.
	ErrConflict = errors.New("client id already used for a different action")
	// ErrStale means the client did not see the latest move.
	ErrStale = errors.New("the game has changed")
	// ErrNotNow means the action is not possible in the game's current
	// state: not this child's turn, no offer to answer, or the game is over.
	ErrNotNow      = errors.New("not possible now")
	ErrIllegal     = errors.New("illegal move")
	ErrBusy        = errors.New("an unfinished game already exists for this contact")
	ErrRateLimited = errors.New("too many requests")
	ErrUnavailable = errors.New("game could not be stored")
)

const (
	// InviteTTL is how long an invitation can be accepted.
	InviteTTL = 7 * 24 * time.Hour
	// AbandonAfter ends a game in which nothing happened for this long.
	AbandonAfter = 30 * 24 * time.Hour
	// Retention keeps an ended game this long after its end (issue #7).
	Retention = 30 * 24 * time.Hour
	// MaxInvitesPerDay limits one child's invitations to one contact.
	MaxInvitesPerDay = 5
	// MaxDrawOffers limits one player's draw offers in one game.
	MaxDrawOffers = 3

	maxLogBytes = 64 << 20
)

var (
	clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)
	gameIDPattern   = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// Mode is correspondence or live with a fixed time control.
type Mode string

const (
	Correspondence Mode = "correspondence"
	Live5          Mode = "live-5"
	Live10         Mode = "live-10"
	Live20         Mode = "live-20"
)

// Clock returns the time per side and the increment per move of a live
// mode; live is false for correspondence and unknown modes.
func (m Mode) Clock() (base, increment time.Duration, live bool) {
	switch m {
	case Live5:
		return 5 * time.Minute, 3 * time.Second, true
	case Live10:
		return 10 * time.Minute, 5 * time.Second, true
	case Live20:
		return 20 * time.Minute, 10 * time.Second, true
	}
	return 0, 0, false
}

func (m Mode) valid() bool {
	_, _, live := m.Clock()
	return live || m == Correspondence
}

// Phase of a game.
type Phase uint8

const (
	Invited Phase = iota + 1
	Running
	Finished
)

// End says how a game finished; empty while it has not.
type End string

const (
	EndCheckmate    End = "checkmate"
	EndStalemate    End = "stalemate"
	EndInsufficient End = "insufficient"
	EndRepetition   End = "repetition"
	EndFiftyMoves   End = "fifty-moves"
	EndResigned     End = "resigned"
	EndAgreed       End = "agreed"
	EndTimeout      End = "timeout"
	EndDeclined     End = "declined"
	EndWithdrawn    End = "withdrawn"
	EndExpired      End = "expired"
	EndAbandoned    End = "abandoned"
	EndAborted      End = "aborted"
)

// ActionKind is what a player does in a game.
type ActionKind string

const (
	Accept      ActionKind = "accept"
	Decline     ActionKind = "decline"
	Withdraw    ActionKind = "withdraw"
	Move        ActionKind = "move"
	Resign      ActionKind = "resign"
	OfferDraw   ActionKind = "offer_draw"
	AcceptDraw  ActionKind = "accept_draw"
	DeclineDraw ActionKind = "decline_draw"
	Abort       ActionKind = "abort"
)

var knownActions = map[ActionKind]bool{Accept: true, Decline: true, Withdraw: true, Move: true, Resign: true,
	OfferDraw: true, AcceptDraw: true, DeclineDraw: true, Abort: true}

// Action is one request of a player. Move and Ply are used by Move only:
// Ply is the number of half-moves the client has seen.
type Action struct {
	Kind     ActionKind
	ClientID string
	Move     string
	Ply      int
}

// Principal is a child on one device, taken from a verified child session.
type Principal struct {
	Party    contacts.Party
	DeviceID string
}

// Contacts exposes stored consent records.
type Contacts interface {
	Grant(id string, self contacts.Party) (contacts.Party, authz.ContactGrant, bool)
	Exists(id string) bool
}

// Devices confirms current device approval.
type Devices interface {
	ActiveDevice(tenantID, childID, deviceID string) (devicepairing.Device, bool)
}

// Children confirms that a profile exists.
type Children interface {
	ChildName(tenantID, childID string) (string, bool)
}

// Enablement says whether the guardians of a child currently allow a game.
type Enablement interface {
	Enabled(tenantID, childID, game string) bool
}

// View is one game as seen by one of its two players at one moment.
type View struct {
	ID      string
	Contact string
	Mode    Mode
	Phase   Phase
	End     End
	// Winner is NoColor for a draw or a game without result.
	Winner     chess.Color
	YourColor  chess.Color
	YouInvited bool
	Position   chess.Position
	Moves      []chess.Move
	// Version increases with every stored action.
	Version uint64
	// White and Black are the remaining times of a live game at At.
	White, Black time.Duration
	// Running is the side whose clock runs at At, NoColor if none.
	Running chess.Color
	At      time.Time
	// DrawOffer is the side whose draw offer is pending, NoColor if none.
	DrawOffer chess.Color
	// Expires is when an invitation lapses or a running game is abandoned.
	Expires time.Time
	EndedAt time.Time
}

// YourTurn reports whether the viewer is to move in a running game.
func (v View) YourTurn() bool { return v.Phase == Running && v.Position.Turn() == v.YourColor }

// YourTurnToAnswer reports whether the viewer is invited and may answer.
func (v View) YourTurnToAnswer() bool { return v.Phase == Invited && !v.YouInvited }

// entry is one log line.
type entry struct {
	Game     string    `json:"game"`
	Seq      uint64    `json:"seq"`
	At       time.Time `json:"at"`
	Type     string    `json:"type"`
	TenantID string    `json:"tenant_id"`
	ChildID  string    `json:"child_id"`
	ClientID string    `json:"client_id"`
	// Invitation only.
	Contact      string `json:"contact,omitempty"`
	PeerTenantID string `json:"peer_tenant_id,omitempty"`
	PeerChildID  string `json:"peer_child_id,omitempty"`
	Mode         Mode   `json:"mode,omitempty"`
	InviterColor string `json:"inviter_color,omitempty"`
	// Move only.
	Move string `json:"move,omitempty"`
	Ply  int    `json:"ply,omitempty"`
}

const typeInvite = "invite"

func (e entry) actor() contacts.Party {
	return contacts.Party{TenantID: e.TenantID, ChildID: e.ChildID}
}

type game struct {
	id, contact      string
	inviter, invitee contacts.Party
	inviterColor     chess.Color
	mode             Mode
	created          time.Time
	phase            Phase
	end              End
	winner           chess.Color
	endedAt          time.Time
	rules            *chess.Game
	moveAt           []time.Time
	left             [3]time.Duration // remaining time by chess.Color, after the last move
	offer            chess.Color
	offers           [3]int
	last             time.Time // time of the last stored action
	seq              uint64
	entries          []entry
}

func (g *game) colorOf(p contacts.Party) chess.Color {
	switch p {
	case g.inviter:
		return g.inviterColor
	case g.invitee:
		return g.inviterColor.Other()
	}
	return chess.NoColor
}

// settle returns the end of the game at now, whether it was recorded or
// follows from time alone, with the winner and the moment it ended.
func (g *game) settle(now time.Time) (End, chess.Color, time.Time) {
	if g.end != "" {
		return g.end, g.winner, g.endedAt
	}
	switch g.phase {
	case Invited:
		if deadline := g.created.Add(InviteTTL); !now.Before(deadline) {
			return EndExpired, chess.NoColor, deadline
		}
	case Running:
		if deadline, ok := g.flag(); ok && !now.Before(deadline) {
			turn := g.rules.Position().Turn()
			winner := turn.Other()
			if g.rules.Position().CannotMate(winner) {
				winner = chess.NoColor
			}
			return EndTimeout, winner, deadline
		}
		if deadline := g.last.Add(AbandonAfter); !now.Before(deadline) {
			return EndAbandoned, chess.NoColor, deadline
		}
	}
	return "", chess.NoColor, time.Time{}
}

// flag returns when the clock of the side to move runs out. Clocks run only
// in live games after each side has made its first move.
func (g *game) flag() (time.Time, bool) {
	_, _, live := g.mode.Clock()
	n := len(g.moveAt)
	if !live || n < 2 {
		return time.Time{}, false
	}
	return g.moveAt[n-1].Add(g.left[g.rules.Position().Turn()]), true
}

type idempotencyKey struct {
	scope    string // game id, or "invite:" + contact id
	actor    contacts.Party
	clientID string
}

type keyed struct {
	game        string
	fingerprint string
}

// Store is single-writer per file; a transactional shared backend is needed
// for more than one server process.
type Store struct {
	mu       sync.Mutex
	path     string
	contacts Contacts
	devices  Devices
	children Children
	enabled  Enablement
	now      func() time.Time

	file  *os.File
	size  int64
	games map[string]*game
	keys  map[idempotencyKey]keyed

	// coin decides whether the inviter plays white. It is a field so tests
	// can fix colours; production uses crypto/rand.
	coin func() (bool, error)
	// syncFile forces written data to stable storage; a field so tests can
	// fail it. failWrite makes the next append write only part of its line.
	syncFile  func(*os.File) error
	failWrite bool
}

func randomCoin() (bool, error) {
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		return false, err
	}
	return b[0]&1 == 1, nil
}

// Open loads the log. It fails closed on insecure permissions and on any
// complete line that is malformed, inconsistent or an illegal move. A
// partial final line is the trace of a crash during an unacknowledged
// append and is removed.
func Open(path string, c Contacts, d Devices, ch Children, en Enablement, now func() time.Time) (*Store, error) {
	if path == "" || c == nil || d == nil || ch == nil || en == nil || now == nil {
		return nil, ErrInvalid
	}
	s := &Store{path: path, contacts: c, devices: d, children: ch, enabled: en, now: now,
		games: map[string]*game{}, keys: map[idempotencyKey]keyed{}, coin: randomCoin, syncFile: (*os.File).Sync}
	stat, err := os.Lstat(path)
	created := errors.Is(err, os.ErrNotExist)
	if err != nil && !created {
		return nil, err
	}
	if !created {
		if !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 || stat.Size() > maxLogBytes {
			return nil, fmt.Errorf("%w: log must be a private regular file", ErrInvalid)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		complete := data
		if i := bytes.LastIndexByte(data, '\n'); i < len(data)-1 {
			complete = data[:i+1]
			if err := os.Truncate(path, int64(len(complete))); err != nil {
				return nil, err
			}
		}
		for _, line := range bytes.Split(complete, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			if err := s.load(line); err != nil {
				return nil, err
			}
		}
		s.size = int64(len(complete))
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	s.file = f
	if created {
		if err := syncDir(filepath.Dir(path)); err != nil {
			f.Close()
			return nil, err
		}
	}
	if err := s.compact(); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (s *Store) load(line []byte) error {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	var e entry
	if err := dec.Decode(&e); err != nil {
		return fmt.Errorf("%w: corrupt log line", ErrInvalid)
	}
	if !gameIDPattern.MatchString(e.Game) || e.At.IsZero() || !clientIDPattern.MatchString(e.ClientID) {
		return fmt.Errorf("%w: log line", ErrInvalid)
	}
	g := s.games[e.Game]
	if e.Type == typeInvite {
		if g != nil || e.Seq != 1 {
			return fmt.Errorf("%w: invitation", ErrInvalid)
		}
		g, err := newGame(e)
		if err != nil {
			return err
		}
		s.games[e.Game] = g
		s.remember(g, e)
		return nil
	}
	if g == nil || e.Seq != g.seq+1 || e.At.Before(g.last) {
		return fmt.Errorf("%w: sequence", ErrInvalid)
	}
	a := Action{Kind: ActionKind(e.Type), ClientID: e.ClientID, Move: e.Move, Ply: e.Ply}
	if err := g.check(e.actor(), a, e.At); err != nil {
		return fmt.Errorf("%w: stored action: %v", ErrInvalid, err)
	}
	g.apply(e)
	s.remember(g, e)
	return nil
}

func colorName(c chess.Color) string {
	if c == chess.White {
		return "white"
	}
	return "black"
}

func newGame(e entry) (*game, error) {
	inviter := e.actor()
	invitee := contacts.Party{TenantID: e.PeerTenantID, ChildID: e.PeerChildID}
	color := map[string]chess.Color{"white": chess.White, "black": chess.Black}[e.InviterColor]
	if e.Contact == "" || len(e.Contact) > 128 || inviter.TenantID == "" || inviter.ChildID == "" ||
		invitee.TenantID == "" || invitee.ChildID == "" || inviter == invitee || !e.Mode.valid() ||
		color == chess.NoColor || e.Move != "" || e.Ply != 0 {
		return nil, fmt.Errorf("%w: invitation", ErrInvalid)
	}
	g := &game{id: e.Game, contact: e.Contact, inviter: inviter, invitee: invitee, inviterColor: color,
		mode: e.Mode, created: e.At, phase: Invited, rules: chess.NewGame(), last: e.At, seq: 1,
		entries: []entry{e}}
	base, _, _ := e.Mode.Clock()
	g.left[chess.White], g.left[chess.Black] = base, base
	return g, nil
}

func fingerprint(a Action) string { return string(a.Kind) + " " + a.Move + " " + fmt.Sprint(a.Ply) }

func (s *Store) remember(g *game, e entry) {
	scope := g.id
	fp := fingerprint(Action{Kind: ActionKind(e.Type), Move: e.Move, Ply: e.Ply})
	if e.Type == typeInvite {
		scope, fp = "invite:"+g.contact, string(e.Mode)
	}
	s.keys[idempotencyKey{scope, e.actor(), e.ClientID}] = keyed{game: g.id, fingerprint: fp}
}

// check decides whether actor may perform a at now. It changes nothing.
func (g *game) check(actor contacts.Party, a Action, now time.Time) error {
	color := g.colorOf(actor)
	if color == chess.NoColor || !knownActions[a.Kind] {
		return ErrInvalid
	}
	if a.Kind != Move && (a.Move != "" || a.Ply != 0) {
		return ErrInvalid
	}
	if end, _, _ := g.settle(now); end != "" {
		return ErrNotNow
	}
	other := color.Other()
	switch a.Kind {
	case Accept, Decline:
		if g.phase != Invited || actor != g.invitee {
			return ErrNotNow
		}
	case Withdraw:
		if g.phase != Invited || actor != g.inviter {
			return ErrNotNow
		}
	case Move:
		if g.phase != Running {
			return ErrNotNow
		}
		if a.Ply != len(g.moveAt) {
			return ErrStale
		}
		if g.rules.Position().Turn() != color {
			return ErrNotNow
		}
		m, err := chess.ParseUCI(a.Move)
		if err != nil {
			return ErrInvalid
		}
		if g.rules.Check(m) != nil {
			return ErrIllegal
		}
	case Resign:
		if g.phase != Running {
			return ErrNotNow
		}
	case Abort:
		if g.phase != Running || len(g.moveAt) >= 2 {
			return ErrNotNow
		}
	case OfferDraw:
		if g.phase != Running || g.offer != chess.NoColor {
			return ErrNotNow
		}
		if g.offers[color] >= MaxDrawOffers {
			return ErrRateLimited
		}
	case AcceptDraw, DeclineDraw:
		if g.phase != Running || g.offer != other {
			return ErrNotNow
		}
	}
	return nil
}

var ruleEnds = map[chess.StatusKind]End{chess.Checkmate: EndCheckmate, chess.Stalemate: EndStalemate,
	chess.InsufficientMaterial: EndInsufficient, chess.Repetition: EndRepetition, chess.FiftyMoves: EndFiftyMoves}

// apply records an action that check accepted.
func (g *game) apply(e entry) {
	color := g.colorOf(e.actor())
	finish := func(end End, winner chess.Color) {
		g.phase, g.end, g.winner, g.endedAt = Finished, end, winner, e.At
	}
	switch ActionKind(e.Type) {
	case Accept:
		g.phase = Running
	case Decline:
		finish(EndDeclined, chess.NoColor)
	case Withdraw:
		finish(EndWithdrawn, chess.NoColor)
	case Move:
		m, _ := chess.ParseUCI(e.Move)
		if n := len(g.moveAt); n >= 2 {
			_, increment, _ := g.mode.Clock()
			g.left[color] += increment - e.At.Sub(g.moveAt[n-1])
		}
		_ = g.rules.Play(m)
		g.moveAt = append(g.moveAt, e.At)
		if g.offer == color.Other() {
			g.offer = chess.NoColor
		}
		if st := g.rules.Status(); st.Kind != chess.Ongoing {
			finish(ruleEnds[st.Kind], st.Winner)
		}
	case Resign:
		finish(EndResigned, color.Other())
	case Abort:
		finish(EndAborted, chess.NoColor)
	case OfferDraw:
		g.offer = color
		g.offers[color]++
	case AcceptDraw:
		finish(EndAgreed, chess.NoColor)
	case DeclineDraw:
		g.offer = chess.NoColor
	}
	g.seq, g.last = e.Seq, e.At
	g.entries = append(g.entries, e)
}

// authorize loads the current state behind one attempt and applies
// authz.CanPlayGame. It runs at the moment of every call.
func (s *Store) authorize(p Principal, contactID string) (contacts.Party, bool) {
	if contactID == "" || p.DeviceID == "" || p.Party.TenantID == "" || p.Party.ChildID == "" {
		return contacts.Party{}, false
	}
	peer, grant, ok := s.contacts.Grant(contactID, p.Party)
	if !ok {
		return contacts.Party{}, false
	}
	_, device := s.devices.ActiveDevice(p.Party.TenantID, p.Party.ChildID, p.DeviceID)
	_, self := s.children.ChildName(p.Party.TenantID, p.Party.ChildID)
	_, other := s.children.ChildName(peer.TenantID, peer.ChildID)
	allowed := authz.CanPlayGame(authz.GameAccess{
		Conversation: authz.ConversationAccess{
			Self: p.Party.Participant(), Peer: peer.Participant(),
			SelfEnabled: self, PeerEnabled: other, DeviceApproved: device, Contact: grant,
		},
		SelfGameEnabled: s.enabled.Enabled(p.Party.TenantID, p.Party.ChildID, play.GameChess),
		PeerGameEnabled: s.enabled.Enabled(peer.TenantID, peer.ChildID, play.GameChess),
	})
	return peer, allowed
}

// authorizedGame returns a game of which p is a player, if p may play it now.
func (s *Store) authorizedGame(p Principal, gameID string) (*game, bool) {
	g, ok := s.games[gameID]
	if !ok {
		return nil, false
	}
	peer, ok := s.authorize(p, g.contact)
	if !ok || !((p.Party == g.inviter && peer == g.invitee) || (p.Party == g.invitee && peer == g.inviter)) {
		return nil, false
	}
	return g, true
}

func (s *Store) view(g *game, self contacts.Party, now time.Time) View {
	v := View{ID: g.id, Contact: g.contact, Mode: g.mode, Phase: g.phase, YourColor: g.colorOf(self),
		YouInvited: self == g.inviter, Position: g.rules.Position(), Moves: g.rules.Moves(), Version: g.seq,
		At: now, DrawOffer: g.offer, White: g.left[chess.White], Black: g.left[chess.Black]}
	end, winner, at := g.settle(now)
	if end != "" {
		v.Phase, v.End, v.Winner, v.EndedAt, v.DrawOffer = Finished, end, winner, at, chess.NoColor
	}
	if deadline, ok := g.flag(); ok && g.phase == Running {
		turn := v.Position.Turn()
		stop := now
		if end != "" && at.Before(now) {
			stop = at
		}
		left := deadline.Sub(stop)
		if left < 0 {
			left = 0
		}
		if turn == chess.White {
			v.White = left
		} else {
			v.Black = left
		}
		if end == "" {
			v.Running = turn
		}
	}
	switch {
	case end != "":
	case g.phase == Invited:
		v.Expires = g.created.Add(InviteTTL)
	case g.phase == Running:
		v.Expires = g.last.Add(AbandonAfter)
	}
	return v
}

// stamp returns the time to record: now, but never before the game's last
// action, so a clock step backwards cannot make the log inconsistent.
func stamp(now, last time.Time) time.Time {
	if now.Before(last) {
		return last
	}
	return now
}

func newGameID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Invite starts a game with the other child of a contact. The inviter's
// colour is chosen by the server.
func (s *Store) Invite(p Principal, contactID, clientID string, mode Mode) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	peer, ok := s.authorize(p, contactID)
	if !ok {
		return View{}, ErrForbidden
	}
	if !mode.valid() || !clientIDPattern.MatchString(clientID) {
		return View{}, ErrInvalid
	}
	now := s.now().UTC()
	if k, seen := s.keys[idempotencyKey{"invite:" + contactID, p.Party, clientID}]; seen {
		if k.fingerprint != string(mode) {
			return View{}, ErrConflict
		}
		return s.view(s.games[k.game], p.Party, now), nil
	}
	recent := 0
	for _, g := range s.games {
		if g.contact != contactID {
			continue
		}
		if end, _, _ := g.settle(now); end == "" {
			return View{}, ErrBusy
		}
		if g.inviter == p.Party && g.created.After(now.Add(-24*time.Hour)) {
			recent++
		}
	}
	if recent >= MaxInvitesPerDay {
		return View{}, ErrRateLimited
	}
	white, err := s.coin()
	if err != nil {
		return View{}, ErrUnavailable
	}
	id, err := newGameID()
	if err != nil {
		return View{}, ErrUnavailable
	}
	color := chess.Black
	if white {
		color = chess.White
	}
	e := entry{Game: id, Seq: 1, At: now, Type: typeInvite, TenantID: p.Party.TenantID, ChildID: p.Party.ChildID,
		ClientID: clientID, Contact: contactID, PeerTenantID: peer.TenantID, PeerChildID: peer.ChildID,
		Mode: mode, InviterColor: colorName(color)}
	g, err := newGame(e)
	if err != nil {
		return View{}, err
	}
	if err := s.append(e); err != nil {
		return View{}, ErrUnavailable
	}
	s.games[id] = g
	s.remember(g, e)
	return s.view(g, p.Party, now), nil
}

// Act performs one action. The second result is true when this client id
// was already stored for the same action; nothing is written then.
func (s *Store) Act(p Principal, gameID string, a Action) (View, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.authorizedGame(p, gameID)
	if !ok {
		return View{}, false, ErrForbidden
	}
	if !clientIDPattern.MatchString(a.ClientID) || !knownActions[a.Kind] {
		return View{}, false, ErrInvalid
	}
	now := s.now().UTC()
	if k, seen := s.keys[idempotencyKey{gameID, p.Party, a.ClientID}]; seen {
		if k.fingerprint != fingerprint(a) {
			return View{}, false, ErrConflict
		}
		return s.view(g, p.Party, now), true, nil
	}
	at := stamp(now, g.last)
	if err := g.check(p.Party, a, at); err != nil {
		return View{}, false, err
	}
	e := entry{Game: gameID, Seq: g.seq + 1, At: at, Type: string(a.Kind), TenantID: p.Party.TenantID,
		ChildID: p.Party.ChildID, ClientID: a.ClientID, Move: a.Move, Ply: a.Ply}
	if err := s.append(e); err != nil {
		return View{}, false, ErrUnavailable
	}
	g.apply(e)
	s.remember(g, e)
	return s.view(g, p.Party, now), false, nil
}

// Game returns one game for one of its players.
func (s *Store) Game(p Principal, gameID string) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.authorizedGame(p, gameID)
	if !ok {
		return View{}, ErrForbidden
	}
	return s.view(g, p.Party, s.now().UTC()), nil
}

// Latest returns the unfinished game of a contact, or else its most recent
// one. The second result is false when the contact has no stored game.
func (s *Store) Latest(p Principal, contactID string) (View, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.authorize(p, contactID); !ok {
		return View{}, false, ErrForbidden
	}
	now := s.now().UTC()
	var best *game
	bestOpen := false
	for _, g := range s.games {
		if g.contact != contactID {
			continue
		}
		end, _, _ := g.settle(now)
		open := end == ""
		if best == nil || (open && !bestOpen) || (open == bestOpen && (g.created.After(best.created) ||
			(g.created.Equal(best.created) && g.id > best.id))) {
			best, bestOpen = g, open
		}
	}
	if best == nil {
		return View{}, false, nil
	}
	return s.view(best, p.Party, now), true, nil
}

// append writes one line and syncs it. On any failure the log is cut back to
// its previous length so a partial line cannot corrupt later appends.
func (s *Store) append(e entry) error {
	if s.file == nil {
		return ErrUnavailable
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if s.failWrite {
		s.failWrite = false
		_, _ = s.file.Write(line[:len(line)/2])
		err = errors.New("simulated short write")
	} else {
		_, err = s.file.Write(line)
	}
	if err == nil {
		err = s.syncFile(s.file)
	}
	if err != nil {
		if terr := s.file.Truncate(s.size); terr == nil {
			_ = s.syncFile(s.file)
		}
		return err
	}
	s.size += int64(len(line))
	return nil
}

// Compact deletes games whose contact no longer exists and games that ended
// more than Retention ago, then rewrites the log atomically.
func (s *Store) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.compact()
}

func (s *Store) compact() error {
	now := s.now().UTC()
	ids := make([]string, 0, len(s.games))
	for id := range s.games {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := s.games[ids[i]], s.games[ids[j]]
		return a.created.Before(b.created) || (a.created.Equal(b.created) && a.id < b.id)
	})
	next := map[string]*game{}
	var out bytes.Buffer
	for _, id := range ids {
		g := s.games[id]
		if !s.contacts.Exists(g.contact) {
			continue
		}
		if end, _, at := g.settle(now); end != "" && !now.Before(at.Add(Retention)) {
			continue
		}
		next[id] = g
		for _, e := range g.entries {
			line, err := json.Marshal(e)
			if err != nil {
				return err
			}
			out.Write(append(line, '\n'))
		}
	}
	if len(next) == len(s.games) {
		return nil
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".yapuno-games-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(out.Bytes()); err != nil {
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
	if err := syncDir(dir); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if s.file != nil {
		_ = s.file.Close()
	}
	s.file, s.size, s.games = f, int64(out.Len()), next
	s.keys = map[idempotencyKey]keyed{}
	for _, g := range next {
		for _, e := range g.entries {
			s.remember(g, e)
		}
	}
	return nil
}

// Close releases the log file.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}
