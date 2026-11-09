package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/skin"
)

func TestDemoProfilesAreEditableOnlyWithToken(t *testing.T) {
	state := newDemoState()
	handler := state.handler()
	send := func(name, token string) *httptest.ResponseRecorder {
		t.Helper()
		form := url.Values{"name": {name}, "token": {token}, "skin": {"playful"}}
		req := httptest.NewRequest(http.MethodPost, "/profiles", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	if got := send("No access", "wrong").Code; got != http.StatusForbidden {
		t.Fatalf("invalid token: got %d", got)
	}
	if got := send("<script>", state.token).Code; got != http.StatusBadRequest {
		t.Fatalf("unsafe name: got %d", got)
	}
	if got := send("Nina", state.token).Code; got != http.StatusSeeOther {
		t.Fatalf("valid profile: got %d", got)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/?skin=playful", nil))
	if !strings.Contains(w.Body.String(), "Nina") || !strings.Contains(w.Body.String(), "data-yp-skin=\"playful\"") {
		t.Fatal("new profile not visible in selected skin")
	}
	if len(state.children) != 3 {
		t.Fatalf("expected 3 synthetic profiles, got %d", len(state.children))
	}
}

func TestDemoOffersEveryFirstPartySkin(t *testing.T) {
	w := httptest.NewRecorder()
	newDemoState().handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	for id, s := range skin.Catalog() {
		if !strings.Contains(w.Body.String(), `href="/?skin=`+id+`"`) || !strings.Contains(w.Body.String(), s.Name) {
			t.Errorf("demo navigation misses skin %q", id)
		}
	}
}
