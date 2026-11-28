package chesspage

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/chess"
	"github.com/pblumer/yapuno/internal/games"
)

func view(t *testing.T, mode games.Mode, color chess.Color, moves ...string) *games.View {
	t.Helper()
	g := chess.NewGame()
	var played []chess.Move
	for _, s := range moves {
		m, err := chess.ParseUCI(s)
		if err != nil || g.Play(m) != nil {
			t.Fatalf("move %s", s)
		}
		played = append(played, m)
	}
	return &games.View{ID: strings.Repeat("a", 32), Contact: strings.Repeat("c", 32), Mode: mode, Phase: games.Running,
		YourColor: color, Position: g.Position(), Moves: played, Version: uint64(2 + len(moves))}
}

func render(t *testing.T, p Page) string {
	t.Helper()
	w := httptest.NewRecorder()
	if err := Render(w, 200, p); err != nil {
		t.Fatal(err)
	}
	return w.Body.String()
}

func input(v *games.View) Input {
	return Input{View: v, Peer: "Mia", Home: "/child", Path: "/child/play/" + strings.Repeat("c", 32), Assets: "/child", CSRF: "csrf-token", ClientID: "client-id-0000000001"}
}

func TestBoardFromEachSide(t *testing.T) {
	white := Build(input(view(t, games.Correspondence, chess.White)))
	if white.Board[0].Name != "a8" || white.Board[63].Name != "h1" || white.Board[56].File != "a" || white.Board[0].Rank != "8" {
		t.Fatalf("white orientation: %s %s", white.Board[0].Name, white.Board[63].Name)
	}
	black := Build(input(view(t, games.Correspondence, chess.Black)))
	if black.Board[0].Name != "h1" || black.Board[63].Name != "a8" || black.Board[56].File != "h" || black.Board[0].Rank != "1" {
		t.Fatalf("black orientation: %s %s", black.Board[0].Name, black.Board[63].Name)
	}
	if sq := white.Board[60]; sq.Name != "e1" || sq.Label != "e1, weisser König" || sq.Light || !sq.Holder {
		t.Fatalf("e1: %+v", sq)
	}
	if sq := white.Board[3]; sq.Name != "d8" || sq.Label != "d8, schwarze Dame" || sq.Light || sq.Holder || sq.Side != "b" {
		t.Fatalf("d8: %+v", sq)
	}
	if sq := white.Board[36]; sq.Name != "e4" || sq.Label != "e4, leer" || sq.Glyph != "" {
		t.Fatalf("e4: %+v", sq)
	}
}

func TestStatesAndActions(t *testing.T) {
	v := view(t, games.Correspondence, chess.White)
	p := Build(input(v))
	if !p.CanMove || p.Status != "Du bist am Zug." || !strings.Contains(p.Legal, "e2e4") || strings.Count(p.Legal, " ") != 19 || !p.CanAbort || !p.CanResign || p.CanInvite {
		t.Fatalf("own turn: %+v", p)
	}
	v = view(t, games.Live5, chess.Black, "e2e4", "e7e5", "g1f3")
	v.White, v.Black, v.Running = 4*time.Minute+53*time.Second, 5*time.Minute, chess.Black
	p = Build(input(v))
	if !p.CanMove || p.CanAbort || !p.Live || p.You.Text != "5:00" || !p.You.Running || p.Them.Text != "4:53" || p.Them.Running {
		t.Fatalf("live black: %+v", p)
	}
	if p.LastMove != "Mia: Springer g1 nach f3" || len(p.Moves) != 2 || p.Moves[1] != "2. g1–f3" {
		t.Fatalf("history: %q %q", p.LastMove, p.Moves)
	}
	v = view(t, games.Live5, chess.White, "e2e4", "e7e5", "g1f3")
	p = Build(input(v))
	if p.CanMove || p.Legal != "" || p.Status != "Mia ist am Zug." || p.PollMS != 1000 {
		t.Fatalf("opponent's live turn: %+v", p)
	}
	v.DrawOffer = chess.Black
	if p := Build(input(v)); !p.CanAnswerDraw || p.Detail != "Mia bietet ein Remis an." {
		t.Fatalf("draw offer: %+v", p)
	}
	v = view(t, games.Correspondence, chess.White)
	v.Phase, v.YouInvited = games.Invited, false
	if p := Build(input(v)); !p.CanAccept || p.CanMove || p.CanResign || !strings.Contains(p.Status, "Mia möchte") {
		t.Fatalf("invited: %+v", p)
	}
	v.YouInvited = true
	if p := Build(input(v)); p.CanAccept || !p.CanWithdraw {
		t.Fatalf("inviter: %+v", p)
	}
	if p := Build(input(nil)); !p.CanInvite || p.GameID != "" || len(p.Modes) != 4 || p.Sig != "none" {
		t.Fatalf("no game: %+v", p)
	}
}

