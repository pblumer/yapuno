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
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/play"
	"github.com/pblumer/yapuno/internal/rating"
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
	// MaxTips is how many tips each player may take in a game with tips.
	MaxTips = 3
	// tipNodes bounds the search behind a level-2 tip.
	tipNodes = 60000

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
	// Hint asks for a tip: Level 1 explains, Level 2 names a move.
	Hint ActionKind = "hint"
)

var knownActions = map[ActionKind]bool{Accept: true, Decline: true, Withdraw: true, Move: true, Resign: true,
	OfferDraw: true, AcceptDraw: true, DeclineDraw: true, Abort: true, Hint: true}

// Action is one request of a player. Ply, the number of half-moves the
// client has seen, is used by Move and Hint; Move by Move; Level by Hint.
type Action struct {
	Kind     ActionKind
	ClientID string
	Move     string
	Ply      int
	Level    int
}

// Tip is a tip a player took on one move. Best is set from level 2.
type Tip struct {
	Level int
	Hint  chess.Hint
	Best  chess.Move
}

// Principal is a child on one device, taken from a verified child session.
type Principal struct {
	Party    contacts.Party
	DeviceID string
}

// GuardianPrincipal is a guardian, taken from a verified guardian session.
type GuardianPrincipal struct {
	TenantID, GuardianID string
}

// actor is whoever acts: a child on a device, or a guardian.
type actor struct {
	who    authz.Participant
	device string
}

func childParticipant(tenantID, childID string) authz.Participant {
	return authz.Participant{TenantID: tenantID, ID: childID, Kind: authz.ParticipantChild}
}

func guardianParticipant(tenantID, guardianID string) authz.Participant {
	return authz.Participant{TenantID: tenantID, ID: guardianID, Kind: authz.ParticipantAdult}
}

func childActor(p Principal) actor {
	return actor{who: childParticipant(p.Party.TenantID, p.Party.ChildID), device: p.DeviceID}
}

func guardianActor(g GuardianPrincipal) actor {
	return actor{who: guardianParticipant(g.TenantID, g.GuardianID)}
}

// FamilyScope is the stable identifier of the games of a guardian with
// their own child. It is derived, not secret: knowing it grants nothing.
func FamilyScope(tenantID, guardianID, childID string) string {
	sum := sha256.Sum256([]byte("yapuno family chess\x00" + tenantID + "\x00" + guardianID + "\x00" + childID))
	return hex.EncodeToString(sum[:16])
}

// Families is the authoritative source for guardian grants.
type Families interface {
	Cards(guardianID, tenantID string) []familydashboard.ChildCard
	GuardiansOf(tenantID, childID string) []string
}

// Guardians says whether a guardian identity is active and not locked.
type Guardians interface {
	ActiveGuardian(ctx context.Context, guardianID, tenantID string) bool
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
	ID string
	// Scope is the contact id of two children, or the FamilyScope of a
	// guardian and their child. Family says which.
	Scope  string
	Family bool
	Mode   Mode
	Phase  Phase
	End    End
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
	// Tips says the game was started with tips. TipsLeft is the viewer's
	// remaining tips, TheirTipsUsed how many the other player took. Tip is
	// the viewer's own tip on the current move, if any.
	Tips          bool
	TipsLeft      int
	TheirTipsUsed int
	Tip           *Tip
}

// YourTurn reports whether the viewer is to move in a running game.
func (v View) YourTurn() bool { return v.Phase == Running && v.Position.Turn() == v.YourColor }

// YourTurnToAnswer reports whether the viewer is invited and may answer.
func (v View) YourTurnToAnswer() bool { return v.Phase == Invited && !v.YouInvited }

