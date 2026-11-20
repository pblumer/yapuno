package familyweb

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/skin"
)

var guardianA = Session{GuardianID: "guardian-a", TenantID: "tenant-a", CSRFToken: "csrf-a-0123456789"}

func lookSetup(t *testing.T, auth testAuth) (http.Handler, http.Handler, *appearance.Store) {
	t.Helper()
	looks, err := appearance.Open(filepath.Join(t.TempDir(), "appearance.json"))
	if err != nil {
		t.Fatal(err)
	}
	pages, err := NewLookPages(auth, looks)
	if err != nil {
		t.Fatal(err)
	}
	families, err := familystore.Open(filepath.Join(t.TempDir(), "family.json"))
	if err != nil {
		t.Fatal(err)
	}
	grant := authz.GuardianGrant{GuardianID: "guardian-a", ChildID: "child-a", TenantID: "tenant-a", Active: true, CanManageDevices: true, CanApproveContacts: true}
	if err := families.Bootstrap(familydashboard.Child{ID: "child-a", TenantID: "tenant-a", DisplayName: "Alex"}, grant); err != nil {
		t.Fatal(err)
	}
	family, err := NewWithAppearance(auth, families, looks, false)
	if err != nil {
		t.Fatal(err)
	}
	return pages, family, looks
}

func postLook(h http.Handler, form url.Values, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/family/look", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func validLookForm() url.Values {
	return url.Values{"csrf_token": {guardianA.CSRFToken}, "skin": {"night"}, "shape": {"ghost"}, "color": {"peach"}}
}

func subjectA() appearance.Subject {
	return appearance.Subject{Kind: appearance.KindGuardian, TenantID: "tenant-a", ID: "guardian-a"}
}

func TestLookRequiresAuthentication(t *testing.T) {
	pages, _, looks := lookSetup(t, testAuth{err: errors.New("no session")})
	r := httptest.NewRequest(http.MethodGet, "/family/look", nil)
	w := httptest.NewRecorder()
	pages.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("GET without session: %d", w.Code)
	}
	if w := postLook(pages, validLookForm(), "application/x-www-form-urlencoded"); w.Code != http.StatusUnauthorized {
		t.Fatalf("POST without session: %d", w.Code)
	}
	if looks.Get(subjectA()) != appearance.Default() {
		t.Fatal("unauthenticated request changed a preference")
	}
}

func TestLookPageRendersAccessibleChoices(t *testing.T) {
	pages, _, looks := lookSetup(t, testAuth{session: guardianA})
	if err := looks.Set(subjectA(), appearance.Preference{Skin: "vivid", Figure: appearance.Figure{Shape: "flower", Color: "lime"}}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	pages.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/look", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`data-yp-skin="vivid"`, `<form method="post" action="/family/look">`,
		`name="csrf_token" value="` + guardianA.CSRFToken + `"`,
		`<legend>Thema</legend>`, `<legend>Figur</legend>`, `<legend>Farbe</legend>`,
		`id="yp-skin-vivid" name="skin" value="vivid" checked`,
		`id="yp-shape-flower" name="shape" value="flower" checked`,
		`id="yp-color-lime" name="color" value="lime" checked`,
		`Nur das Aussehen ändert sich`, `href="/family"`,
		`@media (prefers-reduced-motion:reduce)`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for id := range skin.Catalog() {
		if !strings.Contains(body, `value="`+id+`"`) {
			t.Errorf("skin %q not offered", id)
		}
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing security headers")
	}
	if strings.Contains(body, "<script") {
		t.Fatal("look page must work without JavaScript")
	}
}

func TestLookSaveUsesSessionIdentityOnly(t *testing.T) {
	pages, family, looks := lookSetup(t, testAuth{session: guardianA})
	form := validLookForm()
	// Identity fields in the form must be ignored; the session decides.
	form.Set("guardian_id", "guardian-b")
	form.Set("tenant_id", "tenant-b")
	w := postLook(pages, form, "application/x-www-form-urlencoded")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family/look?saved=1" {
		t.Fatalf("save: %d %q", w.Code, w.Header().Get("Location"))
	}
	want := appearance.Preference{Skin: "night", Figure: appearance.Figure{Shape: "ghost", Color: "peach"}}
	if got := looks.Get(subjectA()); got != want {
		t.Fatalf("stored %+v", got)
	}
	if got := looks.Get(appearance.Subject{Kind: appearance.KindGuardian, TenantID: "tenant-b", ID: "guardian-b"}); got != appearance.Default() {
		t.Fatal("form-supplied identity was used")
	}
	saved := httptest.NewRecorder()
	pages.ServeHTTP(saved, httptest.NewRequest(http.MethodGet, "/family/look?saved=1", nil))
	if !strings.Contains(saved.Body.String(), `role="status"`) {
		t.Fatal("no save confirmation")
	}
	dash := httptest.NewRecorder()
	family.ServeHTTP(dash, httptest.NewRequest(http.MethodGet, "/family", nil))
	if !strings.Contains(dash.Body.String(), `data-yp-skin="night"`) || !strings.Contains(dash.Body.String(), `href="/family/look"`) {
		t.Fatal("family page ignores saved look or lacks the Mein Look entry")
	}
}

func TestLookRejectsInvalidSubmissions(t *testing.T) {
	cases := map[string]struct {
		mutate      func(url.Values)
		contentType string
		want        int
	}{
		"wrong content type": {func(url.Values) {}, "text/plain", http.StatusUnsupportedMediaType},
		"missing csrf":       {func(f url.Values) { f.Del("csrf_token") }, "", http.StatusBadRequest},
		"wrong csrf":         {func(f url.Values) { f.Set("csrf_token", "csrf-b-0123456789") }, "", http.StatusForbidden},
		"duplicate skin":     {func(f url.Values) { f.Add("skin", "classic") }, "", http.StatusBadRequest},
		"unknown skin":       {func(f url.Values) { f.Set("skin", "neon") }, "", http.StatusBadRequest},
		"injected skin":      {func(f url.Values) { f.Set("skin", `night"><script>`) }, "", http.StatusBadRequest},
		"unknown shape":      {func(f url.Values) { f.Set("shape", "photo") }, "", http.StatusBadRequest},
		"hex color":          {func(f url.Values) { f.Set("color", "#ff0000") }, "", http.StatusBadRequest},
		"oversized body":     {func(f url.Values) { f.Set("padding", strings.Repeat("x", 8192)) }, "", http.StatusBadRequest},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			pages, _, looks := lookSetup(t, testAuth{session: guardianA})
			form := validLookForm()
			c.mutate(form)
			contentType := c.contentType
			if contentType == "" {
				contentType = "application/x-www-form-urlencoded"
			}
			if w := postLook(pages, form, contentType); w.Code != c.want {
				t.Fatalf("got %d, want %d", w.Code, c.want)
			}
			if looks.Get(subjectA()) != appearance.Default() {
				t.Fatal("rejected submission changed the stored preference")
			}
		})
	}
}

func TestFamilyQuerySkinStillPreviewsWithoutSaving(t *testing.T) {
	_, family, looks := lookSetup(t, testAuth{session: guardianA})
	w := httptest.NewRecorder()
	family.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family?skin=terminal", nil))
	if !strings.Contains(w.Body.String(), `data-yp-skin="terminal"`) {
		t.Fatal("query preview no longer works")
	}
	if looks.Get(subjectA()) != appearance.Default() {
		t.Fatal("a GET request changed the stored preference")
	}
}
