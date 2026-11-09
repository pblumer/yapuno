// Package skin validates versioned, declarative UI skin definitions.
//
// A skin assigns values to fixed semantic roles (colors, typography, shape,
// wallpaper, avatar style, density, motion). Every value is either a #rrggbb
// color or a member of a closed vocabulary; skins never carry CSS, URLs,
// scripts, fonts or images. Skins describe appearance only: they cannot change
// authorization, consent, approval, privacy or status wording, which live in
// templates and domain code. See docs/adr/draft-skin-token-contract.md.
package skin

import (
	"errors"
	"fmt"
	"regexp"
	"unicode"
	"unicode/utf8"
)

// SchemaVersion 2 replaces the three-color v1 contract with semantic roles.
const SchemaVersion = 2

var (
	ErrInvalidSkin = errors.New("invalid skin definition")
	idPattern      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
	hexColor       = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// Closed vocabularies. Rendering maps each value to fixed, reviewed CSS.
var (
	fontStacks = map[string]string{
		"system":    `system-ui,-apple-system,"Segoe UI",Roboto,sans-serif`,
		"jakarta":   `"Plus Jakarta Sans",system-ui,-apple-system,sans-serif`,
		"bricolage": `"Bricolage Grotesque","Plus Jakarta Sans",system-ui,sans-serif`,
		"nunito":    `Nunito,system-ui,-apple-system,sans-serif`,
		"mono":      `"JetBrains Mono",ui-monospace,SFMono-Regular,Menlo,monospace`,
	}
	radii = map[string][2]string{ // surface radius, bubble radius
		"square": {"2px", "2px"},
		"soft":   {"16px", "20px"},
		"round":  {"24px", "26px"},
	}
	bubbleStyles = set("flat", "outlined", "raised")
	wallpapers   = set("none", "dots", "lines")
	avatars      = set("initials", "figure")
	densities    = map[string]string{"comfortable": "0.75rem", "compact": "0.5rem"}
	motions      = set("calm", "standard")
)

const (
	minBaseSize = 16 // smaller inputs trigger iOS zoom and hurt readability
	maxBaseSize = 20
	maxNameLen  = 80
)

// Definition is a first-party or vetted skin manifest.
type Definition struct {
	Version    int        `json:"version"`
	ID         string     `json:"id"`
	Name       string     `json:"name"` // display only; HTML-escaped, never emitted into CSS
	Colors     Colors     `json:"colors"`
	Typography Typography `json:"typography"`
	Shape      Shape      `json:"shape"`
	Wallpaper  string     `json:"wallpaper"` // none | dots | lines
	Avatar     string     `json:"avatar"`    // initials | figure
	Density    string     `json:"density"`   // comfortable | compact
	Motion     string     `json:"motion"`    // calm | standard; reduced-motion always wins
}

// Colors assigns #rrggbb values to semantic roles. "On" roles are the text or
// icon color placed on the matching fill.
type Colors struct {
	Background    string `json:"background"`
	Surface       string `json:"surface"`
	Text          string `json:"text"`
	TextMuted     string `json:"text_muted"`
	Line          string `json:"line"`
	Accent        string `json:"accent"`
	OnAccent      string `json:"on_accent"`
	AccentText    string `json:"accent_text"` // accent hue used as text on Background
	OwnBubble     string `json:"own_bubble"`
	OnOwnBubble   string `json:"on_own_bubble"`
	OtherBubble   string `json:"other_bubble"`
	OnOtherBubble string `json:"on_other_bubble"`
	Badge         string `json:"badge"`
	OnBadge       string `json:"on_badge"`
	DangerText    string `json:"danger_text"`
	Focus         string `json:"focus"`
}

type Typography struct {
	Heading  string `json:"heading"` // font vocabulary key
	Body     string `json:"body"`    // font vocabulary key
	BaseSize int    `json:"base_size"`
}

type Shape struct {
	Radius string `json:"radius"` // square | soft | round
	Bubble string `json:"bubble"` // flat | outlined | raised
}

type contrastRule struct {
	name    string
	fg, bg  func(Colors) string
	minimum float64
}

// contrastRules lists every foreground/background pairing the UI relies on.
// Add a rule whenever a component introduces a new pairing.
var contrastRules = []contrastRule{
	{"text/background", func(c Colors) string { return c.Text }, func(c Colors) string { return c.Background }, MinTextContrast},
	{"text/surface", func(c Colors) string { return c.Text }, func(c Colors) string { return c.Surface }, MinTextContrast},
	{"text-muted/background", func(c Colors) string { return c.TextMuted }, func(c Colors) string { return c.Background }, MinTextContrast},
	{"text-muted/surface", func(c Colors) string { return c.TextMuted }, func(c Colors) string { return c.Surface }, MinTextContrast},
	{"on-accent/accent", func(c Colors) string { return c.OnAccent }, func(c Colors) string { return c.Accent }, MinTextContrast},
	{"accent-text/background", func(c Colors) string { return c.AccentText }, func(c Colors) string { return c.Background }, MinTextContrast},
	{"on-own-bubble/own-bubble", func(c Colors) string { return c.OnOwnBubble }, func(c Colors) string { return c.OwnBubble }, MinTextContrast},
	{"on-other-bubble/other-bubble", func(c Colors) string { return c.OnOtherBubble }, func(c Colors) string { return c.OtherBubble }, MinTextContrast},
	{"on-badge/badge", func(c Colors) string { return c.OnBadge }, func(c Colors) string { return c.Badge }, MinTextContrast},
	{"danger-text/background", func(c Colors) string { return c.DangerText }, func(c Colors) string { return c.Background }, MinTextContrast},
	{"focus/background", func(c Colors) string { return c.Focus }, func(c Colors) string { return c.Background }, MinNonTextContrast},
}

// Validate fails closed on unsupported versions, unknown vocabulary values,
// malformed colors and insufficient contrast.
func Validate(s Definition) error {
	if s.Version != SchemaVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidSkin, s.Version)
	}
	if !idPattern.MatchString(s.ID) {
		return fmt.Errorf("%w: id", ErrInvalidSkin)
	}
	if err := validateName(s.Name); err != nil {
		return err
	}
	for name, value := range colorFields(s.Colors) {
		if !hexColor.MatchString(value) {
			return fmt.Errorf("%w: color %s", ErrInvalidSkin, name)
		}
	}
	if _, ok := fontStacks[s.Typography.Heading]; !ok {
		return fmt.Errorf("%w: heading font", ErrInvalidSkin)
	}
	if _, ok := fontStacks[s.Typography.Body]; !ok {
		return fmt.Errorf("%w: body font", ErrInvalidSkin)
	}
	if s.Typography.BaseSize < minBaseSize || s.Typography.BaseSize > maxBaseSize {
		return fmt.Errorf("%w: base size", ErrInvalidSkin)
	}
	if _, ok := radii[s.Shape.Radius]; !ok {
		return fmt.Errorf("%w: radius", ErrInvalidSkin)
	}
	if !bubbleStyles[s.Shape.Bubble] {
		return fmt.Errorf("%w: bubble style", ErrInvalidSkin)
	}
	if !wallpapers[s.Wallpaper] {
		return fmt.Errorf("%w: wallpaper", ErrInvalidSkin)
	}
	if !avatars[s.Avatar] {
		return fmt.Errorf("%w: avatar", ErrInvalidSkin)
	}
	if _, ok := densities[s.Density]; !ok {
		return fmt.Errorf("%w: density", ErrInvalidSkin)
	}
	if !motions[s.Motion] {
		return fmt.Errorf("%w: motion", ErrInvalidSkin)
	}
	for _, rule := range contrastRules {
		ratio, err := ContrastRatio(rule.fg(s.Colors), rule.bg(s.Colors))
		if err != nil {
			return err
		}
		if ratio < rule.minimum {
			return fmt.Errorf("%w: contrast %s is %.2f, minimum %.1f", ErrInvalidSkin, rule.name, ratio, rule.minimum)
		}
	}
	return nil
}