func TestCheckAndMateAreShown(t *testing.T) {
	v := view(t, games.Correspondence, chess.Black, "e2e4", "d7d6", "f1b5")
	p := Build(input(v))
	if p.Status != "Du bist am Zug. Dein König steht im Schach!" || p.LastMove != "Mia: Läufer f1 nach b5, Schach" {
		t.Fatalf("check: %q %q", p.Status, p.LastMove)
	}
	var king Square
	for _, sq := range p.Board {
		if sq.Name == "e8" {
			king = sq
		}
	}
	if !king.Check || king.Label != "e8, schwarzer König, steht im Schach" {
		t.Fatalf("king square: %+v", king)
	}
	v = view(t, games.Correspondence, chess.White, "f2f3", "e7e5", "g2g4", "d8h4")
	v.Phase, v.End, v.Winner = games.Finished, games.EndCheckmate, chess.Black
	p = Build(input(v))
	if p.Status != "Schachmatt. Mia hat gewonnen." || p.LastMove != "Mia: Dame d8 nach h4, schachmatt" || p.CanMove || !p.CanInvite {
		t.Fatalf("mate: %q %q", p.Status, p.LastMove)
	}
	v.YourColor = chess.Black
	if p := Build(input(v)); p.Status != "Schachmatt! Du hast gewonnen." {
		t.Fatalf("winner: %q", p.Status)
	}
}

