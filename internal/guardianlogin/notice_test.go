package guardianlogin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginPageConfirmsSignOutWithFixedTextsOnly(t *testing.T) {
	h, _, _, _, _ := harness(t)
	page := func(query string) string {
		w := httptest.NewRecorder()
		h.Routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://yapuno.example.org/login"+query, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%q: %d", query, w.Code)
		}
		return w.Body.String()
	}
	if plain := page(""); strings.Contains(plain, `role="status"`) || !strings.Contains(plain, "Anmeldelink senden") {
		t.Fatal("plain login page wrong")
	}
	one := page("?notice=signed-out")
	if !strings.Contains(one, `role="status"`) || !strings.Contains(one, "Du bist abgemeldet.") || !strings.Contains(one, "Anmeldelink senden") {
		t.Fatal("sign-out not confirmed")
	}
	all := page("?notice=signed-out-everywhere")
	if !strings.Contains(all, `role="status"`) || !strings.Contains(all, "überall abgemeldet") || !strings.Contains(all, "Anmeldelinks") {
		t.Fatal("sign-out everywhere not confirmed")
	}
	for _, forged := range []string{"?notice=%3Cb%3Ehacked", "?notice=locked", "?notice=signed-out&notice=signed-out-everywhere", "?notice="} {
		if body := page(forged); strings.Contains(body, `role="status"`) || strings.Contains(body, "hacked") {
			t.Fatalf("%q rendered a notice", forged)
		}
	}
}
