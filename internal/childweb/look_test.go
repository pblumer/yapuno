package childweb

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
)

func childSubject(tenant, child string) appearance.Subject {
	return appearance.Subject{Kind: appearance.KindChild, TenantID: tenant, ID: child}
}

func (cw *chatWorld) postLook(cookie *http.Cookie, form url.Values, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, origin+"/child/look", strings.NewReader(form.Encode()))
	if contentType == "" {
		contentType = "application/x-www-form-urlencoded"
	}
	r.Header.Set("Content-Type", contentType)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	cw.handler.ServeHTTP(rec, r)
	return rec
}

func TestChildLookPageNeedsASession(t *testing.T) {
	cw := newChatWorld(t)
	if rec := cw.get("/child/look", nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/child" {
		t.Fatalf("signed-out look page: %d", rec.Code)
	}
	form := url.Values{"csrf_token": {"x"}, "skin": {"night"}, "shape": {"ghost"}, "color": {"peach"}}
	if rec := cw.postLook(nil, form, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out save: %d", rec.Code)
	}
	if cw.looks.Get(childSubject("tenant-a", "child-a")) != appearance.Default() {
		t.Fatal("signed-out request changed a look")
	}
}

func TestChildChoosesOwnLookFromSessionIdentityOnly(t *testing.T) {
	cw := newChatWorld(t)
	page := cw.get("/child/look", cw.alex)
	body := page.Body.String()
	if page.Code != http.StatusOK {
		t.Fatalf("look page: %d", page.Code)
	}
	for _, want := range []string{`<form method="post" action="/child/look">`, `<a class="yp-back" href="/child">`, `name="csrf_token"`, `<legend>Thema</legend>`, `<legend>Figur</legend>`} {
		if !strings.Contains(body, want) {
			t.Errorf("look page misses %q", want)
		}
	}
	if csp := page.Header().Get("Content-Security-Policy"); strings.Contains(csp, "script-src") || strings.Contains(body, "<script") || page.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("look page must be script-free and uncached: %q", csp)
	}
	form := url.Values{"csrf_token": {cw.csrfOf(t, cw.alex)}, "skin": {"night"}, "shape": {"ghost"}, "color": {"peach"},
		// Smuggled identity must be ignored.
		"tenant_id": {"tenant-b"}, "child_id": {"child-b"}, "kind": {"guardian"}}
	rec := cw.postLook(cw.alex, form, "")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/child/look?saved=1" {
		t.Fatalf("save: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	want := appearance.Preference{Skin: "night", Figure: appearance.Figure{Shape: "ghost", Color: "peach"}}
	if got := cw.looks.Get(childSubject("tenant-a", "child-a")); got != want {
		t.Fatalf("stored %+v", got)
	}
	for name, other := range map[string]appearance.Subject{
		"other child":              childSubject("tenant-b", "child-b"),
		"guardian with same ids":   {Kind: appearance.KindGuardian, TenantID: "tenant-a", ID: "child-a"},
		"same child, other tenant": childSubject("tenant-b", "child-a"),
	} {
		if cw.looks.Get(other) != appearance.Default() {
			t.Errorf("look of %s changed", name)
		}
	}
	if saved := cw.get("/child/look?saved=1", cw.alex).Body.String(); !strings.Contains(saved, `role="status"`) || !strings.Contains(saved, `data-yp-skin="night"`) {
		t.Fatal("no confirmation or new skin after saving")
	}
	home := cw.get("/child", cw.alex).Body.String()
	if !strings.Contains(home, `data-yp-skin="night"`) || !strings.Contains(home, `href="/child/look"`) {
		t.Fatal("home page does not use the new look or lacks the Mein Look link")
	}
	if chat := cw.page(cw.alex).Body.String(); !strings.Contains(chat, `data-yp-skin="night"`) {
		t.Fatal("chat page does not use the new look")
	}
}

