package skin

import (
	"strings"
	"testing"
)

func TestCSSVariables(t *testing.T) {
	for id, s := range Catalog() {
		t.Run(id, func(t *testing.T) {
			css, err := CSSVariables(s)
			if err != nil {
				t.Fatal(err)
			}
			for _, token := range []string{s.Palette.Background, s.Palette.Foreground, s.Palette.Accent, "--yp-radius:", "--yp-spacing:"} {
				if !strings.Contains(css, token) {
					t.Fatalf("missing token %q", token)
				}
			}
			if strings.Contains(css, "url(") || strings.Contains(css, "@import") {
				t.Fatal("unexpected external CSS")
			}
		})
	}
	invalid := Catalog()["classic"]
	invalid.Palette.Accent = "#123456;background:url(https://attacker.invalid)"
	if _, err := CSSVariables(invalid); err == nil {
		t.Fatal("accepted CSS injection")
	}
}

func TestStylesheet(t *testing.T) {
	css, err := Stylesheet()
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{`[data-yp-skin="classic"]`, `[data-yp-skin="playful"]`} {
		if !strings.Contains(css, marker) {
			t.Fatalf("missing skin selector %s", marker)
		}
	}
	if strings.Contains(css, "https://") {
		t.Fatal("external URL in stylesheet")
	}
}
