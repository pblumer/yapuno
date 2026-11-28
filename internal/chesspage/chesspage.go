// Package chesspage is the child's chess page: board, clocks, status and
// the forms for every action, plus a small progressive-enhancement script.
// It knows no session, store or authorization; callers authorize and pass
// the game as the store returned it.
//
// The page works without JavaScript: a move is typed and posted with a form.
// The script adds tap-to-move on the board, a ticking clock and a reload
// when the game changes; it never decides anything the server decides.
package chesspage

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/yapuno/internal/chess"
	"github.com/pblumer/yapuno/internal/fontassets"
	"github.com/pblumer/yapuno/internal/games"
	"github.com/pblumer/yapuno/internal/skin"
)

// JS and UtilsJS are served by the child area under its own path.
//
//go:embed chess.js
var JS []byte

//go:embed chess-utils.js
var UtilsJS []byte

// Square is one board square as shown.
type Square struct {
	Name   string // "e4"
	Glyph  string
	Side   string // "w", "b" or ""
	Label  string // accessible name
	Light  bool
	Last   bool
	File   string // file letter on the bottom row, else ""
	Rank   string // rank number on the left column, else ""
	Holder bool   // the square holds a piece of the viewer
	Check  bool   // the king on this square is in check
	Hint   bool   // part of the move a tip suggests
}

// Clock is one side's clock.
type Clock struct {
	Name    string
	Text    string
	MS      int64
	Running bool
}

// ModeOption is one choice of the invitation form.
type ModeOption struct {
	Value, Label string
	Checked      bool
}

// Page is everything the chess page shows. Text is escaped by the template;
// PeerFigure MUST be trusted markup from appearance.FigureSVG.
type Page struct {
	Skin       string
	Peer       string
	PeerFigure template.HTML
	// Home is the back link; Path is "/child/play/<contact>"; Assets is the
	// prefix of the scripts.
	Home, Path, Assets string
	CSRF, ClientID     string
	Alert              string

	Status, Detail string
	ModeText       string
	ColorText      string
	Board          []Square
	LastMove       string
	Moves          []string
	Live           bool
	You, Them      Clock

	GameID string
	Ply    int
	Legal  string // space-separated UCI moves when the viewer may move

	CanAccept, CanWithdraw, CanMove, CanOfferDraw bool
	CanAnswerDraw, CanResign, CanAbort, CanInvite bool
	Modes                                         []ModeOption

	Sig      string
	PollMS   int
	MoveText string // the text a failed no-script move keeps

	Title, Subtitle, ResignText string
	// Tips: a level-1 tip explains (TipText), a level-2 tip adds a move.
	TipText, TipMove, TipsNote string
	CanTip, CanTipMove         bool
	InviteTips                 bool
	// Practice against the computer.
	Practice, CanStart, CanTakeback bool
	Levels, Colors                  []ModeOption
}

var pieceNames = map[chess.PieceType]string{chess.King: "König", chess.Queen: "Dame", chess.Rook: "Turm",
	chess.Bishop: "Läufer", chess.Knight: "Springer", chess.Pawn: "Bauer"}

// Solid glyphs for both sides, coloured by style, with the text
// presentation selector so no system draws an emoji.
var glyphs = map[chess.PieceType]string{chess.King: "♚︎", chess.Queen: "♛︎", chess.Rook: "♜︎",
	chess.Bishop: "♝︎", chess.Knight: "♞︎", chess.Pawn: "♟︎"}

// pieceLabel is "weisser Bauer", "schwarze Dame".
func pieceLabel(p chess.Piece) string {
	adj := "weiss"
	if p.Color == chess.Black {
		adj = "schwarz"
	}
	if p.Type == chess.Queen {
		return adj + "e " + pieceNames[p.Type]
	}
	return adj + "er " + pieceNames[p.Type]
}

func colorText(c chess.Color) string {
	if c == chess.Black {
		return "Schwarz"
	}
	return "Weiss"
}

var modeTexts = map[games.Mode]string{
	games.Correspondence: "Fernschach: ziehen, wann es passt",
	games.Live5:          "Live: 5 Minuten, plus 3 Sekunden pro Zug",
	games.Live10:         "Live: 10 Minuten, plus 5 Sekunden pro Zug",
	games.Live20:         "Live: 20 Minuten, plus 10 Sekunden pro Zug",
}

var modeOrder = []games.Mode{games.Correspondence, games.Live5, games.Live10, games.Live20}

// Signature changes whenever what the page shows changes, apart from the
// running clock. The script reloads the page when it differs.
func Signature(v *games.View) string {
	if v == nil {
		return "none"
	}
	return fmt.Sprintf("%s.%d.%d.%s", v.ID, v.Version, v.Phase, v.End)
}

