package main

import (
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/play"
)

var (
	familyPlayLink = regexp.MustCompile(`href="/child/play/([0-9a-f]{32})" aria-label="Schach mit`)
	chessFormField = regexp.MustCompile(`name="(client_id|game|csrf_token)" value="([^"]+)"`)
)

func TestChildPlaysOwnGuardianAndSeesOnlyOwnRating(t *testing.T) {
	s := newSecurityServer(t, false)
	s.chatPath = filepath.Join(s.root, "guardian-chat.log")
	s.chess = true
	s.start(false)
	deviceID, key := pairingtest.Pair(t, s.pairing, "guardian-a", "tenant-a", "child-a")
	alex := childLogin(t, s.handler, deviceID, key)
	anna := s.issue("guardian-a", "tenant-a")
	if familyPlayLink.MatchString(s.do(http.MethodGet, "/child", alex, nil).Body.String()) {
		t.Fatal("family chess offered before chess is allowed")
	}
	if err := s.settings.SetEnabled("guardian-a", "tenant-a", "child-a", play.GameChess, true); err != nil {
		t.Fatal(err)
	}
	m := familyPlayLink.FindStringSubmatch(s.do(http.MethodGet, "/child", alex, nil).Body.String())
	if m == nil {
		t.Fatal("no chess link next to the guardian")
	}
	scope := m[1]
	formOf := func(path string, cookie *http.Cookie) url.Values {
		v := url.Values{}
		for _, f := range chessFormField.FindAllStringSubmatch(s.do(http.MethodGet, path, cookie, nil).Body.String(), -1) {
			v.Set(f[1], f[2])
		}
		return v
	}
	// The guardian invites from the family area.
	if !strings.Contains(s.do(http.MethodGet, "/family", anna, nil).Body.String(), `href="/family/play/child-a"`) {
		t.Fatal("no chess link on the family overview")
	}
	f := formOf("/family/play/child-a", anna)
	f.Set("mode", "correspondence")
	if w := s.do(http.MethodPost, "/family/play/child-a/invite", anna, f); w.Code != http.StatusSeeOther {
		t.Fatalf("guardian invites: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(s.do(http.MethodGet, "/child", alex, nil).Body.String(), ": Einladung") {
		t.Fatal("child not told about the invitation")
	}
	f = formOf("/child/play/"+scope, alex)
	f.Set("kind", "accept")
	if w := s.do(http.MethodPost, "/child/play/"+scope+"/act", alex, f); w.Code != http.StatusSeeOther {
		t.Fatalf("child accepts: %d", w.Code)
	}
	// Alex resigns; the game is rated, and only Alex sees Alex's rating.
	f = formOf("/child/play/"+scope, alex)
	f.Set("kind", "resign")
	if w := s.do(http.MethodPost, "/child/play/"+scope+"/act", alex, f); w.Code != http.StatusSeeOther {
		t.Fatalf("resign: %d", w.Code)
	}
	home := s.do(http.MethodGet, "/child", alex, nil).Body.String()
	if !strings.Contains(home, "Deine Schachwertung: <strong>980</strong>") {
		t.Fatalf("child's rating: %s", home)
	}
	for _, page := range []string{"/family", "/family/play/child-a", "/family/games?child_id=child-a"} {
		if body := s.do(http.MethodGet, page, anna, nil).Body.String(); strings.Contains(body, "Schachwertung") || strings.Contains(body, "980") || strings.Contains(body, "1020") {
			t.Fatalf("guardian sees a rating on %s", page)
		}
	}
	// Guardians of another family get nowhere near Alex's game.
	bruno := s.issue("guardian-b", "tenant-b")
	if w := s.do(http.MethodGet, "/family/play/child-a", bruno, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("other guardian opened the game: %d", w.Code)
	}
	if w := s.do(http.MethodGet, "/child/play/"+scope+"/state", bruno, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("guardian session on child route: %d", w.Code)
	}
}