// entry is one log line.
type entry struct {
	Game string    `json:"game"`
	Seq  uint64    `json:"seq"`
	At   time.Time `json:"at"`
	Type string    `json:"type"`
	// The actor is a child (ChildID) or a guardian (GuardianID), never both.
	TenantID   string `json:"tenant_id"`
	ChildID    string `json:"child_id,omitempty"`
	GuardianID string `json:"guardian_id,omitempty"`
	ClientID   string `json:"client_id"`
	// Invitation only. Contact is the scope; Family marks a family game.
	Contact        string `json:"contact,omitempty"`
	Family         bool   `json:"family,omitempty"`
	PeerTenantID   string `json:"peer_tenant_id,omitempty"`
	PeerChildID    string `json:"peer_child_id,omitempty"`
	PeerGuardianID string `json:"peer_guardian_id,omitempty"`
	Mode           Mode   `json:"mode,omitempty"`
	InviterColor   string `json:"inviter_color,omitempty"`
	Tips           bool   `json:"tips,omitempty"`
	// Move and hint.
	Move  string     `json:"move,omitempty"`
	Ply   int        `json:"ply,omitempty"`
	Level int        `json:"level,omitempty"`
	Tip   *storedTip `json:"tip,omitempty"`
}

// storedTip is the content of a tip as computed when it was taken.
type storedTip struct {
	Kind   uint8  `json:"kind"`
	Square int8   `json:"square"`
	Piece  uint8  `json:"piece"`
	Best   string `json:"best,omitempty"`
}

const typeInvite = "invite"

func participant(tenantID, childID, guardianID string) (authz.Participant, bool) {
	switch {
	case tenantID == "" || (childID == "") == (guardianID == ""):
		return authz.Participant{}, false
	case guardianID != "":
		return guardianParticipant(tenantID, guardianID), true
	}
	return childParticipant(tenantID, childID), true
}

func (e entry) actor() authz.Participant {
	p, _ := participant(e.TenantID, e.ChildID, e.GuardianID)
	return p
}

// setActor fills the actor fields of an entry.
func (e *entry) setActor(p authz.Participant) {
	e.TenantID = p.TenantID
	if p.Kind == authz.ParticipantAdult {
		e.GuardianID = p.ID
	} else {
		e.ChildID = p.ID
	}
}

type game struct {
	id, scope        string
	family           bool
	inviter, invitee authz.Participant
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
	tips             bool
	rated            bool // rated, or ended without a rated result
	tipsUsed         [3]int
	tipAt            [3]map[int]*Tip // by colour, then half-move
}