// FormatClock writes a remaining time as m:ss, rounded down.
func FormatClock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int64(d / time.Second)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// Input is what Build needs. View is nil when there is no game yet.
type Input struct {
	View                         *games.View
	Peer                         string
	PeerFigure                   template.HTML
	Skin, Home, Path, Assets     string
	CSRF, ClientID, Alert, Moved string
	// Practice is set for a game against the computer; Peer is then the
	// computer's name as it starts a sentence.
	Practice *Practice
}

// Practice describes a game against the computer.
type Practice struct {
	Level       string
	CanTakeback bool
}

var levelTexts = map[string]string{
	"easy":   "Leicht: Der Computer macht auch Fehler",
	"medium": "Mittel: Der Computer schaut zwei Halbzüge voraus",
	"hard":   "Schwer: Der Computer schaut drei Halbzüge voraus",
}

var levelNames = map[string]string{"easy": "leicht", "medium": "mittel", "hard": "schwer"}

func options(values []string, labels map[string]string, selected string) []ModeOption {
	out := make([]ModeOption, 0, len(values))
	for _, v := range values {
		out = append(out, ModeOption{Value: v, Label: labels[v], Checked: v == selected})
	}
	return out
}

var possessive = map[chess.PieceType]string{chess.Queen: "Deine Dame", chess.King: "Dein König", chess.Rook: "Dein Turm",
	chess.Bishop: "Dein Läufer", chess.Knight: "Dein Springer", chess.Pawn: "Dein Bauer"}

var accusative = map[chess.PieceType]string{chess.Queen: "die Dame", chess.Rook: "den Turm", chess.Bishop: "den Läufer",
	chess.Knight: "den Springer", chess.Pawn: "den Bauern"}

// HintText explains a tip in words for a child. peer is the opponent's
// name as it starts a sentence.
func HintText(h chess.Hint, peer string) string {
	switch h.Kind {
	case chess.HintMateInOne:
		return "Du kannst mit einem einzigen Zug schachmatt setzen. Schau dir alle Züge an, die Schach geben!"
	case chess.HintCheck:
		return "Dein König steht im Schach. Du kannst ihn wegziehen, eine Figur dazwischenstellen oder den Angreifer schlagen."
	case chess.HintThreatMate:
		return "Achtung: " + peer + " droht, dich im nächsten Zug mattzusetzen. Wie kannst du das verhindern?"
	case chess.HintHanging:
		return possessive[h.Piece] + " auf " + h.Square.String() + " ist in Gefahr. Bring die Figur in Sicherheit oder schütze sie."
	case chess.HintWinPiece:
		return "Du kannst etwas gewinnen: Schau dir " + accusative[h.Piece] + " auf " + h.Square.String() + " an."
	case chess.HintDevelop:
		return "Bring deine Springer und Läufer ins Spiel, zum Beispiel " + accusative[h.Piece] + " auf " + h.Square.String() + ", und kämpfe um die Mitte."
	case chess.HintCastle:
		return "Bring deinen König mit einer Rochade in Sicherheit."
	}
	return "Gerade droht keine Gefahr. Suche eine Figur, die noch wenig zu tun hat, und gib ihr eine Aufgabe."
}

// tips fills the tip part of a page where the viewer is to move.
func tips(p *Page, v *games.View, peer string) {
	if !v.Tips {
		return
	}
	switch {
	case v.TipsLeft < 0:
		p.TipsNote = "Tipps: so viele du willst."
	default:
		p.TipsNote = fmt.Sprintf("Tipps: noch %d von %d.", v.TipsLeft, games.MaxTips)
		if v.TheirTipsUsed > 0 {
			p.TipsNote += fmt.Sprintf(" %s hat %d genutzt.", peer, v.TheirTipsUsed)
		}
	}
	if !p.CanMove {
		return
	}
	if v.Tip == nil {
		p.CanTip = v.TipsLeft != 0
		return
	}
	p.TipText = HintText(v.Tip.Hint, peer)
	if v.Tip.Level == 1 {
		p.CanTipMove = true
		return
	}
	if v.Position.Legal(v.Tip.Best) {
		p.TipMove = "Ein guter Zug wäre: " + Describe(v.Position, v.Tip.Best) + "."
		for i := range p.Board {
			if p.Board[i].Name == v.Tip.Best.From.String() || p.Board[i].Name == v.Tip.Best.To.String() {
				p.Board[i].Hint = true
				p.Board[i].Label += ", Tipp"
			}
		}
	}
}

