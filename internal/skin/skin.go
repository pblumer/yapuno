// Package skin validates versioned, declarative UI skin definitions.
// Skins describe appearance only; they cannot change authorization or behavior.
package skin

import (
	"errors"
	"regexp"
)

const SchemaVersion = 1

var (
	ErrInvalidSkin = errors.New("invalid skin definition")
	idPattern      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
	hexColor       = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// Definition is a first-party or vetted skin manifest. Never interpret tokens
// as executable CSS, HTML, URLs, or scripts.
type Definition struct {
	Version int     `json:"version"`
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Palette Palette `json:"palette"`
	Density string  `json:"density"`
	Shape   string  `json:"shape"`
}

type Palette struct {
	Background string `json:"background"`
	Foreground string `json:"foreground"`
	Accent     string `json:"accent"`
}

// Validate fails closed on unsupported versions and token values.
func Validate(s Definition) error {
	if s.Version != SchemaVersion || !idPattern.MatchString(s.ID) || s.Name == "" || len(s.Name) > 80 ||
		!hexColor.MatchString(s.Palette.Background) || !hexColor.MatchString(s.Palette.Foreground) || !hexColor.MatchString(s.Palette.Accent) ||
		(s.Density != "comfortable" && s.Density != "compact") ||
		(s.Shape != "rounded" && s.Shape != "square") {
		return ErrInvalidSkin
	}
	return nil
}

// Resolve returns a safe default when a stored preference is unavailable or invalid.
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