func TestChildLookRejectsInvalidSubmissions(t *testing.T) {
	cases := map[string]struct {
		mutate      func(url.Values)
		contentType string
		want        int
	}{
		"wrong content type":   {func(url.Values) {}, "text/plain", http.StatusUnsupportedMediaType},
		"missing csrf":         {func(f url.Values) { f.Del("csrf_token") }, "", http.StatusBadRequest},
		"wrong csrf":           {func(f url.Values) { f.Set("csrf_token", "forged-token") }, "", http.StatusForbidden},
		"other session's csrf": {nil, "", http.StatusForbidden},
		"duplicate skin":       {func(f url.Values) { f.Add("skin", "classic") }, "", http.StatusBadRequest},
		"missing shape":        {func(f url.Values) { f.Del("shape") }, "", http.StatusBadRequest},
		"unknown skin":         {func(f url.Values) { f.Set("skin", "neon") }, "", http.StatusBadRequest},
		"injected skin":        {func(f url.Values) { f.Set("skin", `night"><script>`) }, "", http.StatusBadRequest},
		"photo as shape":       {func(f url.Values) { f.Set("shape", "photo") }, "", http.StatusBadRequest},
		"hex color":            {func(f url.Values) { f.Set("color", "#ff0000") }, "", http.StatusBadRequest},
		"oversized body":       {func(f url.Values) { f.Set("padding", strings.Repeat("x", 8192)) }, "", http.StatusBadRequest},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cw := newChatWorld(t)
			form := url.Values{"csrf_token": {cw.csrfOf(t, cw.alex)}, "skin": {"night"}, "shape": {"ghost"}, "color": {"peach"}}
			if c.mutate == nil {
				form.Set("csrf_token", cw.csrfOf(t, cw.bea))
			} else {
				c.mutate(form)
			}
			if rec := cw.postLook(cw.alex, form, c.contentType); rec.Code != c.want {
				t.Fatalf("got %d, want %d", rec.Code, c.want)
			}
			if cw.looks.Get(childSubject("tenant-a", "child-a")) != appearance.Default() {
				t.Fatal("rejected submission changed the stored look")
			}
		})
	}
}

// A child's figure is visible to mutually approved contacts and to nobody
// else. Themes stay private to each child.
func TestFiguresAreShownOnlyToApprovedContacts(t *testing.T) {
	cw := newChatWorld(t)
	set := func(tenant, child, skinID, shape, color string) {
		t.Helper()
		if err := cw.looks.Set(childSubject(tenant, child), appearance.Preference{Skin: skinID, Figure: appearance.Figure{Shape: shape, Color: color}}); err != nil {
			t.Fatal(err)
		}
	}
	set("tenant-b", "child-b", "terminal", "ghost", "peach") // Bea, Alex's contact
	set("tenant-c", "child-c", "vivid", "flower", "lime")    // Cem, no contact of Alex
	figure := func(shape, color string) string {
		t.Helper()
		svg, err := appearance.FigureSVG(appearance.Figure{Shape: shape, Color: color}, 44)
		if err != nil {
			t.Fatal(err)
		}
		return string(svg)
	}
	bea, cem := figure("ghost", "peach"), figure("flower", "lime")
	for name, body := range map[string]string{"home": cw.get("/child", cw.alex).Body.String(), "chat": cw.page(cw.alex).Body.String()} {
		if !strings.Contains(body, bea) {
			t.Errorf("%s: contact's figure missing", name)
		}
		if strings.Contains(body, cem) || strings.Contains(body, "#d9f692") {
			t.Errorf("%s: figure of a non-contact shown", name)
		}
		if strings.Contains(body, `data-yp-skin="terminal"`) {
			t.Errorf("%s: contact's theme leaked", name)
		}
	}
	// Cem, who is nobody's contact, sees no figures of others.
	if home := cw.get("/child", cw.cem).Body.String(); strings.Contains(home, bea) {
		t.Fatal("non-contact sees a figure")
	}
	// When the contact ends, the figure is no longer shown.
	if err := cw.contacts.Remove("guardian-b", "tenant-b", "child-b", cw.chat); err != nil {
		t.Fatal(err)
	}
	if home := cw.get("/child", cw.alex).Body.String(); strings.Contains(home, bea) {
		t.Fatal("figure still shown after the contact ended")
	}
}

func TestLookPagesNeedTheAppearanceStore(t *testing.T) {
	w := newWorld(t, false)
	id, key := pairingtest.Pair(t, w.pairing, "guardian-a", "tenant-a", "child-a")
	cookie := w.login(t, id, key)
	if rec := w.get("/child/look", cookie); rec.Code != http.StatusNotFound {
		t.Fatalf("look page without appearance store: %d", rec.Code)
	}
	if strings.Contains(w.get("/child", cookie).Body.String(), "/child/look") {
		t.Fatal("link to a disabled look page")
	}
}