// Build turns a game, as the viewer sees it, into the page.
func Build(in Input) Page {
	p := Page{Skin: in.Skin, Peer: in.Peer, PeerFigure: in.PeerFigure, Home: in.Home, Path: in.Path, Assets: in.Assets,
		CSRF: in.CSRF, ClientID: in.ClientID, Alert: in.Alert, MoveText: in.Moved, Sig: Signature(in.View), PollMS: 4000,
		Title: "Schach mit " + in.Peer, Subtitle: "Nur ihr zwei. Im Spiel gibt es keinen Chat.",
		ResignText: "Wenn du aufgibst, hat " + in.Peer + " gewonnen."}
	v := in.View
	if in.Practice != nil {
		return buildPractice(p, in)
	}
	if v == nil {
		p.Status = "Ihr habt noch nie zusammen gespielt."
		p.Detail = "Lade " + in.Peer + " zu einer Partie ein."
		p.CanInvite = true
		p.Board = board(chess.NewGame().Position(), chess.White, nil)
		p.Modes = modes(games.Correspondence)
		return p
	}
	p.GameID, p.Ply, p.ModeText, p.ColorText = v.ID, len(v.Moves), modeTexts[v.Mode], colorText(v.YourColor)
	p.Board = board(v.Position, v.YourColor, v.Moves)
	p.LastMove, p.Moves = history(v, in.Peer)
	if _, _, live := v.Mode.Clock(); live && v.Phase != games.Invited {
		p.Live = true
		you, them := v.White, v.Black
		if v.YourColor == chess.Black {
			you, them = them, you
		}
		p.You = Clock{Name: "Du", Text: FormatClock(you), MS: you.Milliseconds(), Running: v.Running == v.YourColor}
		p.Them = Clock{Name: in.Peer, Text: FormatClock(them), MS: them.Milliseconds(), Running: v.Running == v.YourColor.Other()}
	}
	switch v.Phase {
	case games.Invited:
		if v.YouInvited {
			p.Status = "Du hast " + in.Peer + " eingeladen."
			p.Detail = "Warte, bis " + in.Peer + " annimmt. " + p.ModeText + "."
			p.CanWithdraw = true
		} else {
			p.Status = in.Peer + " möchte mit dir Schach spielen."
			p.Detail = p.ModeText + ". Du spielst " + p.ColorText + "."
			p.CanAccept = true
		}
	case games.Running:
		p.CanResign = true
		p.CanAbort = len(v.Moves) < 2
		p.CanOfferDraw = v.DrawOffer == chess.NoColor
		p.CanAnswerDraw = v.DrawOffer == v.YourColor.Other()
		if v.YourTurn() {
			p.Status = "Du bist am Zug."
			if v.Position.InCheck() {
				p.Status = "Du bist am Zug. Dein König steht im Schach!"
			}
			p.CanMove = true
			legal := v.Position.LegalMoves()
			moves := make([]string, len(legal))
			for i, m := range legal {
				moves[i] = m.String()
			}
			p.Legal = strings.Join(moves, " ")
		} else {
			p.Status = in.Peer + " ist am Zug."
		}
		switch v.DrawOffer {
		case v.YourColor:
			p.Detail = "Du hast ein Remis angeboten."
		case v.YourColor.Other():
			p.Detail = in.Peer + " bietet ein Remis an."
		default:
			p.Detail = "Du spielst " + p.ColorText + ". " + p.ModeText + "."
		}
		if p.Live && !v.YourTurn() {
			p.PollMS = 1000
		}
	case games.Finished:
		p.Status, p.Detail = endText(v, in.Peer), "Möchtest du nochmals spielen?"
		p.CanInvite = true
		p.Modes = modes(v.Mode)
		p.InviteTips = v.Tips
	}
	tips(&p, v, in.Peer)
	return p
}

// buildPractice builds the page of a game against the computer.
func buildPractice(p Page, in Input) Page {
	p.Practice, p.PollMS, p.Sig = true, 0, ""
	p.Title, p.Subtitle = "Üben gegen den Computer", "Nur du und der Computer. Hier darfst du alles ausprobieren."
	p.ResignText = "Wenn du aufgibst, gewinnt der Computer."
	level := in.Practice.Level
	p.Levels = options([]string{"easy", "medium", "hard"}, levelTexts, map[bool]string{true: level, false: "easy"}[level != ""])
	p.Colors = options([]string{"white", "black", "random"}, map[string]string{"white": "Weiss", "black": "Schwarz", "random": "Zufall"}, "white")
	v := in.View
	if v == nil {
		p.Status, p.Detail = "Spiel gegen den Computer.", "Wähle, wie stark der Computer spielen soll, und deine Farbe."
		p.CanStart = true
		p.Board = board(chess.NewGame().Position(), chess.White, nil)
		return p
	}
	p.Ply, p.ColorText = len(v.Moves), colorText(v.YourColor)
	p.Board = board(v.Position, v.YourColor, v.Moves)
	p.LastMove, p.Moves = history(v, in.Peer)
	p.CanTakeback = in.Practice.CanTakeback
	if v.Phase == games.Finished {
		p.Status, p.Detail = endText(v, in.Peer), "Nochmals? Du kannst auch den letzten Zug zurücknehmen."
		if !p.CanTakeback {
			p.Detail = "Möchtest du nochmals spielen?"
		}
		p.CanStart = true
		return p
	}
	p.CanResign = true
	p.Detail = "Du spielst " + p.ColorText + ". Stufe: " + levelNames[level] + "."
	if v.YourTurn() {
		p.Status = "Du bist am Zug."
		if v.Position.InCheck() {
			p.Status = "Du bist am Zug. Dein König steht im Schach!"
		}
		p.CanMove = true
		legal := v.Position.LegalMoves()
		moves := make([]string, len(legal))
		for i, m := range legal {
			moves[i] = m.String()
		}
		p.Legal = strings.Join(moves, " ")
	}
	tips(&p, v, in.Peer)
	return p
}

