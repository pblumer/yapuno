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
func (pw *playWorld) begin(t *testing.T, mode string) (string, side, side) {
	t.Helper()
	f := pw.form(t, pw.alex, pw.ab)
	if rec := pw.post(pw.alex, "/child/play/"+pw.ab+"/invite", url.Values{"csrf_token": {f.csrf}, "client_id": {f.client}, "mode": {mode}}); rec.Code != http.StatusSeeOther {
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
	if body := pw.get("/child/play/"+pw.ab, white.cookie).Body.String(); !strings.Contains(body, black.name+" hat aufgegeben. Du hast gewonnen.") || !strings.Contains(body, black.name+" einladen") {
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