func TestDescribeSpecialMoves(t *testing.T) {
	cases := map[string]struct{ fen, move, want string }{
		"castle":     {"r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 0 1", "e1g1", "kurze Rochade"},
		"long":       {"r3k2r/8/8/8/8/8/8/R3K2R b KQkq - 0 1", "e8c8", "lange Rochade"},
		"en passant": {"4k3/8/8/3pP3/8/8/8/4K3 w - d6 0 1", "e5d6", "Bauer e5 nach d6, schlägt Bauer en passant"},
		"promotion":  {"1n2k3/P7/8/8/8/8/8/4K3 w - - 0 1", "a7b8q", "Bauer a7 nach b8, schlägt Springer, wird Dame, Schach"},
	}
	for name, c := range cases {
		pos, err := chess.ParseFEN(c.fen)
		if err != nil {
			t.Fatal(err)
		}
		m, _ := chess.ParseUCI(c.move)
		if got := Describe(pos, m); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

func TestRenderEscapesAndCarriesNoIdentifiers(t *testing.T) {
	in := input(view(t, games.Correspondence, chess.White))
	in.Peer = `<img src=x onerror=alert(1)>`
	in.Alert = `<script>alert(1)</script>`
	body := render(t, Build(in))
	if strings.Contains(body, "<img src=x") || strings.Contains(body, "<script>alert") {
		t.Fatal("unescaped text")
	}
	for _, want := range []string{`data-legal="`, `name="csrf_token" value="csrf-token"`, `action="/child/play/` + strings.Repeat("c", 32) + `/act"`,
		`aria-label="e2, weisser Bauer"`, `<script src="/child/chess.js" defer>`, `pattern="[a-h][1-8][a-h][1-8][qrbn]?"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, unwanted := range []string{"tenant", "child_id", "device_id"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("page mentions %s", unwanted)
		}
	}
	if strings.Contains(string(JS), "innerHTML") || strings.Contains(string(JS), "eval(") {
		t.Error("script uses markup injection")
	}
}

func TestFormatClockAndSignature(t *testing.T) {
	if FormatClock(293999*time.Millisecond) != "4:53" || FormatClock(-time.Second) != "0:00" || FormatClock(61*time.Minute) != "61:00" {
		t.Error("clock format")
	}
	v := view(t, games.Correspondence, chess.White)
	a := Signature(v)
	v.Version++
	b := Signature(v)
	v.End = games.EndTimeout
	if a == b || b == Signature(v) || Signature(nil) != "none" {
		t.Error("signature does not change")
	}
}

// The page's generic button rule (.yp-play button) must not override the
// square colours: square rules need at least the same specificity.
func TestSquareStylesOutrankButtonStyles(t *testing.T) {
	for _, rule := range []string{".yp-play .yp-sq{", ".yp-play .yp-sq.yp-light{background-color:"} {
		if !strings.Contains(pageCSS, rule) {
			t.Errorf("missing %s", rule)
		}
	}
	if strings.Contains(pageCSS, "\n.yp-sq{") {
		t.Error("square rule weaker than the button rule")
	}
}

func TestTipsOnThePage(t *testing.T) {
	v := view(t, games.Correspondence, chess.White)
	v.Tips, v.TipsLeft, v.TheirTipsUsed = true, 3, 1
	p := Build(input(v))
	if !p.CanTip || p.CanTipMove || p.TipText != "" || p.TipsNote != "Tipps: noch 3 von 3. Mia hat 1 genutzt." {
		t.Fatalf("before a tip: %+v", p)
	}
	v.Tip = &games.Tip{Level: 1, Hint: chess.Hint{Kind: chess.HintDevelop, Square: 6, Piece: chess.Knight}}
	p = Build(input(v))
	if p.CanTip || !p.CanTipMove || !strings.Contains(p.TipText, "den Springer auf g1") {
		t.Fatalf("level 1: %+v", p)
	}
	best, _ := chess.ParseUCI("g1f3")
	v.Tip = &games.Tip{Level: 2, Hint: v.Tip.Hint, Best: best}
	p = Build(input(v))
	if p.CanTipMove || p.TipMove != "Ein guter Zug wäre: Springer g1 nach f3." {
		t.Fatalf("level 2: %+v", p)
	}
	marked := 0
	for _, sq := range p.Board {
		if sq.Hint {
			marked++
			if !strings.HasSuffix(sq.Label, ", Tipp") {
				t.Errorf("label %q", sq.Label)
			}
		}
	}
	if marked != 2 {
		t.Fatalf("marked squares: %d", marked)
	}
	body := render(t, p)
	if !strings.Contains(body, `class="yp-tip" role="status"`) || !strings.Contains(body, "yp-hint") {
		t.Fatal("tip not rendered")
	}
	// No tips left and none on this move: no button.
	v.Tip, v.TipsLeft = nil, 0
	if p := Build(input(v)); p.CanTip {
		t.Fatal("tip offered without tips left")
	}
	// Not the viewer's turn: no tip.
	v = view(t, games.Correspondence, chess.Black)
	v.Tips, v.TipsLeft = true, 3
	if p := Build(input(v)); p.CanTip {
		t.Fatal("tip offered on the opponent's turn")
	}
	// The invitation offers tips.
	if body := render(t, Build(input(nil))); !strings.Contains(body, `name="tips" value="yes"`) {
		t.Fatal("no tips checkbox")
	}
}

func TestHintTexts(t *testing.T) {
	for kind := chess.HintMateInOne; kind <= chess.HintActivate; kind++ {
		text := HintText(chess.Hint{Kind: kind, Square: 3, Piece: chess.Queen}, "Mia")
		if text == "" || strings.Contains(text, "ß") {
			t.Errorf("kind %d: %q", kind, text)
		}
	}
	if got := HintText(chess.Hint{Kind: chess.HintHanging, Square: 3, Piece: chess.Queen}, "Mia"); got != "Deine Dame auf d1 ist in Gefahr. Bring die Figur in Sicherheit oder schütze sie." {
		t.Errorf("hanging: %q", got)
	}
	if got := HintText(chess.Hint{Kind: chess.HintThreatMate}, "Der Computer"); !strings.HasPrefix(got, "Achtung: Der Computer droht") {
		t.Errorf("threat: %q", got)
	}
}

func TestPracticePage(t *testing.T) {
	in := input(nil)
	in.Peer, in.Path, in.Practice = "Der Computer", "/child/practice", &Practice{}
	p := Build(in)
	if !p.Practice || !p.CanStart || p.CanInvite || p.PollMS != 0 || len(p.Levels) != 3 || len(p.Colors) != 3 || p.Title != "Üben gegen den Computer" {
		t.Fatalf("empty practice: %+v", p)
	}
	body := render(t, p)
	if !strings.Contains(body, `action="/child/practice/start"`) || !strings.Contains(body, `data-poll="0"`) || strings.Contains(body, "/invite") {
		t.Fatal("practice start form")
	}
	v := view(t, games.Correspondence, chess.White, "e2e4", "e7e5")
	v.Tips, v.TipsLeft = true, -1
	in.View, in.Practice = v, &Practice{Level: "medium", CanTakeback: true}
	p = Build(in)
	if !p.CanMove || !p.CanTakeback || !p.CanResign || p.CanOfferDraw && strings.Contains(render(t, p), "offer_draw") || !p.CanTip || p.TipsNote != "Tipps: so viele du willst." || p.Detail != "Du spielst Weiss. Stufe: mittel." {
		t.Fatalf("running practice: %+v", p)
	}
	body = render(t, p)
	if strings.Contains(body, `value="offer_draw"`) || strings.Contains(body, `value="abort"`) || !strings.Contains(body, `value="takeback"`) || !strings.Contains(body, "gewinnt der Computer") {
		t.Fatal("practice actions")
	}
	v.Phase, v.End, v.Winner = games.Finished, games.EndCheckmate, chess.Black
	if p := Build(in); p.Status != "Schachmatt. Der Computer hat gewonnen." || !p.CanStart || !p.CanTakeback {
		t.Fatalf("lost practice: %+v", p)
	}
}