func modes(selected games.Mode) []ModeOption {
	out := make([]ModeOption, 0, len(modeOrder))
	for _, m := range modeOrder {
		out = append(out, ModeOption{Value: string(m), Label: modeTexts[m], Checked: m == selected})
	}
	return out
}

func endText(v *games.View, peer string) string {
	won := v.Winner != chess.NoColor && v.Winner == v.YourColor
	lost := v.Winner != chess.NoColor && v.Winner != v.YourColor
	switch v.End {
	case games.EndCheckmate:
		if won {
			return "Schachmatt! Du hast gewonnen."
		}
		return "Schachmatt. " + peer + " hat gewonnen."
	case games.EndStalemate:
		return "Patt. Die Partie ist unentschieden."
	case games.EndInsufficient:
		return "Unentschieden: Niemand kann mehr mattsetzen."
	case games.EndRepetition:
		return "Unentschieden: Dieselbe Stellung kam dreimal vor."
	case games.EndFiftyMoves:
		return "Unentschieden: 50 Züge lang wurde nichts geschlagen und kein Bauer gezogen."
	case games.EndResigned:
		if won {
			return peer + " hat aufgegeben. Du hast gewonnen."
		}
		return "Du hast aufgegeben. " + peer + " hat gewonnen."
	case games.EndAgreed:
		return "Ihr habt euch auf Remis geeinigt. Unentschieden."
	case games.EndTimeout:
		switch {
		case won:
			return "Die Zeit von " + peer + " ist abgelaufen. Du hast gewonnen."
		case lost:
			return "Deine Zeit ist abgelaufen. " + peer + " hat gewonnen."
		}
		return "Die Zeit ist abgelaufen, aber es kann niemand mehr gewinnen. Unentschieden."
	case games.EndDeclined:
		if v.YouInvited {
			return peer + " hat die Einladung abgelehnt."
		}
		return "Du hast die Einladung abgelehnt."
	case games.EndWithdrawn:
		return "Die Einladung wurde zurückgezogen."
	case games.EndExpired:
		return "Die Einladung ist abgelaufen."
	case games.EndAbandoned:
		return "Die Partie ist eingeschlafen: 30 Tage lang hat niemand gezogen."
	case games.EndAborted:
		return "Die Partie wurde abgebrochen."
	}
	return "Die Partie ist zu Ende."
}

// board lists the squares from the viewer's side: the own pieces at the
// bottom.
func board(pos chess.Position, viewer chess.Color, moves []chess.Move) []Square {
	var last chess.Move
	hasLast := len(moves) > 0
	if hasLast {
		last = moves[len(moves)-1]
	}
	out := make([]Square, 0, 64)
	for row := 0; row < 8; row++ {
		for col := 0; col < 8; col++ {
			file, rank := col, 7-row
			if viewer == chess.Black {
				file, rank = 7-col, row
			}
			s := chess.Square(rank*8 + file)
			pc := pos.PieceAt(s)
			sq := Square{Name: s.String(), Light: (file+rank)%2 == 1, Last: hasLast && (s == last.From || s == last.To)}
			if row == 7 {
				sq.File = string(rune('a' + file))
			}
			if col == 0 {
				sq.Rank = string(rune('1' + rank))
			}
			sq.Label = sq.Name + ", leer"
			if pc.Type != chess.NoPiece {
				sq.Glyph, sq.Label = glyphs[pc.Type], sq.Name+", "+pieceLabel(pc)
				sq.Side = "w"
				if pc.Color == chess.Black {
					sq.Side = "b"
				}
				sq.Holder = pc.Color == viewer
			}
			if pc.Type == chess.King && pc.Color == pos.Turn() && pos.InCheck() {
				sq.Check = true
				sq.Label += ", steht im Schach"
			}
			if sq.Last {
				sq.Label += ", letzter Zug"
			}
			out = append(out, sq)
		}
	}
	return out
}

// history replays the moves for a description of the last one and a
// numbered list of all of them.
func history(v *games.View, peer string) (string, []string) {
	if len(v.Moves) == 0 {
		return "", nil
	}
	g := chess.NewGame()
	var before chess.Position
	var list []string
	for i, m := range v.Moves {
		before = g.Position()
		if g.Play(m) != nil {
			return "", nil
		}
		text := m.From.String() + "–" + m.To.String()
		if i%2 == 0 {
			list = append(list, fmt.Sprintf("%d. %s", i/2+1, text))
		} else {
			list[len(list)-1] += "  " + text
		}
	}
	m := v.Moves[len(v.Moves)-1]
	who := "Du"
	if before.Turn() != v.YourColor {
		who = peer
	}
	return who + ": " + Describe(before, m), list
}