func (g *game) colorOf(p authz.Participant) chess.Color {
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
	actor    authz.Participant
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
	// families and guardians are set by EnableFamilies.
	families  Families
	guardians Guardians
	rater     Rater

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
	if _, ok := participant(e.TenantID, e.ChildID, e.GuardianID); !ok || !gameIDPattern.MatchString(e.Game) || e.At.IsZero() || !clientIDPattern.MatchString(e.ClientID) {
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
	a.Level = e.Level
	if err := g.check(e.actor(), a, e.At); err != nil {
		return fmt.Errorf("%w: stored action: %v", ErrInvalid, err)
	}
	if (a.Kind == Hint) != (e.Tip != nil) || a.Kind == Hint && !validTip(g.rules.Position(), e) {
		return fmt.Errorf("%w: stored tip", ErrInvalid)
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
	inviter, ok1 := participant(e.TenantID, e.ChildID, e.GuardianID)
	invitee, ok2 := participant(e.PeerTenantID, e.PeerChildID, e.PeerGuardianID)
	color := map[string]chess.Color{"white": chess.White, "black": chess.Black}[e.InviterColor]
	if !ok1 || !ok2 || e.Contact == "" || len(e.Contact) > 128 || inviter == invitee || !e.Mode.valid() ||
		color == chess.NoColor || e.Move != "" || e.Ply != 0 || e.Level != 0 || e.Tip != nil {
		return nil, fmt.Errorf("%w: invitation", ErrInvalid)
	}
	// Two children of a contact, or a guardian with their own child.
	if e.Family {
		adult, child := inviter, invitee
		if adult.Kind != authz.ParticipantAdult {
			adult, child = invitee, inviter
		}
		if adult.Kind != authz.ParticipantAdult || child.Kind != authz.ParticipantChild || adult.TenantID != child.TenantID ||
			e.Contact != FamilyScope(adult.TenantID, adult.ID, child.ID) {
			return nil, fmt.Errorf("%w: family invitation", ErrInvalid)
		}
	} else if inviter.Kind != authz.ParticipantChild || invitee.Kind != authz.ParticipantChild {
		return nil, fmt.Errorf("%w: invitation", ErrInvalid)
	}
	g := &game{id: e.Game, scope: e.Contact, family: e.Family, inviter: inviter, invitee: invitee, inviterColor: color,
		mode: e.Mode, created: e.At, phase: Invited, rules: chess.NewGame(), last: e.At, seq: 1,
		entries: []entry{e}, tips: e.Tips}
	g.tipAt[chess.White], g.tipAt[chess.Black] = map[int]*Tip{}, map[int]*Tip{}
	base, _, _ := e.Mode.Clock()
	g.left[chess.White], g.left[chess.Black] = base, base
	return g, nil
}

func fingerprint(a Action) string {
	return fmt.Sprintf("%s %s %d %d", a.Kind, a.Move, a.Ply, a.Level)
}

func (s *Store) remember(g *game, e entry) {
	scope := g.id
	fp := fingerprint(Action{Kind: ActionKind(e.Type), Move: e.Move, Ply: e.Ply, Level: e.Level})
	if e.Type == typeInvite {
		scope, fp = "invite:"+g.scope, inviteFingerprint(e.Mode, e.Tips)
	}
	s.keys[idempotencyKey{scope, e.actor(), e.ClientID}] = keyed{game: g.id, fingerprint: fp}
}

// check decides whether actor may perform a at now. It changes nothing.
func (g *game) check(actor authz.Participant, a Action, now time.Time) error {
	color := g.colorOf(actor)
	if color == chess.NoColor || !knownActions[a.Kind] {
		return ErrInvalid
	}
	if a.Kind != Move && a.Move != "" || a.Kind != Move && a.Kind != Hint && a.Ply != 0 {
		return ErrInvalid
	}
	if a.Kind == Hint && a.Level != 1 && a.Level != 2 || a.Kind != Hint && a.Level != 0 {
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
	case Hint:
		if g.phase != Running || !g.tips {
			return ErrNotNow
		}
		if a.Ply != len(g.moveAt) {
			return ErrStale
		}
		if g.rules.Position().Turn() != color {
			return ErrNotNow
		}
		taken := g.tipAt[color][a.Ply]
		if a.Level == 1 && taken != nil || a.Level == 2 && (taken == nil || taken.Level != 1) {
			return ErrNotNow
		}
		if a.Level == 1 && g.tipsUsed[color] >= MaxTips {
			return ErrRateLimited
		}
	}
	return nil
}

// computeTip works out the content of a tip for the current position.
func computeTip(pos chess.Position, level int) *storedTip {
	h := chess.Analyze(pos)
	t := &storedTip{Kind: uint8(h.Kind), Square: int8(h.Square), Piece: uint8(h.Piece)}
	if level == 2 {
		if m, _, ok := chess.Search(pos, chess.SearchOptions{Depth: 3, Quiescence: true, NodeLimit: tipNodes}); ok {
			t.Best = m.String()
		}
	}
	return t
}

// validTip checks the shape of a stored tip against the position it was
// taken in.
func validTip(pos chess.Position, e entry) bool {
	t := e.Tip
	if t == nil || t.Kind == uint8(chess.HintNone) || t.Kind > uint8(chess.HintActivate) || t.Square < 0 || t.Square > 63 || t.Piece > uint8(chess.King) {
		return false
	}
	if e.Level == 1 {
		return t.Best == ""
	}
	m, err := chess.ParseUCI(t.Best)
	return err == nil && pos.Legal(m)
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
	case Hint:
		tip := &Tip{Level: e.Level, Hint: chess.Hint{Kind: chess.HintKind(e.Tip.Kind), Square: chess.Square(e.Tip.Square), Piece: chess.PieceType(e.Tip.Piece)}}
		if e.Level == 1 {
			g.tipsUsed[color]++
		} else {
			tip.Best, _ = chess.ParseUCI(e.Tip.Best)
		}
		g.tipAt[color][e.Ply] = tip
	}
	g.seq, g.last = e.Seq, e.At
	g.entries = append(g.entries, e)
}

// allowedScope loads the current state behind one attempt for a scope and
// returns the other player, whether it is a family game, and whether the
// actor may play there right now. It runs at the moment of every call.
func (s *Store) allowedScope(a actor, scope string) (authz.Participant, bool, bool) {
	w := a.who
	if scope == "" || w.TenantID == "" || w.ID == "" {
		return authz.Participant{}, false, false
	}
	switch w.Kind {
	case authz.ParticipantChild:
		if a.device == "" {
			return authz.Participant{}, false, false
		}
		self := contacts.Party{TenantID: w.TenantID, ChildID: w.ID}
		if peer, grant, ok := s.contacts.Grant(scope, self); ok {
			_, device := s.devices.ActiveDevice(w.TenantID, w.ID, a.device)
			_, mine := s.children.ChildName(w.TenantID, w.ID)
			_, other := s.children.ChildName(peer.TenantID, peer.ChildID)
			allowed := authz.CanPlayGame(authz.GameAccess{
				Conversation: authz.ConversationAccess{
					Self: w, Peer: peer.Participant(),
					SelfEnabled: mine, PeerEnabled: other, DeviceApproved: device, Contact: grant,
				},
				SelfGameEnabled: s.enabled.Enabled(w.TenantID, w.ID, play.GameChess),
				PeerGameEnabled: s.enabled.Enabled(peer.TenantID, peer.ChildID, play.GameChess),
			})
			return peer.Participant(), false, allowed
		}
		if s.families == nil {
			return authz.Participant{}, false, false
		}
		for _, g := range s.families.GuardiansOf(w.TenantID, w.ID) {
			if FamilyScope(w.TenantID, g, w.ID) == scope {
				guardian := guardianParticipant(w.TenantID, g)
				return guardian, true, s.familyAllowed(a, guardian, w)
			}
		}
	case authz.ParticipantAdult:
		if s.families == nil {
			return authz.Participant{}, false, false
		}
		for _, c := range s.families.Cards(w.ID, w.TenantID) {
			if FamilyScope(w.TenantID, w.ID, c.ID) == scope {
				child := childParticipant(w.TenantID, c.ID)
				return child, true, s.familyAllowed(a, w, child)
			}
		}
	}
	return authz.Participant{}, false, false
}

// familyAllowed applies authz.CanPlayFamilyGame from current state.
func (s *Store) familyAllowed(a actor, guardian, child authz.Participant) bool {
	var grant authz.GuardianGrant
	for _, c := range s.families.Cards(guardian.ID, guardian.TenantID) {
		if c.ID == child.ID {
			grant = authz.GuardianGrant{GuardianID: guardian.ID, ChildID: child.ID, TenantID: child.TenantID, Active: true,
				CanManageDevices: c.Permissions.ManageDevices, CanApproveContacts: c.Permissions.ApproveContacts}
		}
	}
	_, enabled := s.children.ChildName(child.TenantID, child.ID)
	approved := false
	if a.who == child && a.device != "" {
		_, approved = s.devices.ActiveDevice(child.TenantID, child.ID, a.device)
	}
	return authz.CanPlayFamilyGame(authz.FamilyGameAccess{
		Chat: authz.FamilyChatAccess{Self: a.who, Guardian: guardian, Child: child, Grant: grant,
			GuardianActive: s.guardians.ActiveGuardian(context.Background(), guardian.ID, guardian.TenantID),
			ChildEnabled:   enabled, DeviceApproved: approved},
		ChildGameEnabled: s.enabled.Enabled(child.TenantID, child.ID, play.GameChess),
	})
}

// authorizedGame returns a game of which the actor is a player, if the
// actor may play it now.
func (s *Store) authorizedGame(a actor, gameID string) (*game, bool) {
	g, ok := s.games[gameID]
	if !ok {
		return nil, false
	}
	peer, family, ok := s.allowedScope(a, g.scope)
	if !ok || family != g.family || !((a.who == g.inviter && peer == g.invitee) || (a.who == g.invitee && peer == g.inviter)) {
		return nil, false
	}
	return g, true
}

// exists says whether the relationship behind a game still exists, which
// decides what compaction keeps.
func (s *Store) exists(g *game) bool {
	if !g.family {
		return s.contacts.Exists(g.scope)
	}
	if s.families == nil {
		return true
	}
	adult, child := g.inviter, g.invitee
	if adult.Kind != authz.ParticipantAdult {
		adult, child = child, adult
	}
	for _, id := range s.families.GuardiansOf(child.TenantID, child.ID) {
		if id == adult.ID {
			return true
		}
	}
	return false
}

// Rater rates finished games. Record must be idempotent by game key.
type Rater interface {
	Record(gameKey string, a, b rating.Player, scoreA float64) error
}

var ratedEnds = map[End]bool{EndCheckmate: true, EndStalemate: true, EndInsufficient: true, EndRepetition: true,
	EndFiftyMoves: true, EndResigned: true, EndAgreed: true, EndTimeout: true}

func ratingPlayer(p authz.Participant) rating.Player {
	kind := rating.Child
	if p.Kind == authz.ParticipantAdult {
		kind = rating.Guardian
	}
	return rating.Player{Kind: kind, TenantID: p.TenantID, ID: p.ID}
}

// rate passes a finished game to the rater once. A failure is retried the
// next time the game is read or compacted.
func (s *Store) rate(g *game, now time.Time) {
	if s.rater == nil || g.rated {
		return
	}
	end, winner, _ := g.settle(now)
	if end == "" {
		return
	}
	if !ratedEnds[end] {
		g.rated = true
		return
	}
	score := 0.5
	switch winner {
	case g.inviterColor:
		score = 1
	case g.inviterColor.Other():
		score = 0
	}
	if s.rater.Record("game:"+g.id, ratingPlayer(g.inviter), ratingPlayer(g.invitee), score) == nil {
		g.rated = true
	}
}

// SetRater switches on ratings and rates every game that has ended.
func (s *Store) SetRater(r Rater) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rater = r
	now := s.now().UTC()
	for _, g := range s.games {
		s.rate(g, now)
	}
}