func validateName(name string) error {
	if name == "" || len(name) > maxNameLen || !utf8.ValidString(name) {
		return fmt.Errorf("%w: name", ErrInvalidSkin)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: name", ErrInvalidSkin)
		}
	}
	return nil
}

func colorFields(c Colors) map[string]string {
	return map[string]string{
		"background": c.Background, "surface": c.Surface, "text": c.Text, "text_muted": c.TextMuted, "line": c.Line,
		"accent": c.Accent, "on_accent": c.OnAccent, "accent_text": c.AccentText,
		"own_bubble": c.OwnBubble, "on_own_bubble": c.OnOwnBubble, "other_bubble": c.OtherBubble, "on_other_bubble": c.OnOtherBubble,
		"badge": c.Badge, "on_badge": c.OnBadge, "danger_text": c.DangerText, "focus": c.Focus,
	}
}

// Resolve returns a safe default when a stored preference is unavailable or
// invalid. An invalid fallback yields the zero Definition, which callers must
// treat as unusable.
func Resolve(preferred string, catalog map[string]Definition, fallback Definition) Definition {
	if Validate(fallback) != nil {
		return Definition{}
	}
	selected, ok := catalog[preferred]
	if !ok || selected.ID != preferred || Validate(selected) != nil {
		return fallback
	}
	return selected
}

func set(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}
