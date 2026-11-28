package childweb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/childsession"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/games"
	"github.com/pblumer/yapuno/internal/play"
	"github.com/pblumer/yapuno/internal/practice"
)

type playWorld struct {
	*world
	settings       *play.Settings
	store          *games.Store
	ab, ac         string // contacts Alex–Bea and Alex–Cem
	alex, bea, cem *http.Cookie
}

func (w *world) connectChildren(t *testing.T, gA, tA, cA, gB, tB, cB string) string {
	t.Helper()
	code, err := w.contacts.Invite(gA, tA, cA)
	if err != nil {
		t.Fatal(err)
	}
	id, err := w.contacts.Redeem(gB, tB, cB, code)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.contacts.Confirm(gA, tA, cA, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func newPlayWorld(t *testing.T) *playWorld {
	t.Helper()
	w := newWorld(t, true)
	dir := t.TempDir()
	settings, err := play.OpenSettings(filepath.Join(dir, "play.json"), w.families)
	if err != nil {
		t.Fatal(err)
	}
	store, err := games.Open(filepath.Join(dir, "games.log"), w.contacts, w.pairing, w.families, settings, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if w.sessions, err = childsession.New(w.pairing, w.families, time.Now, time.Hour); err != nil {
		t.Fatal(err)
	}
	if w.handler, err = New(w.sessions, w.families, Options{Contacts: w.contacts, Looks: w.looks, Games: store}); err != nil {
		t.Fatal(err)
	}
	practiceStore, err := practice.Open(filepath.Join(dir, "practice.json"), w.pairing, w.families, settings, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if w.handler, err = New(w.sessions, w.families, Options{Contacts: w.contacts, Looks: w.looks, Games: store, Practice: practiceStore}); err != nil {
		t.Fatal(err)
	}
	pw := &playWorld{world: w, settings: settings, store: store}
	pw.ab = w.connectChildren(t, "guardian-a", "tenant-a", "child-a", "guardian-b", "tenant-b", "child-b")
	pw.ac = w.connectChildren(t, "guardian-a", "tenant-a", "child-a", "guardian-c", "tenant-c", "child-c")
	for _, c := range [][3]string{{"guardian-a", "tenant-a", "child-a"}, {"guardian-b", "tenant-b", "child-b"}, {"guardian-c", "tenant-c", "child-c"}} {
		if err := settings.SetEnabled(c[0], c[1], c[2], play.GameChess, true); err != nil {
			t.Fatal(err)
		}
	}
	signIn := func(guardian, tenant, child string) *http.Cookie {
		id, key := pairingtest.Pair(t, w.pairing, guardian, tenant, child)
		return w.login(t, id, key)
	}
	pw.alex, pw.bea, pw.cem = signIn("guardian-a", "tenant-a", "child-a"), signIn("guardian-b", "tenant-b", "child-b"), signIn("guardian-c", "tenant-c", "child-c")
	return pw
}

var (
	gameInput = regexp.MustCompile(`name="game" value="([0-9a-f]{32})"`)
	plyInput  = regexp.MustCompile(`name="ply" value="(\d+)"`)
)

type pageForm struct{ csrf, client, game, ply string }

func (pw *playWorld) form(t *testing.T, cookie *http.Cookie, contact string) pageForm {
	t.Helper()
	body := pw.get("/child/play/"+contact, cookie).Body.String()
	var f pageForm
	if m := csrfInput.FindStringSubmatch(body); m != nil {
		f.csrf = m[1]
	}
	if m := clientIDInput.FindStringSubmatch(body); m != nil {
		f.client = m[1]
	}
	if m := gameInput.FindStringSubmatch(body); m != nil {
		f.game = m[1]
	}
	if m := plyInput.FindStringSubmatch(body); m != nil {
		f.ply = m[1]
	}
	if f.csrf == "" || f.client == "" {
		t.Fatalf("no form on chess page: %s", body)
	}
	return f
}

func (pw *playWorld) post(cookie *http.Cookie, path string, values url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, origin+path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	pw.handler.ServeHTTP(rec, r)
	return rec
}

func (pw *playWorld) sig(t *testing.T, cookie *http.Cookie, contact string) (int, string) {
	t.Helper()
	rec := pw.get("/child/play/"+contact+"/state", cookie)
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out["sig"]
}

type side struct {
	cookie *http.Cookie
	name   string
}

// begin lets Alex invite Bea and Bea accept. The server chooses colours at
// random; it returns the game id and who plays white and black.
func (pw *playWorld) begin(t *testing.T, mode string, extra ...string) (string, side, side) {
	t.Helper()
	f := pw.form(t, pw.alex, pw.ab)
	values := url.Values{"csrf_token": {f.csrf}, "client_id": {f.client}, "mode": {mode}}
	for i := 0; i+1 < len(extra); i += 2 {
		values.Add(extra[i], extra[i+1])
	}
	if rec := pw.post(pw.alex, "/child/play/"+pw.ab+"/invite", values); rec.Code != http.StatusSeeOther {
		t.Fatalf("invite: %d %s", rec.Code, rec.Body.String())
	}
	f = pw.form(t, pw.bea, pw.ab)
	if rec := pw.post(pw.bea, "/child/play/"+pw.ab+"/act", url.Values{"csrf_token": {f.csrf}, "client_id": {f.client}, "game": {f.game}, "kind": {"accept"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body.String())
	}
	alex, bea := side{pw.alex, "Alex"}, side{pw.bea, "Bea"}
	if strings.Contains(pw.get("/child/play/"+pw.ab, pw.alex).Body.String(), "Du bist am Zug.") {
		return f.game, alex, bea
	}
	return f.game, bea, alex
}

func TestHomeLinksToChessOnlyWhenBothMayPlay(t *testing.T) {
	pw := newPlayWorld(t)
	home := pw.get("/child", pw.alex).Body.String()
	if !strings.Contains(home, `href="/child/play/`+pw.ab+`"`) || !strings.Contains(home, `href="/child/play/`+pw.ac+`"`) {
		t.Fatalf("play links missing: %s", home)
	}
	if err := pw.settings.SetEnabled("guardian-c", "tenant-c", "child-c", play.GameChess, false); err != nil {
		t.Fatal(err)
	}
	home = pw.get("/child", pw.alex).Body.String()
	if strings.Contains(home, `/child/play/`+pw.ac) || !strings.Contains(home, `/child/play/`+pw.ab) {
		t.Fatal("link shown although Cem may not play")
	}
	f := pw.form(t, pw.alex, pw.ab)
	pw.post(pw.alex, "/child/play/"+pw.ab+"/invite", url.Values{"csrf_token": {f.csrf}, "client_id": {f.client}, "mode": {"correspondence"}})
	if home := pw.get("/child", pw.bea).Body.String(); !strings.Contains(home, "Schach mit Alex: Einladung") {
		t.Fatalf("invitation not shown: %s", home)
	}
}

func TestPlayAGameThroughForms(t *testing.T) {
	pw := newPlayWorld(t)
	_, none := pw.sig(t, pw.alex, pw.ab)
	game, white, black := pw.begin(t, "correspondence")
	code, before := pw.sig(t, pw.alex, pw.ab)
	if code != http.StatusOK || before == none || strings.Contains(before, "tenant") {
		t.Fatalf("state: %d %q", code, before)
	}
	page := pw.get("/child/play/"+pw.ab, white.cookie)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Du bist am Zug.") || !strings.Contains(page.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("page: %d", page.Code)
	}
	f := pw.form(t, white.cookie, pw.ab)
	move := func(cookie *http.Cookie, f pageForm, m, ply string) *httptest.ResponseRecorder {
		return pw.post(cookie, "/child/play/"+pw.ab+"/act", url.Values{"csrf_token": {f.csrf}, "client_id": {f.client}, "game": {game}, "kind": {"move"}, "move": {m}, "ply": {ply}})
	}
	if rec := move(white.cookie, f, "e2e5", f.ply); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "nicht erlaubt") || !strings.Contains(rec.Body.String(), `value="e2e5"`) {
		t.Fatalf("illegal move: %d", rec.Code)
	}
	if rec := move(white.cookie, f, "e2-e4", f.ply); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "zum Beispiel e2e4") {
		t.Fatalf("bad notation: %d", rec.Code)
	}
	if rec := move(white.cookie, f, "e2e4", "1"); rec.Code != http.StatusConflict {
		t.Fatalf("stale ply: %d", rec.Code)
	}
	if rec := move(white.cookie, f, "e2e4", "0"); rec.Code != http.StatusSeeOther {
		t.Fatalf("move: %d %s", rec.Code, rec.Body.String())
	}
	// A double submit of the same form stores one move.
	if rec := move(white.cookie, f, "e2e4", "0"); rec.Code != http.StatusSeeOther {
		t.Fatalf("double submit: %d", rec.Code)
	}
	if _, after := pw.sig(t, black.cookie, pw.ab); after == before {
		t.Fatal("signature did not change")
	}
	body := pw.get("/child/play/"+pw.ab, black.cookie).Body.String()
	if !strings.Contains(body, white.name+": Bauer e2 nach e4") || !strings.Contains(body, "Du bist am Zug.") || strings.Count(body, "data-square=") != 64 {
		t.Fatalf("opponent page: %s", body)
	}
	f = pw.form(t, black.cookie, pw.ab)
	if f.ply != "1" {
		t.Fatalf("ply on page: %q", f.ply)
	}
	if rec := pw.post(black.cookie, "/child/play/"+pw.ab+"/act", url.Values{"csrf_token": {f.csrf}, "client_id": {f.client}, "game": {game}, "kind": {"resign"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("resign: %d", rec.Code)
	}
	if body := pw.get("/child/play/"+pw.ab, white.cookie).Body.String(); !strings.Contains(body, black.name+" hat aufgegeben. Du hast gewonnen.") || !strings.Contains(body, black.name+" einladen") || !strings.Contains(body, "Nachbesprechung") {
		t.Fatalf("after resign: %s", body)
	}
}

func TestPlayRoutesRefuse(t *testing.T) {
	pw := newPlayWorld(t)
	game, _, _ := pw.begin(t, "live-10")
	f := pw.form(t, pw.alex, pw.ab)
	act := func(cookie *http.Cookie, contact string, v url.Values) int {
		return pw.post(cookie, "/child/play/"+contact+"/act", v).Code
	}
	good := func() url.Values {
		return url.Values{"csrf_token": {f.csrf}, "client_id": {f.client}, "game": {game}, "kind": {"resign"}}
	}
	cemForm := pw.form(t, pw.cem, pw.ac)
	cases := map[string]struct {
		cookie  *http.Cookie
		contact string
		values  url.Values
		want    int
	}{
		"outsider with own token": {pw.cem, pw.ab, func() url.Values { v := good(); v.Set("csrf_token", cemForm.csrf); return v }(), http.StatusForbidden},
		"game of another contact": {pw.alex, pw.ac, good(), http.StatusForbidden},
		"wrong CSRF token":        {pw.alex, pw.ab, func() url.Values { v := good(); v.Set("csrf_token", "x"); return v }(), http.StatusForbidden},
		"missing CSRF token":      {pw.alex, pw.ab, func() url.Values { v := good(); v.Del("csrf_token"); return v }(), http.StatusBadRequest},
		"two kinds":               {pw.alex, pw.ab, func() url.Values { v := good(); v.Add("kind", "abort"); return v }(), http.StatusBadRequest},
		"move fields on resign":   {pw.alex, pw.ab, func() url.Values { v := good(); v.Set("ply", "0"); return v }(), http.StatusBadRequest},
		"move without ply":        {pw.alex, pw.ab, func() url.Values { v := good(); v.Set("kind", "move"); v.Set("move", "e2e4"); return v }(), http.StatusBadRequest},
		"negative ply": {pw.alex, pw.ab, func() url.Values {
			v := good()
			v.Set("kind", "move")
			v.Set("move", "e2e4")
			v.Set("ply", "-1")
			return v
		}(), http.StatusBadRequest},
		"signed out":        {nil, pw.ab, good(), http.StatusUnauthorized},
		"malformed contact": {pw.alex, "nope", good(), http.StatusNotFound},
	}
	for name, c := range cases {
		if got := act(c.cookie, c.contact, c.values); got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
	r := httptest.NewRequest(http.MethodPost, origin+"/child/play/"+pw.ab+"/act", strings.NewReader(`{"kind":"resign"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(pw.alex)
	rec := httptest.NewRecorder()
	pw.handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("JSON body: %d", rec.Code)
	}
	if body := pw.get("/child/play/"+pw.ab, pw.alex).Body.String(); !strings.Contains(body, "Ja, ich gebe auf") {
		t.Fatal("a refused request changed the game")
	}
	// The outsider sees nothing of the game.
	if rec := pw.get("/child/play/"+pw.ab, pw.cem); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/child" {
		t.Errorf("outsider page: %d", rec.Code)
	}
	if code, _ := pw.sig(t, pw.cem, pw.ab); code != http.StatusForbidden {
		t.Errorf("outsider state: %d", code)
	}
	if code, _ := pw.sig(t, nil, pw.ab); code != http.StatusUnauthorized {
		t.Errorf("signed-out state: %d", code)
	}
	// Switching chess off stops the open game at once.
	if err := pw.settings.SetEnabled("guardian-b", "tenant-b", "child-b", play.GameChess, false); err != nil {
		t.Fatal(err)
	}
	if rec := pw.get("/child/play/"+pw.ab, pw.alex); rec.Code != http.StatusSeeOther {
		t.Errorf("page after disabling: %d", rec.Code)
	}
	if code, _ := pw.sig(t, pw.alex, pw.ab); code != http.StatusForbidden {
		t.Errorf("state after disabling: %d", code)
	}
	if got := act(pw.alex, pw.ab, good()); got != http.StatusForbidden {
		t.Errorf("act after disabling: %d", got)
	}
}

func TestTipsThroughForms(t *testing.T) {
	pw := newPlayWorld(t)
	f := pw.form(t, pw.alex, pw.ab)
	if rec := pw.post(pw.alex, "/child/play/"+pw.ab+"/invite", url.Values{"csrf_token": {f.csrf}, "client_id": {f.client}, "mode": {"correspondence"}, "tips": {"maybe"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad tips value: %d", rec.Code)
	}
	game, white, black := pw.begin(t, "correspondence", "tips", "yes")
	act := func(cookie *http.Cookie, v url.Values) *httptest.ResponseRecorder {
		f := pw.form(t, cookie, pw.ab)
		v.Set("csrf_token", f.csrf)
		v.Set("client_id", f.client)
		v.Set("game", game)
		return pw.post(cookie, "/child/play/"+pw.ab+"/act", v)
	}
	for name, v := range map[string]url.Values{
		"hint without level": {"kind": {"hint"}, "ply": {"0"}},
		"move with level":    {"kind": {"move"}, "move": {"e2e4"}, "ply": {"0"}, "level": {"1"}},
		"resign with ply":    {"kind": {"resign"}, "ply": {"0"}},
		"negative level":     {"kind": {"hint"}, "ply": {"0"}, "level": {"-1"}},
	} {
		if rec := act(white.cookie, v); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if !strings.Contains(pw.get("/child/play/"+pw.ab, white.cookie).Body.String(), `name="level" value="1"`) {
		t.Fatal("no tip button")
	}
	if rec := act(white.cookie, url.Values{"kind": {"hint"}, "level": {"1"}, "ply": {"0"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("tip: %d %s", rec.Code, rec.Body.String())
	}
	body := pw.get("/child/play/"+pw.ab, white.cookie).Body.String()
	if !strings.Contains(body, "Bring deine Springer und Läufer ins Spiel") || !strings.Contains(body, "Tipps: noch 2 von 3.") {
		t.Fatalf("tip not shown: %s", body)
	}
	if rec := act(white.cookie, url.Values{"kind": {"hint"}, "level": {"2"}, "ply": {"0"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("tip level 2: %d", rec.Code)
	}
	if body := pw.get("/child/play/"+pw.ab, white.cookie).Body.String(); !strings.Contains(body, "Ein guter Zug wäre:") {
		t.Fatal("move tip not shown")
	}
	other := pw.get("/child/play/"+pw.ab, black.cookie).Body.String()
	if strings.Contains(other, "Ein guter Zug") || !strings.Contains(other, white.name+" hat 1 genutzt.") {
		t.Fatal("opponent view of tips")
	}
}

func TestPracticeRoutes(t *testing.T) {
	pw := newPlayWorld(t)
	if !strings.Contains(pw.get("/child", pw.alex).Body.String(), `href="/child/practice"`) {
		t.Fatal("no practice link")
	}
	page := pw.get("/child/practice", pw.alex)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Üben gegen den Computer") {
		t.Fatalf("practice page: %d", page.Code)
	}
	f := pw.form(t, pw.alex, pw.ab) // any page carries the session's CSRF token
	post := func(path string, v url.Values) *httptest.ResponseRecorder {
		v.Set("csrf_token", f.csrf)
		v.Set("client_id", f.client)
		return pw.post(pw.alex, path, v)
	}
	if rec := post("/child/practice/start", url.Values{"level": {"medium"}, "color": {"white"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("start: %d", rec.Code)
	}
	if rec := post("/child/practice/act", url.Values{"kind": {"move"}, "move": {"e2e4"}, "ply": {"0"}, "game": {""}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("move: %d %s", rec.Code, rec.Body.String())
	}
	body := pw.get("/child/practice", pw.alex).Body.String()
	if !strings.Contains(body, "Der Computer: ") || !strings.Contains(body, `value="takeback"`) || !strings.Contains(body, `data-poll="0"`) {
		t.Fatalf("after move: %s", body)
	}
	if rec := post("/child/practice/act", url.Values{"kind": {"move"}, "move": {"e2e5"}, "ply": {"2"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("illegal: %d", rec.Code)
	}
	if rec := post("/child/practice/act", url.Values{"kind": {"hint"}, "level": {"1"}, "ply": {"2"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("hint: %d", rec.Code)
	}
	if rec := post("/child/practice/act", url.Values{"kind": {"takeback"}, "ply": {"2"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("takeback: %d", rec.Code)
	}
	for name, v := range map[string]url.Values{
		"game field":   {"kind": {"resign"}, "game": {strings.Repeat("a", 32)}},
		"unknown kind": {"kind": {"offer_draw"}},
	} {
		if rec := post("/child/practice/act", v); rec.Code != http.StatusBadRequest && rec.Code != http.StatusConflict {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if rec := post("/child/practice/start", url.Values{"level": {"godlike"}, "color": {"white"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown level: %d", rec.Code)
	}
	bad := url.Values{"csrf_token": {"x"}, "client_id": {f.client}, "kind": {"resign"}}
	if rec := pw.post(pw.alex, "/child/practice/act", bad); rec.Code != http.StatusForbidden {
		t.Errorf("wrong CSRF: %d", rec.Code)
	}
	if rec := pw.post(nil, "/child/practice/act", url.Values{"kind": {"resign"}}); rec.Code != http.StatusUnauthorized {
		t.Errorf("signed out: %d", rec.Code)
	}
	if strings.Contains(pw.get("/child/practice", pw.alex).Body.String(), "Nachbesprechung") {
		t.Fatal("review of a running game")
	}
	if rec := post("/child/practice/act", url.Values{"kind": {"move"}, "move": {"d2d4"}, "ply": {"0"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("move before resigning: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("/child/practice/act", url.Values{"kind": {"resign"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("resign: %d", rec.Code)
	}
	if !strings.Contains(pw.get("/child/practice", pw.alex).Body.String(), "Nachbesprechung") {
		t.Fatal("no review after practice")
	}
	// Switching chess off stops practice too.
	if err := pw.settings.SetEnabled("guardian-a", "tenant-a", "child-a", play.GameChess, false); err != nil {
		t.Fatal(err)
	}
	if rec := pw.get("/child/practice", pw.alex); rec.Code != http.StatusSeeOther {
		t.Errorf("page after disabling: %d", rec.Code)
	}
	if rec := post("/child/practice/act", url.Values{"kind": {"resign"}}); rec.Code != http.StatusForbidden {
		t.Errorf("act after disabling: %d", rec.Code)
	}
	if strings.Contains(pw.get("/child", pw.alex).Body.String(), "/child/practice") {
		t.Error("link after disabling")
	}
}
