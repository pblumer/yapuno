package familyweb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/games"
	"github.com/pblumer/yapuno/internal/play"
)

type allActive struct{}

func (allActive) ActiveGuardian(context.Context, string, string) bool { return true }

type playWorld struct {
	*contactWorld
	settings *play.Settings
	games    *games.Store
}

func newPlayPagesWorld(t *testing.T) *playWorld {
	t.Helper()
	cw := newContactWorld(t)
	dir := t.TempDir()
	second := authz.GuardianGrant{GuardianID: "guardian-a", ChildID: "child-d", TenantID: "tenant-a", Active: true, CanManageDevices: true, CanApproveContacts: true}
	if err := cw.families.Bootstrap(familydashboard.Child{ID: "child-d", TenantID: "tenant-a", DisplayName: "Dana"}, second); err != nil {
		t.Fatal(err)
	}
	settings, err := play.OpenSettings(filepath.Join(dir, "play.json"), cw.families)
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := devicepairing.Open(filepath.Join(dir, "pairing.json"), cw.families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	store, err := games.Open(filepath.Join(dir, "games.log"), cw.store, pairing, cw.families, settings, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.EnableFamilies(cw.families, allActive{}); err != nil {
		t.Fatal(err)
	}
	return &playWorld{contactWorld: cw, settings: settings, games: store}
}

func (pw *playWorld) pages(t *testing.T, s Session) http.Handler {
	t.Helper()
	h, err := NewPlayPages(testAuth{session: s}, pw.games, pw.families, pw.looks)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

var playField = regexp.MustCompile(`name="(client_id|game)" value="([^"]+)"`)

func playForm(h http.Handler, path string) url.Values {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	v := url.Values{}
	for _, m := range playField.FindAllStringSubmatch(w.Body.String(), -1) {
		v.Set(m[1], m[2])
	}
	return v
}

func playPost(h http.Handler, path string, v url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestGuardianPlaysOwnChild(t *testing.T) {
	pw := newPlayPagesWorld(t)
	overview := func() string {
		h, err := NewWithOptions(testAuth{session: sessionA}, pw.families, Options{FamilyGames: pw.games})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family", nil))
		return w.Body.String()
	}
	if strings.Contains(overview(), "/family/play/") {
		t.Fatal("chess offered before it is allowed for the child")
	}
	if err := pw.settings.SetEnabled("guardian-a", "tenant-a", "child-a", play.GameChess, true); err != nil {
		t.Fatal(err)
	}
	if o := overview(); !strings.Contains(o, `href="/family/play/child-a"`) || strings.Contains(o, "/family/play/child-d") {
		t.Fatal("overview links")
	}
	h := pw.pages(t, sessionA)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/play/child-a", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Schach mit Alex") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") ||
		!strings.Contains(w.Body.String(), `aria-label="Zurück zur Familie"`) {
		t.Fatalf("page: %d", w.Code)
	}
	f := playForm(h, "/family/play/child-a")
	f.Set("csrf_token", sessionA.CSRFToken)
	f.Set("mode", "live-10")
	f.Set("tips", "yes")
	if w := playPost(h, "/family/play/child-a/invite", f); w.Code != http.StatusSeeOther {
		t.Fatalf("invite: %d %s", w.Code, w.Body.String())
	}
	v, ok, err := pw.games.GuardianLatest(games.GuardianPrincipal{TenantID: "tenant-a", GuardianID: "guardian-a"}, "child-a")
	if err != nil || !ok || !v.Family || !v.Tips || v.Phase != games.Invited {
		t.Fatalf("stored invitation: %+v %v %v", v, ok, err)
	}
	if o := overview(); strings.Contains(o, "Einladung") {
		t.Fatal("the inviter is told about an invitation")
	}
	// The game of Alex cannot be acted on through Dana's path.
	pw.settings.SetEnabled("guardian-a", "tenant-a", "child-d", play.GameChess, true)
	f = playForm(h, "/family/play/child-a")
	f.Set("csrf_token", sessionA.CSRFToken)
	f.Set("kind", "withdraw")
	if w := playPost(h, "/family/play/child-d/act", f); w.Code != http.StatusForbidden {
		t.Fatalf("game through another child's path: %d", w.Code)
	}
	bad := url.Values{}
	for k, vs := range f {
		bad[k] = vs
	}
	bad.Set("csrf_token", "wrong")
	if w := playPost(h, "/family/play/child-a/act", bad); w.Code != http.StatusForbidden {
		t.Fatalf("wrong CSRF: %d", w.Code)
	}
	if w := playPost(h, "/family/play/child-a/act", f); w.Code != http.StatusSeeOther {
		t.Fatalf("withdraw: %d %s", w.Code, w.Body.String())
	}
	if v, _, _ := pw.games.GuardianLatest(games.GuardianPrincipal{TenantID: "tenant-a", GuardianID: "guardian-a"}, "child-a"); v.End != games.EndWithdrawn {
		t.Fatalf("after withdraw: %+v", v)
	}
	// A game without moves has nothing to review.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/play/child-a", nil))
	if strings.Contains(w.Body.String(), "Nachbesprechung") {
		t.Fatal("review of a game without moves")
	}
}

func TestOtherGuardiansCannotPlayAChild(t *testing.T) {
	pw := newPlayPagesWorld(t)
	pw.settings.SetEnabled("guardian-a", "tenant-a", "child-a", play.GameChess, true)
	h := pw.pages(t, sessionB)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/play/child-a", nil))
	if w.Code != http.StatusSeeOther || strings.Contains(w.Body.String(), "Alex") {
		t.Fatalf("page of another family's child: %d", w.Code)
	}
	f := url.Values{"csrf_token": {sessionB.CSRFToken}, "client_id": {"client-id-0000000009"}, "mode": {"correspondence"}}
	if w := playPost(h, "/family/play/child-a/invite", f); w.Code != http.StatusForbidden {
		t.Fatalf("invite another family's child: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/play/child-a/state", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("state of another family's child: %d", w.Code)
	}
	anonymous, _ := NewPlayPages(testAuth{err: errors.New("no session")}, pw.games, pw.families, nil)
	w = httptest.NewRecorder()
	anonymous.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/play/child-a/state", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous state: %d", w.Code)
	}
}
