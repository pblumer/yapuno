package skin

import (
	"sort"
	"strings"
	"testing"
)

var requiredVariables = []string{
	"--yp-bg:", "--yp-surface:", "--yp-text:", "--yp-text-muted:", "--yp-line:",
	"--yp-accent:", "--yp-on-accent:", "--yp-accent-text:",
	"--yp-own-bubble:", "--yp-on-own-bubble:", "--yp-other-bubble:", "--yp-on-other-bubble:",
	"--yp-badge:", "--yp-on-badge:", "--yp-danger-text:", "--yp-focus:",
	"--yp-font-heading:", "--yp-font-body:", "--yp-font-size:",
	"--yp-radius:", "--yp-bubble-radius:", "--yp-bubble-border:", "--yp-bubble-shadow:",
	"--yp-wallpaper:", "--yp-wallpaper-size:", "--yp-spacing:", "--yp-motion:",
}

var forbiddenCSS = []string{"url(", "@import", "expression(", "javascript:", "</style", "<script", "https://", "http://"}

func TestCSSVariablesForCatalog(t *testing.T) {
	for id, s := range Catalog() {
		t.Run(id, func(t *testing.T) {
			css, err := CSSVariables(s)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(css, ":root{") || !strings.HasSuffix(css, "}") {
				t.Fatalf("unexpected wrapper: %q", css)
			}
			for _, v := range requiredVariables {
				if !strings.Contains(css, v) {
					t.Fatalf("missing variable %s", v)
				}
			}
			for _, c := range []string{s.Colors.Background, s.Colors.Text, s.Colors.OwnBubble, s.Colors.Focus} {
				if !strings.Contains(css, c) {
					t.Fatalf("missing color %s", c)
				}
			}
			for _, bad := range forbiddenCSS {
				if strings.Contains(strings.ToLower(css), bad) {
					t.Fatalf("forbidden construct %q in %q", bad, css)
				}
			}
		})
	}
}

func TestCSSVariablesRejectsInvalidSkin(t *testing.T) {
	s := validSkin()
	s.Colors.Accent = "#123456;background:url(https://attacker.invalid)"
	if _, err := CSSVariables(s); err == nil {
		t.Fatal("accepted CSS injection")
	}
}

func TestCSSVariablesNeverEmitsDisplayName(t *testing.T) {
	s := validSkin()
	s.Name = "</style><script>alert(1)</script>"
	css, err := CSSVariables(s)
	if err != nil {
		t.Fatalf("display name is escaped by templates and must not invalidate tokens: %v", err)
	}
	if strings.Contains(css, "script") || strings.Contains(css, s.Name) {
		t.Fatal("display name leaked into CSS")
	}
}

func TestBubbleStylesMapToDistinctTreatments(t *testing.T) {
	render := func(style string) string {
		s := validSkin()
		s.Shape.Bubble = style
		css, err := CSSVariables(s)
		if err != nil {
			t.Fatal(err)
		}
		return css
	}
	flat, outlined, raised := render("flat"), render("outlined"), render("raised")
	if !strings.Contains(flat, "--yp-bubble-border:none;") || !strings.Contains(flat, "--yp-bubble-shadow:none;") {
		t.Fatalf("flat bubbles must have no border or shadow: %s", flat)
	}
	if !strings.Contains(outlined, "--yp-bubble-border:1px solid #dde1ea;") {
		t.Fatalf("outlined bubbles must use the line color: %s", outlined)
	}
	if !strings.Contains(raised, "--yp-bubble-shadow:3px 3px 0 #141a2e;") {
		t.Fatalf("raised bubbles must use an offset text-colored shadow: %s", raised)
	}
}

func TestReducedMotionIsAlwaysHonored(t *testing.T) {
	s := validSkin()
	s.Motion = "standard"
	css, err := Stylesheet()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(css, "@media (prefers-reduced-motion:reduce)") || !strings.Contains(css, "--yp-motion:none") {
		t.Fatal("stylesheet must force motion off when the user requests reduced motion")
	}
	single, err := CSSVariables(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(single, "--yp-motion:standard;") {
		t.Fatalf("standard motion token missing: %s", single)
	}
}

func TestStylesheet(t *testing.T) {
	css, err := Stylesheet()
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(Catalog()))
	for id := range Catalog() {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	last := -1
	for _, id := range ids {
		marker := `[data-yp-skin="` + id + `"]{`
		pos := strings.Index(css, marker)
		if pos < 0 {
			t.Fatalf("missing skin selector %s", marker)
		}
		if pos < last {
			t.Fatal("stylesheet order is not deterministic by skin id")
		}
		last = pos
	}
	again, _ := Stylesheet()
	if again != css {
		t.Fatal("stylesheet output is not deterministic")
	}
	for _, bad := range forbiddenCSS {
		if strings.Contains(strings.ToLower(css), bad) {
			t.Fatalf("forbidden construct %q in stylesheet", bad)
		}
	}
}
