package familyweb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/appearance"
)

func TestGuardianPagesAllowAndDeclareOnlySelfHostedFonts(t *testing.T) {
	cw := newContactWorld(t)
	auth := testAuth{session: sessionA}
	family, err := NewWithOptions(auth, cw.families, Options{Looks: cw.looks, ContactPages: true})
	if err != nil {
		t.Fatal(err)
	}
	look, err := NewLookPages(auth, cw.looks)
	if err != nil {
		t.Fatal(err)
	}
	pages := map[string]struct {
		handler http.Handler
		target  string
	}{
		"family":   {family, "/family"},
		"contacts": {cw.as(t, auth), "/family/contacts?child_id=child-a"},
		"look":     {look, "/family/look"},
	}
	fetch := func(name string) (string, string) {
		p := pages[name]
		w := httptest.NewRecorder()
		p.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p.target, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d", name, w.Code)
		}
		return w.Body.String(), w.Header().Get("Content-Security-Policy")
	}
	subject := appearance.Subject{Kind: appearance.KindGuardian, TenantID: "tenant-a", ID: "guardian-a"}
	// Default look: system fonts, so the family and contact pages load no font.
	for _, name := range []string{"family", "contacts"} {
		if body, _ := fetch(name); strings.Contains(body, "@font-face") {
			t.Errorf("%s declares fonts for the classic skin", name)
		}
	}
	if err := cw.looks.Set(subject, appearance.Preference{Skin: "vivid", Figure: appearance.Default().Figure}); err != nil {
		t.Fatal(err)
	}
	for name := range pages {
		body, csp := fetch(name)
		if !strings.Contains(csp, "font-src 'self'") || !strings.Contains(csp, "default-src 'none'") {
			t.Errorf("%s: CSP does not allow self-hosted fonts only: %q", name, csp)
		}
		for _, face := range []string{`font-family:"Bricolage Grotesque"`, `font-family:"Plus Jakarta Sans"`} {
			if !strings.Contains(body, face) {
				t.Errorf("%s: missing %s", name, face)
			}
		}
		if strings.Contains(body, "googleapis") || strings.Contains(body, "gstatic") {
			t.Errorf("%s: external font reference", name)
		}
	}
	// The picker previews every theme, so it declares every font; browsers
	// fetch a file only when text actually uses it.
	if body, _ := fetch("look"); strings.Count(body, "@font-face") != 8 {
		t.Errorf("look page declares %d faces, want 8", strings.Count(body, "@font-face"))
	}
	if body, _ := fetch("family"); strings.Count(body, "@font-face") != 4 {
		t.Errorf("family page declares %d faces, want 4", strings.Count(body, "@font-face"))
	}
}