// EnableFamilies switches on games between guardians and their own
// children. Without it, no family game is authorized.
func (s *Store) EnableFamilies(f Families, g Guardians) error {
	if f == nil || g == nil {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.families, s.guardians = f, g
	return s.compact()
}

func (s *Store) view(g *game, self authz.Participant, now time.Time) View {
	s.rate(g, now)
	v := View{ID: g.id, Scope: g.scope, Family: g.family, Mode: g.mode, Phase: g.phase, YourColor: g.colorOf(self),
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
	if g.tips {
		me := v.YourColor
		v.Tips, v.TipsLeft, v.TheirTipsUsed = true, MaxTips-g.tipsUsed[me], g.tipsUsed[me.Other()]
		if t := g.tipAt[me][len(g.moveAt)]; t != nil && end == "" {
			copied := *t
			v.Tip = &copied
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

func inviteFingerprint(mode Mode, tips bool) string { return fmt.Sprintf("%s %t", mode, tips) }

// Invite starts a game in a scope: with the other child of a contact, or
// with one of the child's own guardians (FamilyScope). The inviter's colour
// is chosen by the server; tips says whether both players may take tips.
func (s *Store) Invite(p Principal, scope, clientID string, mode Mode, tips bool) (View, error) {
	return s.invite(childActor(p), scope, clientID, mode, tips)
}

// GuardianInvite starts a game of a guardian with their own child.
func (s *Store) GuardianInvite(g GuardianPrincipal, childID, clientID string, mode Mode, tips bool) (View, error) {
	return s.invite(guardianActor(g), FamilyScope(g.TenantID, g.GuardianID, childID), clientID, mode, tips)
}

func (s *Store) invite(p actor, contactID, clientID string, mode Mode, tips bool) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	peer, family, ok := s.allowedScope(p, contactID)
	if !ok {
		return View{}, ErrForbidden
	}
	if !mode.valid() || !clientIDPattern.MatchString(clientID) {
		return View{}, ErrInvalid
	}
	now := s.now().UTC()
	if k, seen := s.keys[idempotencyKey{"invite:" + contactID, p.who, clientID}]; seen {
		if k.fingerprint != inviteFingerprint(mode, tips) {
			return View{}, ErrConflict
		}
		return s.view(s.games[k.game], p.who, now), nil
	}
	recent := 0
	for _, g := range s.games {
		if g.scope != contactID {
			continue
		}
		if end, _, _ := g.settle(now); end == "" {
			return View{}, ErrBusy
		}
		if g.inviter == p.who && g.created.After(now.Add(-24*time.Hour)) {
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
	e := entry{Game: id, Seq: 1, At: now, Type: typeInvite, ClientID: clientID, Contact: contactID, Family: family,
		PeerTenantID: peer.TenantID, Mode: mode, InviterColor: colorName(color), Tips: tips}
	e.setActor(p.who)
	if peer.Kind == authz.ParticipantAdult {
		e.PeerGuardianID = peer.ID
	} else {
		e.PeerChildID = peer.ID
	}
	g, err := newGame(e)
	if err != nil {
		return View{}, err
	}
	if err := s.append(e); err != nil {
		return View{}, ErrUnavailable
	}
	s.games[id] = g
	s.remember(g, e)
	return s.view(g, p.who, now), nil
}

// Act performs one action. The second result is true when this client id
// was already stored for the same action; nothing is written then.
func (s *Store) Act(p Principal, gameID string, a Action) (View, bool, error) {
	return s.act(childActor(p), gameID, a)
}

// GuardianAct is Act for a guardian in a game with their own child.
func (s *Store) GuardianAct(g GuardianPrincipal, gameID string, a Action) (View, bool, error) {
	return s.act(guardianActor(g), gameID, a)
}

func (s *Store) act(p actor, gameID string, a Action) (View, bool, error) {
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
	if k, seen := s.keys[idempotencyKey{gameID, p.who, a.ClientID}]; seen {
		if k.fingerprint != fingerprint(a) {
			return View{}, false, ErrConflict
		}
		return s.view(g, p.who, now), true, nil
	}
	at := stamp(now, g.last)
	if err := g.check(p.who, a, at); err != nil {
		return View{}, false, err
	}
	e := entry{Game: gameID, Seq: g.seq + 1, At: at, Type: string(a.Kind), ClientID: a.ClientID, Move: a.Move, Ply: a.Ply, Level: a.Level}
	e.setActor(p.who)
	if a.Kind == Hint {
		e.Tip = computeTip(g.rules.Position(), a.Level)
	}
	if err := s.append(e); err != nil {
		return View{}, false, ErrUnavailable
	}
	g.apply(e)
	s.remember(g, e)
	return s.view(g, p.who, now), false, nil
}

// Game returns one game for one of its players.
func (s *Store) Game(p Principal, gameID string) (View, error) { return s.game(childActor(p), gameID) }

// GuardianGame is Game for a guardian.
func (s *Store) GuardianGame(g GuardianPrincipal, gameID string) (View, error) {
	return s.game(guardianActor(g), gameID)
}

func (s *Store) game(p actor, gameID string) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.authorizedGame(p, gameID)
	if !ok {
		return View{}, ErrForbidden
	}
	return s.view(g, p.who, s.now().UTC()), nil
}

// Latest returns the unfinished game of a scope, or else its most recent
// one. The second result is false when the scope has no stored game.
func (s *Store) Latest(p Principal, scope string) (View, bool, error) {
	return s.latest(childActor(p), scope)
}

// GuardianLatest is Latest for a guardian and one of their children.
func (s *Store) GuardianLatest(g GuardianPrincipal, childID string) (View, bool, error) {
	return s.latest(guardianActor(g), FamilyScope(g.TenantID, g.GuardianID, childID))
}

func (s *Store) latest(p actor, contactID string) (View, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, _, ok := s.allowedScope(p, contactID); !ok {
		return View{}, false, ErrForbidden
	}
	now := s.now().UTC()
	var best *game
	bestOpen := false
	for _, g := range s.games {
		if g.scope != contactID {
			continue
		}
		s.rate(g, now)
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
	return s.view(best, p.who, now), true, nil
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
		s.rate(g, now)
		if !s.exists(g) {
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
