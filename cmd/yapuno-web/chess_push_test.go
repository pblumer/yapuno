package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/play"
)

// notice asks, as the device's service worker does, what it was told.
func (s *chatServer) notice(cookie *http.Cookie) map[string]bool {
	s.t.Helper()
	r := httptest.NewRequest(http.MethodPost, "https://app.example.org/child/push/notice", nil)
	r.Header.Set("Origin", "https://app.example.org")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, r)
	var out map[string]bool
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		s.t.Fatalf("notice: %d %s", w.Code, w.Body.String())
	}
	return out
}

func TestAChildIsToldItsTurnAndInvitationsWithoutContent(t *testing.T) {
	s := newChatServer(t, false)
	s.chess, s.withPush = true, true
	s.start(false)
	for _, c := range [][3]string{{"guardian-a", "tenant-a", "child-a"}, {"guardian-b", "tenant-b", "child-b"}} {
		if err := s.settings.SetEnabled(c[0], c[1], c[2], play.GameChess, true); err != nil {
			t.Fatal(err)
		}
	}
	_, alex := s.device("guardian-a", alexParty)
	_, bea := s.device("guardian-b", beaParty)
	if code := s.subscribe(bea, beaEndpoint); code != http.StatusNoContent {
		t.Fatalf("Bea subscribes: %d", code)
	}
	path := "/child/play/" + s.link
	form := func(cookie *http.Cookie) url.Values {
		v := url.Values{}
		for _, f := range chessFormField.FindAllStringSubmatch(s.do(http.MethodGet, path, cookie, nil).Body.String(), -1) {
			v.Set(f[1], f[2])
		}
		return v
	}
	f := form(alex)
	f.Set("mode", "correspondence")
	if w := s.do(http.MethodPost, path+"/invite", alex, f); w.Code != http.StatusSeeOther {
		t.Fatalf("invite: %d", w.Code)
	}
	pushes := s.flush()
	if len(pushes) != 1 || pushes[0].endpoint != beaEndpoint || pushes[0].body != "" {
		t.Fatalf("invitation pushes: %+v", pushes)
	}
	dump := pushes[0].endpoint
	for k, v := range pushes[0].header {
		dump += k + strings.Join(v, ",")
	}
	for _, leak := range []string{"Alex", "Bea", "child-a", "child-b", "tenant-a", "tenant-b", s.link, "chess", "invite"} {
		if strings.Contains(dump, leak) {
			t.Errorf("push mentions %q", leak)
		}
	}
	if got := s.notice(bea); !got["invite"] || got["turn"] || got["message"] {
		t.Fatalf("Bea's device asks: %v", got)
	}
	if got := s.notice(bea); got["invite"] {
		t.Fatal("asked twice")
	}
	// Alex's device was told nothing and learns nothing.
	if got := s.notice(alex); got["invite"] || got["turn"] || got["message"] {
		t.Fatalf("Alex's device: %v", got)
	}
	f = form(bea)
	f.Set("kind", "accept")
	if w := s.do(http.MethodPost, path+"/act", bea, f); w.Code != http.StatusSeeOther {
		t.Fatalf("accept: %d", w.Code)
	}
	// Bea accepted herself: if she plays white, nobody is told; if Alex
	// plays white, it is his turn, and he has no subscription.
	if pushes := s.flush(); len(pushes) != 0 {
		t.Fatalf("pushes after Bea's own acceptance: %+v", pushes)
	}
	moves := []string{"e2e4", "e7e5", "g1f3", "b8c6"}
	ply := 0
	move := func(cookie *http.Cookie) {
		t.Helper()
		f := form(cookie)
		f.Set("kind", "move")
		f.Set("move", moves[ply])
		f.Set("ply", fmt.Sprint(ply))
		if w := s.do(http.MethodPost, path+"/act", cookie, f); w.Code != http.StatusSeeOther {
			t.Fatalf("move %s: %d %s", moves[ply], w.Code, w.Body.String())
		}
		ply++
	}
	alexWhite := strings.Contains(s.do(http.MethodGet, path, alex, nil).Body.String(), "Du bist am Zug.")
	if alexWhite {
		move(alex)
		if pushes := s.flush(); len(pushes) != 1 || pushes[0].endpoint != beaEndpoint {
			t.Fatalf("turn pushes: %+v", pushes)
		}
		if got := s.notice(bea); !got["turn"] || got["invite"] {
			t.Fatalf("Bea's turn: %v", got)
		}
	}
	// Bea moves; it is Alex's turn, and Alex has no subscription.
	move(bea)
	if pushes := s.flush(); len(pushes) != 0 {
		t.Fatalf("pushes to a device without subscription: %+v", pushes)
	}
	if code := s.subscribe(alex, alexEndpoint); code != http.StatusNoContent {
		t.Fatalf("Alex subscribes: %d", code)
	}
	// Alex moves, but Bea's guardians switch chess off before delivery:
	// nothing is sent.
	move(alex)
	if err := s.settings.SetEnabled("guardian-b", "tenant-b", "child-b", play.GameChess, false); err != nil {
		t.Fatal(err)
	}
	if pushes := s.flush(); len(pushes) != 0 {
		t.Fatalf("push although chess was switched off: %+v", pushes)
	}
}
