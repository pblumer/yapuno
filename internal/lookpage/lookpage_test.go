package lookpage

import (
	"bytes"
	"net/url"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/skin"
)

func render(t *testing.T, p Page) string {
	t.Helper()
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestRenderOffersEveryChoiceAndMarksTheSavedOne(t *testing.T) {
	look := appearance.Preference{Skin: "night", Figure: appearance.Figure{Shape: "ghost", Color: "peach"}}
	body := render(t, Page{Action: "/child/look", BackHref: "/child", BackLabel: "← Zurück", CSRF: "csrf-token-123", Saved: true, Look: look})
	for _, want := range []string{
		`<html lang="de">`, `data-yp-skin="night"`, `<form method="post" action="/child/look">`,
		`<a class="yp-back" href="/child">← Zurück</a>`, `name="csrf_token" value="csrf-token-123"`,
		`id="yp-skin-night" name="skin" value="night" checked`, `id="yp-shape-ghost" name="shape" value="ghost" checked`,
		`id="yp-color-peach" name="color" value="peach" checked`, `role="status"`,
		`<legend>Thema</legend>`, `<legend>Figur</legend>`, `<legend>Farbe</legend>`, `@media (prefers-reduced-motion:reduce)`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for id := range skin.Catalog() {
		if !strings.Contains(body, `name="skin" value="`+id+`"`) {
			t.Errorf("skin %q not offered", id)
		}
	}
	for _, s := range appearance.Shapes() {
		if !strings.Contains(body, `name="shape" value="`+s.ID+`"`) {
			t.Errorf("shape %q not offered", s.ID)
		}
	}
	if strings.Count(body, " checked") != 3 || strings.Contains(body, "<script") {
		t.Fatal("exactly the three saved choices must be checked and the page must have no script")
	}
	if unsaved := render(t, Page{Action: "/x", BackHref: "/", BackLabel: "b", CSRF: "c", Look: look}); strings.Contains(unsaved, `role="status"`) {
		t.Fatal("save confirmation shown without saving")
	}
}

func TestRenderEscapesPageParametersAndRejectsInvalidLooks(t *testing.T) {
	look := appearance.Default()
	body := render(t, Page{Action: `/x"><script>alert(1)</script>`, BackHref: `javascript:alert(1)`, BackLabel: `<b>zurück</b>`, CSRF: `"><img src=x>`, Look: look})
	for _, bad := range []string{"<script>alert(1)", `href="javascript:`, "<b>zurück</b>", "<img src=x>"} {
		if strings.Contains(body, bad) {
			t.Errorf("unescaped %q", bad)
		}
	}
	for _, invalid := range []appearance.Preference{
		{Skin: "neon", Figure: look.Figure},
		{Skin: look.Skin, Figure: appearance.Figure{Shape: "photo", Color: "mint"}},
		{Skin: look.Skin, Figure: appearance.Figure{Shape: "ball", Color: "#ff0000;}body{display:none"}},
		{},
	} {
		var b bytes.Buffer
		if err := Render(&b, Page{Action: "/x", BackHref: "/", BackLabel: "b", CSRF: "c", Look: invalid}); err == nil || b.Len() != 0 {
			t.Errorf("rendered invalid look %+v", invalid)
		}
	}
}

func TestParseTakesExactlyOneValuePerChoice(t *testing.T) {
	valid := func() url.Values { return url.Values{"skin": {"vivid"}, "shape": {"flower"}, "color": {"lime"}} }
	got, ok := Parse(valid())
	if !ok || got != (appearance.Preference{Skin: "vivid", Figure: appearance.Figure{Shape: "flower", Color: "lime"}}) {
		t.Fatalf("valid form: %+v %v", got, ok)
	}
	for name, mutate := range map[string]func(url.Values){
		"missing skin":    func(v url.Values) { v.Del("skin") },
		"missing shape":   func(v url.Values) { v.Del("shape") },
		"missing color":   func(v url.Values) { v.Del("color") },
		"duplicate skin":  func(v url.Values) { v.Add("skin", "night") },
		"duplicate shape": func(v url.Values) { v.Add("shape", "ghost") },
		"duplicate color": func(v url.Values) { v.Add("color", "mint") },
	} {
		v := valid()
		mutate(v)
		if _, ok := Parse(v); ok {
			t.Errorf("%s accepted", name)
		}
	}
	// Parse only shapes the form; whether the values exist is the store's check.
	if got, ok := Parse(url.Values{"skin": {"neon"}, "shape": {"x"}, "color": {"y"}}); !ok || got.Skin != "neon" {
		t.Fatal("Parse should not validate vocabulary")
	}
}