// Describe says in words what a legal move does in the position before it.
func Describe(before chess.Position, m chess.Move) string {
	pc := before.PieceAt(m.From)
	captured := before.PieceAt(m.To)
	var text string
	switch {
	case pc.Type == chess.King && m.To-m.From == 2:
		text = "kurze Rochade"
	case pc.Type == chess.King && m.From-m.To == 2:
		text = "lange Rochade"
	default:
		text = pieceNames[pc.Type] + " " + m.From.String() + " nach " + m.To.String()
		switch {
		case captured.Type != chess.NoPiece:
			text += ", schlägt " + pieceNames[captured.Type]
		case pc.Type == chess.Pawn && m.From.File() != m.To.File():
			text += ", schlägt Bauer en passant"
		}
		if m.Promotion != chess.NoPiece {
			text += ", wird " + pieceNames[m.Promotion]
		}
	}
	if after, err := before.Play(m); err == nil && after.InCheck() {
		if len(after.LegalMoves()) == 0 {
			text += ", schachmatt"
		} else {
			text += ", Schach"
		}
	}
	return text
}

// Render writes the page. It writes nothing when it returns an error.
func Render(w http.ResponseWriter, status int, p Page) error {
	catalog := skin.Catalog()
	def := skin.Resolve(p.Skin, catalog, catalog["classic"])
	tokens, err := skin.CSSVariables(def)
	if err != nil {
		return err
	}
	decls := strings.TrimSuffix(strings.TrimPrefix(tokens, ":root{"), "}")
	var out bytes.Buffer
	if err := page.Execute(&out, struct {
		Page
		Skin string
		// Validated first-party skin tokens and fixed component rules only.
		CSS template.CSS
	}{Page: p, Skin: def.ID, CSS: template.CSS(fontassets.FaceCSS(def.Typography.Heading, def.Typography.Body) + ".yp-play{" + decls + "}\n" + pageCSS)}); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(out.Bytes())
	return nil
}

