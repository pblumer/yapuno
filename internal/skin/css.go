package skin

import (
	"fmt"
	"sort"
	"strings"
)

// CSSVariables serializes a validated skin to CSS custom properties wrapped in
// a :root rule. Every emitted value is a validated hex color or a fixed string
// chosen from a closed vocabulary; no skin-provided text is interpolated.
func CSSVariables(s Definition) (string, error) {
	decls, err := declarations(s)
	if err != nil {
		return "", err
	}
	return ":root{" + decls + "}", nil
}

func declarations(s Definition) (string, error) {
	if err := Validate(s); err != nil {
		return "", err
	}
	c := s.Colors
	radius := radii[s.Shape.Radius]
	border, shadow := bubbleTreatment(s)
	wallpaper, wallpaperSize, err := wallpaperTreatment(s)
	if err != nil {
		return "", err
	}
	vars := [][2]string{
		{"bg", c.Background}, {"surface", c.Surface}, {"text", c.Text}, {"text-muted", c.TextMuted}, {"line", c.Line},
		{"accent", c.Accent}, {"on-accent", c.OnAccent}, {"accent-text", c.AccentText},
		{"own-bubble", c.OwnBubble}, {"on-own-bubble", c.OnOwnBubble}, {"other-bubble", c.OtherBubble}, {"on-other-bubble", c.OnOtherBubble},
		{"badge", c.Badge}, {"on-badge", c.OnBadge}, {"danger-text", c.DangerText}, {"focus", c.Focus},
		{"font-heading", fontStacks[s.Typography.Heading]}, {"font-body", fontStacks[s.Typography.Body]},
		{"font-size", fmt.Sprintf("%dpx", s.Typography.BaseSize)},
		{"radius", radius[0]}, {"bubble-radius", radius[1]}, {"bubble-border", border}, {"bubble-shadow", shadow},
		{"wallpaper", wallpaper}, {"wallpaper-size", wallpaperSize},
		{"spacing", densities[s.Density]}, {"motion", s.Motion},
	}
	var b strings.Builder
	for _, v := range vars {
		b.WriteString("--yp-")
		b.WriteString(v[0])
		b.WriteByte(':')
		b.WriteString(v[1])
		b.WriteByte(';')
	}
	return b.String(), nil
}

func bubbleTreatment(s Definition) (border, shadow string) {
	switch s.Shape.Bubble {
	case "outlined":
		return "1px solid " + s.Colors.Line, "none"
	case "raised":
		return "2px solid " + s.Colors.Text, "3px 3px 0 " + s.Colors.Text
	default:
		return "none", "none"
	}
}

func wallpaperTreatment(s Definition) (image, size string, err error) {
	switch s.Wallpaper {
	case "dots":
		return "radial-gradient(" + s.Colors.Line + " 1.6px,transparent 1.8px)", "22px 22px", nil
	case "lines":
		r, g, b, err := parseHex(s.Colors.Accent)
		if err != nil {
			return "", "", err
		}
		tint := fmt.Sprintf("rgba(%d,%d,%d,0.06)", r, g, b)
		return "repeating-linear-gradient(0deg," + tint + " 0," + tint + " 1px,transparent 1px,transparent 4px)", "auto", nil
	default:
		return "none", "auto", nil
	}
}

// Stylesheet emits every first-party skin, keyed by a fixed attribute
// selector and ordered by id. A trailing rule forces motion off whenever the
// user asks the operating system for reduced motion, regardless of skin.
func Stylesheet() (string, error) {
	catalog := Catalog()
	ids := make([]string, 0, len(catalog))
	for id := range catalog {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var result strings.Builder
	result.WriteString("/* Yapuno semantic appearance tokens, schema v2 */\n")
	for _, id := range ids {
		decls, err := declarations(catalog[id])
		if err != nil {
			return "", err
		}
		result.WriteString(`[data-yp-skin="`)
		result.WriteString(id)
		result.WriteString(`"]{`)
		result.WriteString(decls)
		result.WriteString("}\n")
	}
	result.WriteString("@media (prefers-reduced-motion:reduce){[data-yp-skin]{--yp-motion:none}}\n")
	return result.String(), nil
}
