package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/games"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
	"github.com/pblumer/yapuno/internal/guardiansession"
	"github.com/pblumer/yapuno/internal/play"
	"github.com/pblumer/yapuno/internal/practice"
)

func TestChessEndToEndWithGuardianSwitchAndPurge(t *testing.T) {
	root := t.TempDir()
	families, err := familystore.Open(filepath.Join(root, "families.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, tenant, guardian, name string }{{"child-a", "tenant-a", "guardian-a", "Alex"}, {"child-b", "tenant-b", "guardian-b", "Bea"}} {
		grant := authz.GuardianGrant{GuardianID: c.guardian, ChildID: c.id, TenantID: c.tenant, Active: true, CanManageDevices: true, CanApproveContacts: true}
		if err := families.Bootstrap(familydashboard.Child{ID: c.id, TenantID: c.tenant, DisplayName: c.name}, grant); err != nil {
			t.Fatal(err)
		}
	}
	guardiansPath := filepath.Join(root, "guardians.json")
	if err := os.WriteFile(guardiansPath, []byte(strings.Replace(twoGuardians, "%t", "true", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	guardians, err := guardiandirectory.New(guardiansPath, families)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := guardiansession.New(time.Now, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := devicepairing.Open(filepath.Join(root, "pairing.json"), families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	contactStore, err := contacts.Open(filepath.Join(root, "contacts.json"), families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := play.OpenSettings(filepath.Join(root, "play.json"), families)
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "games.log")
	gameStore, err := games.Open(logPath, contactStore, pairing, families, settings, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer gameStore.Close()
	if _, err := newHandler("https://app.example.org", families, guardians, &fakeMailer{}, sessions, optionalStores{pairing: pairing, contacts: contactStore, games: gameStore}); err == nil {
		t.Fatal("chess without game settings accepted")
	}
	practiceStore, err := practice.Open(filepath.Join(root, "practice.json"), pairing, families, settings, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newHandler("https://app.example.org", families, guardians, &fakeMailer{}, sessions, optionalStores{pairing: pairing, contacts: contactStore, play: settings, practice: practiceStore}); err == nil {
		t.Fatal("practice without chess accepted")
	}
	handler, err := newHandler("https://app.example.org", families, guardians, &fakeMailer{}, sessions, optionalStores{pairing: pairing, contacts: contactStore, play: settings, games: gameStore, practice: practiceStore})
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path string, cookie *http.Cookie, form url.Values, header map[string]string) *httptest.ResponseRecorder {
		var body []byte
		if form != nil {
			body = []byte(form.Encode())
		}
		r := httptest.NewRequest(method, "https://app.example.org"+path, bytes.NewReader(body))
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		for k, v := range header {
			r.Header.Set(k, v)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	signIn := func(guardian, tenant, child string) *http.Cookie {
		deviceID, key := pairingtest.Pair(t, pairing, guardian, tenant, child)
		post := func(path string, v any) *httptest.ResponseRecorder {
			b, _ := json.Marshal(v)
			r := httptest.NewRequest(http.MethodPost, "https://app.example.org"+path, bytes.NewReader(b))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "https://app.example.org")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			return w
		}
		var challenge struct {
			Nonce string `json:"nonce"`
		}
		if json.Unmarshal(post("/child/session/challenge", map[string]string{"device_id": deviceID}).Body.Bytes(), &challenge) != nil {
			t.Fatal("challenge")
		}
		proof := base64.RawURLEncoding.EncodeToString(pairingtest.LoginProof(t, key, deviceID, challenge.Nonce))
		w := post("/child/session/login", map[string]string{"device_id": deviceID, "nonce": challenge.Nonce, "signature": proof})
		if w.Code != http.StatusNoContent {
			t.Fatalf("login: %d", w.Code)
		}
		return w.Result().Cookies()[0]
	}
	guardianCookie := func(guardian, tenant string) *http.Cookie {
		w := httptest.NewRecorder()
		if err := sessions.IssueVerified(w, guardian, tenant); err != nil {
			t.Fatal(err)
		}
		return w.Result().Cookies()[0]
	}
	code, err := contactStore.Invite("guardian-a", "tenant-a", "child-a")
	if err != nil {
		t.Fatal(err)
	}
	link, err := contactStore.Redeem("guardian-b", "tenant-b", "child-b", code)
	if err != nil {
		t.Fatal(err)
	}
	if err := contactStore.Confirm("guardian-a", "tenant-a", "child-a", link); err != nil {
		t.Fatal(err)
	}
	alex, bea := signIn("guardian-a", "tenant-a", "child-a"), signIn("guardian-b", "tenant-b", "child-b")
	ga, gb := guardianCookie("guardian-a", "tenant-a"), guardianCookie("guardian-b", "tenant-b")

	// Off by default: no link for the child, the page sends it home.
	if strings.Contains(do(http.MethodGet, "/child", alex, nil, nil).Body.String(), "/child/play/") {
		t.Fatal("chess offered before guardians allowed it")
	}
	if w := do(http.MethodGet, "/child/play/"+link, alex, nil, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("page before enabling: %d", w.Code)
	}
	// Each family's guardian allows chess on the family page.
	allow := func(cookie *http.Cookie, child, value string) {
		page := do(http.MethodGet, "/family/games?child_id="+child, cookie, nil, nil)
		m := csrfField.FindStringSubmatch(page.Body.String())
		if page.Code != http.StatusOK || m == nil {
			t.Fatalf("game page: %d", page.Code)
		}
		form := url.Values{"csrf_token": {m[1]}, "child_id": {child}, "game": {"chess"}, "enabled": {value}}
		if w := do(http.MethodPost, "/family/games", cookie, form, nil); w.Code != http.StatusSeeOther {
			t.Fatalf("allow: %d", w.Code)
		}
	}
	if !strings.Contains(do(http.MethodGet, "/family", ga, nil, nil).Body.String(), "/family/games?child_id=child-a") {
		t.Fatal("family overview does not link to games")
	}
	if w := do(http.MethodGet, "/family/games?child_id=child-b", ga, nil, nil); w.Code != http.StatusForbidden {
		t.Fatalf("guardian A opened Bea's games: %d", w.Code)
	}
	allow(ga, "child-a", "yes")
	if strings.Contains(do(http.MethodGet, "/child", alex, nil, nil).Body.String(), "/child/play/") {
		t.Fatal("chess offered while only one family allowed it")
	}
	allow(gb, "child-b", "yes")

	fields := regexp.MustCompile(`name="(csrf_token|client_id|game)" value="([^"]+)"`)
	formOf := func(cookie *http.Cookie) url.Values {
		v := url.Values{}
		for _, m := range fields.FindAllStringSubmatch(do(http.MethodGet, "/child/play/"+link, cookie, nil, nil).Body.String(), -1) {
			v.Set(m[1], m[2])
		}
		return v
	}
	f := formOf(alex)
	f.Set("mode", "live-5")
	if w := do(http.MethodPost, "/child/play/"+link+"/invite", alex, f, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("invite: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(do(http.MethodGet, "/child", bea, nil, nil).Body.String(), "Schach mit Alex: Einladung") {
		t.Fatal("Bea does not see the invitation")
	}
	f = formOf(bea)
	f.Set("kind", "accept")
	if w := do(http.MethodPost, "/child/play/"+link+"/act", bea, f, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("accept: %d", w.Code)
	}
	white := alex
	if !strings.Contains(do(http.MethodGet, "/child/play/"+link, alex, nil, nil).Body.String(), "Du bist am Zug.") {
		white = bea
	}
	// Practice against the computer is there for each child, too.
	p := formOf(alex)
	p.Set("level", "easy")
	p.Set("color", "white")
	p.Del("game")
	if w := do(http.MethodPost, "/child/practice/start", alex, p, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("practice start: %d %s", w.Code, w.Body.String())
	}
	if body := do(http.MethodGet, "/child/practice", alex, nil, nil).Body.String(); !strings.Contains(body, "Du bist am Zug.") {
		t.Fatal("practice page")
	}
	f = formOf(white)
	f.Set("kind", "move")
	f.Set("move", "e2e4")
	f.Set("ply", "0")
	if w := do(http.MethodPost, "/child/play/"+link+"/act", white, f, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("move: %d", w.Code)
	}
	// Guardians have no way into the children's game.
	for _, g := range []*http.Cookie{ga, gb} {
		if w := do(http.MethodGet, "/child/play/"+link+"/state", g, nil, nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("guardian session read game state: %d", w.Code)
		}
	}
	raw, _ := os.ReadFile(logPath)
	if !strings.Contains(string(raw), `"move":"e2e4"`) {
		t.Fatal("precondition: the move should be on disk")
	}
	// Guardian B ends the contact; the game is gone from disk.
	m := csrfField.FindStringSubmatch(do(http.MethodGet, "/family/contacts?child_id=child-b", gb, nil, nil).Body.String())
	if m == nil {
		t.Fatal("no CSRF token on contact page")
	}
	form := url.Values{"csrf_token": {m[1]}, "child_id": {"child-b"}, "link_id": {link}, "confirm": {"yes"}}
	if w := do(http.MethodPost, "/family/contacts/remove", gb, form, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("remove contact: %d", w.Code)
	}
	for _, child := range []*http.Cookie{alex, bea} {
		if w := do(http.MethodGet, "/child/play/"+link+"/state", child, nil, nil); w.Code != http.StatusForbidden {
			t.Fatalf("state after removal: %d", w.Code)
		}
	}
	raw, _ = os.ReadFile(logPath)
	if len(bytes.TrimSpace(raw)) != 0 {
		t.Fatalf("games of the ended contact are still on disk: %s", raw)
	}
}