// The board spans the full width on small phones: 8 squares cannot reach
// 44 CSS px below a 352 px viewport (MFE exception in the chess ADR).
const pageCSS = `body{margin:0}
.yp-play{box-sizing:border-box;min-height:100vh;background-color:var(--yp-bg);background-image:var(--yp-wallpaper);background-size:var(--yp-wallpaper-size);color:var(--yp-text);font-family:var(--yp-font-body);font-size:var(--yp-font-size);line-height:1.4;padding-bottom:max(1.5rem,env(safe-area-inset-bottom))}
.yp-play *{box-sizing:border-box}
.yp-sr{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap}
.yp-head{display:flex;align-items:center;gap:.5rem;padding:.6rem;padding-top:max(.6rem,env(safe-area-inset-top));background:var(--yp-surface);border-bottom:1px solid var(--yp-line)}
.yp-back{flex:none;display:grid;place-items:center;min-width:44px;min-height:44px;border-radius:var(--yp-radius);color:var(--yp-accent-text);font-weight:800;text-decoration:none}
.yp-head svg{flex:none}
.yp-head h1{font-family:var(--yp-font-heading);font-size:1.15rem;margin:0;overflow-wrap:anywhere}
.yp-head p{margin:0;font-size:.8rem;color:var(--yp-text-muted)}
.yp-wrap{max-width:30rem;margin:0 auto}
.yp-pad{padding:0 1rem}
.yp-status{margin:.9rem 0 .2rem;font-weight:800;font-size:1.1rem}
.yp-detail{margin:0 0 .8rem;color:var(--yp-text-muted)}
.yp-alert{margin:.8rem 1rem;padding:.7rem .9rem;border-radius:var(--yp-radius);background:var(--yp-surface);border:2px solid var(--yp-danger-text);font-weight:700}
.yp-clock{display:flex;justify-content:space-between;align-items:center;margin:.35rem 1rem;padding:.4rem .8rem;border-radius:var(--yp-radius);background:var(--yp-surface);border:2px solid var(--yp-line);font-weight:700}
.yp-clock time{font-variant-numeric:tabular-nums;font-size:1.25rem}
.yp-clock.yp-running{border-color:var(--yp-accent);box-shadow:inset 0 0 0 2px var(--yp-accent)}
.yp-clock.yp-low time{color:var(--yp-danger-text)}
.yp-board{display:grid;grid-template-columns:repeat(8,1fr);width:min(100vw,30rem);aspect-ratio:1/1;margin:.4rem auto;border:2px solid #5b4636;touch-action:manipulation}
.yp-play .yp-sq{position:relative;display:grid;place-items:center;min-width:0;min-height:0;aspect-ratio:1/1;margin:0;padding:0;border:0;border-radius:0;background-color:#b58863;font-size:min(9vw,2.7rem);line-height:1;cursor:pointer;-webkit-tap-highlight-color:transparent}
.yp-play .yp-sq.yp-light{background-color:#f0d9b5}
.yp-sq.yp-last{box-shadow:inset 0 0 0 3px rgba(20,85,160,.75)}
.yp-sq.yp-check{box-shadow:inset 0 0 0 5px #c62828;background-image:radial-gradient(circle,rgba(198,40,40,.55) 0,rgba(198,40,40,0) 70%)}
.yp-sq.yp-hint{box-shadow:inset 0 0 0 4px #2e7d32}
.yp-tip{margin:.6rem 0;padding:.7rem .9rem;border-radius:var(--yp-radius);background:var(--yp-surface);border:2px solid #2e7d32}
.yp-tip p{margin:.2rem 0}
.yp-sq.yp-selected{box-shadow:inset 0 0 0 5px #1455a0}
.yp-sq.yp-target::after{content:"";position:absolute;width:30%;height:30%;border-radius:50%;background:rgba(20,85,160,.55)}
.yp-sq.yp-target.yp-capture::after{width:86%;height:86%;background:transparent;border:4px solid rgba(20,85,160,.7)}
.yp-sq:focus-visible{outline:3px solid var(--yp-focus);outline-offset:-3px;z-index:1}
.yp-sq:disabled{cursor:default}
.yp-piece{pointer-events:none;font-family:"DejaVu Sans","Segoe UI Symbol","Noto Sans Symbols 2","Apple Symbols",sans-serif}
.yp-w .yp-piece{color:#fff;text-shadow:0 0 1px #000,0 0 1px #000,1px 1px 0 #000,-1px -1px 0 #000,1px -1px 0 #000,-1px 1px 0 #000}
.yp-b .yp-piece{color:#111;text-shadow:0 0 1px #fff,1px 1px 0 rgba(255,255,255,.55)}
.yp-file,.yp-rank{position:absolute;font-family:var(--yp-font-body);font-size:.62rem;font-weight:700;color:#5b4636;pointer-events:none}
.yp-light .yp-file,.yp-light .yp-rank{color:#8a6a4a}
.yp-file{right:3px;bottom:1px}
.yp-rank{left:3px;top:1px}
.yp-last-move{margin:.4rem 0;font-weight:600}
.yp-actions{display:flex;flex-wrap:wrap;gap:.5rem;margin:.8rem 0}
.yp-actions form{margin:0}
.yp-play button,.yp-play .yp-button{min-height:48px;padding:.6rem 1rem;border:2px solid var(--yp-text-muted);border-radius:var(--yp-radius);background:var(--yp-bg);color:var(--yp-text);font:inherit;font-weight:700;cursor:pointer}
.yp-play button.yp-primary{border-color:var(--yp-accent);background:var(--yp-accent);color:var(--yp-on-accent)}
.yp-play button.yp-danger{border-color:var(--yp-danger-text);color:var(--yp-danger-text)}
.yp-play details{margin:.8rem 0;padding:.6rem .8rem;border-radius:var(--yp-radius);background:var(--yp-surface)}
.yp-play summary{min-height:44px;display:flex;align-items:center;font-weight:700;cursor:pointer}
.yp-play label{display:block;font-weight:700;margin:.4rem 0}
.yp-play input[type=text]{width:100%;min-height:48px;padding:.6rem .8rem;border:2px solid var(--yp-text-muted);border-radius:var(--yp-radius);background:var(--yp-bg);color:var(--yp-text);font:inherit;font-size:1.05rem}
.yp-modes{border:0;margin:0;padding:0}
.yp-choice{display:flex;align-items:center;gap:.6rem;min-height:48px;margin:.3rem 0;padding:.3rem .6rem;border:2px solid var(--yp-line);border-radius:var(--yp-radius);background:var(--yp-bg);font-weight:600}
.yp-choice input{width:1.3rem;height:1.3rem;flex:none}
.yp-promo{display:flex;flex-wrap:wrap;gap:.5rem;margin:.6rem 0}
.yp-promo[hidden]{display:none}
.yp-moves{margin:.4rem 0;padding-left:0;list-style:none;font-variant-numeric:tabular-nums;columns:2;white-space:pre}
.yp-note{color:var(--yp-text-muted);font-size:.9rem}
.yp-play button:focus-visible,.yp-back:focus-visible,.yp-play summary:focus-visible,.yp-play input:focus-visible{outline:3px solid var(--yp-focus);outline-offset:2px}
@media (prefers-reduced-motion:reduce){.yp-play *{transition:none!important}}
`

