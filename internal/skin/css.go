package skin

import (
	"fmt"
	"strings"
)

// CSSVariables serializes vetted color and layout tokens to CSS custom
// properties. It never accepts arbitrary CSS declarations or asset URLs.
func CSSVariables(s Definition) (string, error) {
	if err := Validate(s); err != nil {
		return "", err
	}
	radius := "0.5rem"
	if s.Shape == "rounded" {
		radius = "1.25rem"
	}
	spacing := "0.75rem"
	if s.Density == "compact" {
		spacing = "0.4rem"
	}
	return fmt.Sprintf(":root{--yp-background:%s;--yp-foreground:%s;--yp-accent:%s;--yp-radius:%s;--yp-spacing:%s;}", s.Palette.Background, s.Palette.Foreground, s.Palette.Accent, radius, spacing), nil
}

// Catalog provides two visually different first-party token sets.
// These are starter tokens, not complete accessible component skins.
func Catalog() map[string]Definition {
	return map[string]Definition{
		"classic": {Version: SchemaVersion, ID: "classic", Name: "Classic", Palette: Palette{Background: "#ffffff", Foreground: "#111111", Accent: "#3355aa"}, Density: "comfortable", Shape: "square"},
		"playful": {Version: SchemaVersion, ID: "playful", Name: "Playful", Palette: Palette{Background: "#fff7e8", Foreground: "#272044", Accent: "#7140a0"}, Density: "comfortable", Shape: "rounded"},
	}
}

// Stylesheet emits only validated first-party skins, keyed by a fixed CSS
// attribute selector. No user-controlled CSS or URLs are interpolated.
func Stylesheet() (string, error) {
	catalog := Catalog()
	var result strings.Builder
	result.WriteString("/* Yapuno semantic appearance tokens */\n")
	for _, id := range []string{"classic", "playful"} {
		s := catalog[id]
		css, err := CSSVariables(s)
		if err != nil {
			return "", err
		}
		result.WriteString("[data-yp-skin=\"")
		result.WriteString(id)
		result.WriteString("\"]")
		result.WriteString(strings.TrimPrefix(css, ":root"))
		result.WriteByte('\n')
	}
	return result.String(), nil
}
