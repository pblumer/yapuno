package familyweb

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/play"
)

func gamePages(t *testing.T, auth testAuth) (http.Handler, *play.Settings) {
	t.Helper()
	cw := newContactWorld(t)
	// Guardian A may manage this child's devices, but not its contacts.
	limited := authz.GuardianGrant{GuardianID: "guardian-a", ChildID: "child-d", TenantID: "tenant-a", Active: true, CanManageDevices: true}
	if err := cw.families.Bootstrap(familydashboard.Child{ID: "child-d", TenantID: "tenant-a", DisplayName: "Dana"}, limited); err != nil {
		t.Fatal(err)
	}
	settings, err := play.OpenSettings(filepath.Join(t.TempDir(), "play.json"), cw.families)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewGamePages(auth, settings, cw.families, cw.looks)
	if err != nil {
		t.Fatal(err)
	}
	return h, settings
}

func gamePost(h http.Handler, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/family/games", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func allowChess(child, value string) url.Values {
	return url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {child}, "game": {"chess"}, "enabled": {value}}
}

func TestGamePagesToggleChessForOwnChild(t *testing.T) {
	h, settings := gamePages(t, testAuth{session: sessionA})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/games?child_id=child-a", nil))
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "Spiele von Alex") || !strings.Contains(body, "Nicht erlaubt") || !strings.Contains(body, `value="yes"`) {
		t.Fatalf("view: %d %s", w.Code, body)
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "script-src") {
		t.Fatalf("CSP: %s", csp)
	}
	w = gamePost(h, allowChess("child-a", "yes"))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family/games?child_id=child-a&done=on" {
		t.Fatalf("enable: %d %s", w.Code, w.Header().Get("Location"))
	}
	if !settings.Enabled("tenant-a", "child-a", play.GameChess) {
		t.Fatal("not enabled")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/games?child_id=child-a&done=on", nil))
	if body := w.Body.String(); !strings.Contains(body, "Schach ist jetzt erlaubt.") || !strings.Contains(body, `value="no"`) {
		t.Fatalf("after enable: %s", body)
	}
	if w := gamePost(h, allowChess("child-a", "no")); w.Code != http.StatusSeeOther || settings.Enabled("tenant-a", "child-a", play.GameChess) {
		t.Fatalf("disable: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/games?child_id=child-a&done=%3Cscript%3E", nil))
	if strings.Contains(w.Body.String(), "<script>") {
		t.Fatal("query text rendered")
	}
}

func TestGamePagesRefuse(t *testing.T) {
	h, settings := gamePages(t, testAuth{session: sessionA})
	cases := map[string]struct {
		form url.Values
		want int
	}{
		"child of another family": {allowChess("child-b", "yes"), http.StatusForbidden},
		"unknown child":           {allowChess("nobody", "yes"), http.StatusForbidden},
		"wrong CSRF token":        {func() url.Values { f := allowChess("child-a", "yes"); f.Set("csrf_token", "x"); return f }(), http.StatusForbidden},
		"missing CSRF token":      {func() url.Values { f := allowChess("child-a", "yes"); f.Del("csrf_token"); return f }(), http.StatusBadRequest},
		"unknown game":            {func() url.Values { f := allowChess("child-a", "yes"); f.Set("game", "poker"); return f }(), http.StatusBadRequest},
		"ambiguous choice":        {func() url.Values { f := allowChess("child-a", "yes"); f.Add("enabled", "no"); return f }(), http.StatusBadRequest},
		"other value":             {allowChess("child-a", "true"), http.StatusBadRequest},
		"malformed child id":      {allowChess("a<b", "yes"), http.StatusBadRequest},
	}
	for name, c := range cases {
		if w := gamePost(h, c.form); w.Code != c.want {
			t.Errorf("%s: %d, want %d", name, w.Code, c.want)
		}
	}
	if settings.Enabled("tenant-a", "child-a", play.GameChess) || settings.Enabled("tenant-b", "child-b", play.GameChess) {
		t.Fatal("a refused request changed a setting")
	}
	r := httptest.NewRequest(http.MethodPost, "/family/games", strings.NewReader(allowChess("child-a", "yes").Encode()))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("JSON body: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/games?child_id=child-b", nil))
	if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "Bea") {
		t.Errorf("view other family's child: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/games?child_id=child-d", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("view without contact authority: %d", w.Code)
	}
	anonymous, _ := gamePages(t, testAuth{err: errors.New("no session")})
	w = httptest.NewRecorder()
	anonymous.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/games?child_id=child-a", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous view: %d", w.Code)
	}
	if w := gamePost(anonymous, allowChess("child-a", "yes")); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous save: %d", w.Code)
	}
}

func TestFamilyOverviewLinksToGamesOnlyWhenOn(t *testing.T) {
	cw := newContactWorld(t)
	for _, on := range []bool{false, true} {
		h, err := NewWithOptions(testAuth{session: sessionA}, cw.families, Options{GamePages: on})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family", nil))
		if got := strings.Contains(w.Body.String(), `href="/family/games?child_id=child-a"`); got != on {
			t.Errorf("GamePages %v: link shown %v", on, got)
		}
		if strings.Contains(w.Body.String(), "child-b") {
			t.Error("another family's child linked")
		}
	}
}