var page = template.Must(template.New("chess").Funcs(template.FuncMap{"args": func(p Page, kind, label, class string) actArgs {
	return actArgs{Page: p, Kind: kind, Label: label, Class: class}
}}).Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><meta name="referrer" content="no-referrer"><title>Yapuno – {{.Title}}</title><style>{{.CSS}}</style></head><body>
<main class="yp-play" id="yp-play" data-yp-skin="{{.Skin}}" data-sig="{{.Sig}}" data-state="{{.Path}}/state" data-home="{{.Home}}" data-poll="{{.PollMS}}">
<header class="yp-head"><a class="yp-back" href="{{.Home}}" aria-label="Zurück zu deiner Crew">←</a>{{.PeerFigure}}<div><h1>{{.Title}}</h1><p>{{.Subtitle}}</p></div></header>
<div class="yp-wrap">
{{if .Alert}}<p class="yp-alert" role="alert">{{.Alert}}</p>{{end}}
<div class="yp-pad" aria-live="polite"><p class="yp-status" id="yp-status">{{.Status}}</p><p class="yp-detail" id="yp-detail">{{.Detail}}</p></div>
{{if .Live}}<p class="yp-clock{{if .Them.Running}} yp-running{{end}}" id="yp-clock-them" data-ms="{{.Them.MS}}" data-running="{{.Them.Running}}"><span>{{.Them.Name}}</span><time>{{.Them.Text}}</time></p>{{end}}
<div class="yp-board" id="yp-board" role="group" aria-label="Schachbrett{{if .ColorText}}, du spielst {{.ColorText}}{{end}}" data-legal="{{.Legal}}">{{range .Board}}<button type="button" class="yp-sq{{if .Light}} yp-light{{end}}{{if .Last}} yp-last{{end}}{{if .Side}} yp-{{.Side}}{{end}}{{if .Check}} yp-check{{end}}{{if .Hint}} yp-hint{{end}}" data-square="{{.Name}}" aria-label="{{.Label}}" disabled>{{if .Glyph}}<span class="yp-piece" aria-hidden="true">{{.Glyph}}</span>{{end}}{{if .Rank}}<span class="yp-rank" aria-hidden="true">{{.Rank}}</span>{{end}}{{if .File}}<span class="yp-file" aria-hidden="true">{{.File}}</span>{{end}}</button>{{end}}</div>
{{if .Live}}<p class="yp-clock{{if .You.Running}} yp-running{{end}}" id="yp-clock-you" data-ms="{{.You.MS}}" data-running="{{.You.Running}}"><span>{{.You.Name}}</span><time>{{.You.Text}}</time></p>{{end}}
<div class="yp-pad">
<p class="yp-sr" id="yp-announce" aria-live="assertive"></p>
{{if .LastMove}}<p class="yp-last-move">{{.LastMove}}</p>{{end}}
{{if .TipText}}<div class="yp-tip" role="status"><p><strong>Tipp:</strong> {{.TipText}}</p>{{if .TipMove}}<p>{{.TipMove}}</p>{{end}}</div>{{end}}
{{if or .CanTip .CanTipMove}}<div class="yp-actions">{{if .CanTip}}{{template "tip" (args .Page "1" "Tipp" "")}}{{end}}{{if .CanTipMove}}{{template "tip" (args .Page "2" "Zeig mir einen Zug" "")}}{{end}}</div>{{end}}
{{if .TipsNote}}<p class="yp-note">{{.TipsNote}}</p>{{end}}
{{if .CanMove}}<div class="yp-promo" id="yp-promo" hidden><p class="yp-sr">Der Bauer wird zu:</p><button type="button" data-promo="q">Dame</button><button type="button" data-promo="r">Turm</button><button type="button" data-promo="b">Läufer</button><button type="button" data-promo="n">Springer</button><button type="button" data-promo="">Abbrechen</button></div>
<details id="yp-type"{{if .MoveText}} open{{end}}><summary>Zug eintippen</summary><form method="post" action="{{.Path}}/act" id="yp-move-form"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="client_id" value="{{.ClientID}}"><input type="hidden" name="game" value="{{.GameID}}"><input type="hidden" name="kind" value="move"><input type="hidden" name="ply" value="{{.Ply}}"><label for="yp-move">Von welchem Feld auf welches? Zum Beispiel e2e4</label><input type="text" id="yp-move" name="move" value="{{.MoveText}}" required maxlength="5" pattern="[a-h][1-8][a-h][1-8][qrbn]?" autocomplete="off" autocapitalize="none" spellcheck="false" enterkeyhint="go"><p class="yp-note">Wird ein Bauer zur Dame, hänge q an (Turm r, Läufer b, Springer n), zum Beispiel e7e8q.</p><button class="yp-primary" type="submit">Ziehen</button></form></details>{{end}}
{{if or .CanAccept .CanWithdraw .CanAnswerDraw .CanOfferDraw .CanAbort .CanTakeback}}<div class="yp-actions">
{{if .CanTakeback}}{{template "ply" (args .Page "takeback" "Zug zurücknehmen" "")}}{{end}}
{{if .CanAccept}}{{template "act" (args .Page "accept" "Annehmen" "yp-primary")}}{{template "act" (args .Page "decline" "Ablehnen" "")}}{{end}}
{{if .CanWithdraw}}{{template "act" (args .Page "withdraw" "Einladung zurückziehen" "")}}{{end}}
{{if .CanAnswerDraw}}{{template "act" (args .Page "accept_draw" "Remis annehmen" "yp-primary")}}{{template "act" (args .Page "decline_draw" "Remis ablehnen" "")}}{{end}}
{{if and .CanOfferDraw (not .CanAnswerDraw) (not .Practice)}}{{template "act" (args .Page "offer_draw" "Remis anbieten" "")}}{{end}}
{{if and .CanAbort (not .Practice)}}{{template "act" (args .Page "abort" "Partie abbrechen" "")}}{{end}}
</div>{{end}}
{{if .CanResign}}<details><summary>Aufgeben</summary><p>{{.ResignText}}</p>{{template "act" (args .Page "resign" "Ja, ich gebe auf" "yp-danger")}}</details>{{end}}
{{if .CanInvite}}<form method="post" action="{{.Path}}/invite"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="client_id" value="{{.ClientID}}"><fieldset class="yp-modes"><legend class="yp-status">Wie wollt ihr spielen?</legend>{{range .Modes}}<label class="yp-choice"><input type="radio" name="mode" value="{{.Value}}"{{if .Checked}} checked{{end}} required> <span>{{.Label}}</span></label>{{end}}</fieldset><label class="yp-choice"><input type="checkbox" name="tips" value="yes"{{if .InviteTips}} checked{{end}}> <span>Mit Tipps: Jedes Kind darf 3 Tipps pro Partie nehmen. Ihr seht beide, wer wie viele genutzt hat.</span></label><p class="yp-note">Wer Weiss spielt, entscheidet der Zufall.</p><p><button class="yp-primary" type="submit">{{.Peer}} einladen</button></p></form>{{end}}
{{if .CanStart}}<form method="post" action="{{.Path}}/start"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="client_id" value="{{.ClientID}}"><fieldset class="yp-modes"><legend class="yp-status">Wie stark soll der Computer spielen?</legend>{{range .Levels}}<label class="yp-choice"><input type="radio" name="level" value="{{.Value}}"{{if .Checked}} checked{{end}} required> <span>{{.Label}}</span></label>{{end}}</fieldset><fieldset class="yp-modes"><legend class="yp-status">Welche Farbe willst du?</legend>{{range .Colors}}<label class="yp-choice"><input type="radio" name="color" value="{{.Value}}"{{if .Checked}} checked{{end}} required> <span>{{.Label}}</span></label>{{end}}</fieldset><p><button class="yp-primary" type="submit">Neue Partie</button></p></form>{{end}}
{{if .Moves}}<details><summary>Alle Züge</summary><ol class="yp-moves">{{range .Moves}}<li>{{.}}</li>{{end}}</ol></details>{{end}}
<p class="yp-note">Das hier ist eine Vorschau.</p>
</div></div></main>
<script src="{{.Assets}}/chess-utils.js" defer></script>
<script src="{{.Assets}}/chess.js" defer></script>
</body></html>{{define "act"}}<form method="post" action="{{.Page.Path}}/act"><input type="hidden" name="csrf_token" value="{{.Page.CSRF}}"><input type="hidden" name="client_id" value="{{.Page.ClientID}}"><input type="hidden" name="game" value="{{.Page.GameID}}"><input type="hidden" name="kind" value="{{.Kind}}"><button type="submit"{{if .Class}} class="{{.Class}}"{{end}}>{{.Label}}</button></form>{{end}}{{define "ply"}}<form method="post" action="{{.Page.Path}}/act"><input type="hidden" name="csrf_token" value="{{.Page.CSRF}}"><input type="hidden" name="client_id" value="{{.Page.ClientID}}"><input type="hidden" name="game" value="{{.Page.GameID}}"><input type="hidden" name="kind" value="{{.Kind}}"><input type="hidden" name="ply" value="{{.Page.Ply}}"><button type="submit">{{.Label}}</button></form>{{end}}{{define "tip"}}<form method="post" action="{{.Page.Path}}/act"><input type="hidden" name="csrf_token" value="{{.Page.CSRF}}"><input type="hidden" name="client_id" value="{{.Page.ClientID}}"><input type="hidden" name="game" value="{{.Page.GameID}}"><input type="hidden" name="kind" value="hint"><input type="hidden" name="level" value="{{.Kind}}"><input type="hidden" name="ply" value="{{.Page.Ply}}"><button type="submit">{{.Label}}</button></form>{{end}}`))

type actArgs struct {
	Page               Page
	Kind, Label, Class string
}
