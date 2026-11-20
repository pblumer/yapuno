// Package appearance stores each person's chosen skin and profile figure.
//
// A figure is drawn from fixed shapes and colors; there are no photos or
// uploads. Preferences are keyed by an authenticated subject (guardian or
// child, scoped to a tenant). This package is a persistence component, not an
// authentication or authorization boundary: callers MUST derive the subject
// from a verified server-side session, never from client input.
package appearance

import (
	"errors"
	"fmt"
	"html/template"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pblumer/yapuno/internal/skin"
)

var ErrInvalid = errors.New("invalid appearance")

const (
	KindGuardian = "guardian"
	KindChild    = "child"

	maxSubjectField = 128
	minFigureSize   = 16
	maxFigureSize   = 256

	// faceInk draws pupils and mouth; every figure color must contrast with it.
	faceInk = "#1c2540"
)

// Subject identifies whose preference this is.
type Subject struct {
	Kind     string `json:"kind"`
	TenantID string `json:"tenant_id"`
	ID       string `json:"id"`
}

// Figure is the profile picture replacement: a shape and a color, both from
// closed vocabularies.
type Figure struct {
	Shape string `json:"shape"`
	Color string `json:"color"`
}

type Preference struct {
	Skin   string `json:"skin"`
	Figure Figure `json:"figure"`
}

// Option is one selectable value. Hex is set for colors, Path for shapes.
type Option struct {
	ID   string
	Hex  string
	Path string
}

var shapes = []Option{
	{ID: "ball", Path: "M24 5c10.5 0 19 8.5 19 19s-8.5 19-19 19S5 34.5 5 24 13.5 5 24 5z"},
	{ID: "cushion", Path: "M24 5c14 0 19 5 19 19s-5 19-19 19S5 38 5 24 10 5 24 5z"},
	{ID: "flower", Path: "M24 5c3.5 0 5.5 3.5 8.5 4.5s7.5 0 8.5 3.5-2 6.5-2 11 3 7.5 2 11-5.5 2.5-8.5 3.5S27.5 43 24 43s-5.5-3.5-8.5-4.5-7.5 0-8.5-3.5 2-6.5 2-11-3-7.5-2-11 5.5-2.5 8.5-3.5S20.5 5 24 5z"},
	{ID: "ghost", Path: "M9 24C9 14 15.5 6 24 6s15 8 15 18v17c-2.5 0-2.5-2.5-5-2.5S31.5 41 29 41s-2.5-2.5-5-2.5S21.5 41 19 41s-2.5-2.5-5-2.5S11.5 41 9 41z"},
}

var colors = []Option{
	{ID: "lavender", Hex: "#dcd6f7"},
	{ID: "lime", Hex: "#d9f692"},
	{ID: "mint", Hex: "#bdece2"},
	{ID: "peach", Hex: "#ffbbac"},
	{ID: "white", Hex: "#ffffff"},
}

// Shapes and Colors return copies of the selectable vocabularies in display order.
func Shapes() []Option { return append([]Option(nil), shapes...) }
func Colors() []Option { return append([]Option(nil), colors...) }

func find(list []Option, id string) (Option, bool) {
	for _, o := range list {
		if o.ID == id {
			return o, true
		}
	}
	return Option{}, false
}

// Default is used for subjects without a stored preference.
func Default() Preference {
	return Preference{Skin: "classic", Figure: Figure{Shape: "ball", Color: "lavender"}}
}

// Validate requires a valid first-party skin and a known figure.
func Validate(p Preference) error {
	s, ok := skin.Catalog()[p.Skin]
	if !ok || s.ID != p.Skin || skin.Validate(s) != nil {
		return fmt.Errorf("%w: skin", ErrInvalid)
	}
	return validateFigure(p.Figure)
}

func validateFigure(f Figure) error {
	if _, ok := find(shapes, f.Shape); !ok {
		return fmt.Errorf("%w: shape", ErrInvalid)
	}
	if _, ok := find(colors, f.Color); !ok {
		return fmt.Errorf("%w: color", ErrInvalid)
	}
	return nil
}

func validateSubject(s Subject) error {
	if s.Kind != KindGuardian && s.Kind != KindChild {
		return fmt.Errorf("%w: subject kind", ErrInvalid)
	}
	for _, v := range []string{s.TenantID, s.ID} {
		if v == "" || len(v) > maxSubjectField || !utf8.ValidString(v) {
			return fmt.Errorf("%w: subject", ErrInvalid)
		}
		if strings.IndexFunc(v, unicode.IsControl) >= 0 {
			return fmt.Errorf("%w: subject", ErrInvalid)
		}
	}
	return nil
}

// FigureSVG renders a decorative inline SVG from fixed paths and colors. The
// output contains no links, scripts, styles or external references. The
// outline uses currentColor so it follows the skin's text color.
func FigureSVG(f Figure, size int) (template.HTML, error) {
	if err := validateFigure(f); err != nil {
		return "", err
	}
	if size < minFigureSize || size > maxFigureSize {
		return "", fmt.Errorf("%w: size", ErrInvalid)
	}
	shape, _ := find(shapes, f.Shape)
	color, _ := find(colors, f.Color)
	svg := fmt.Sprintf(`<svg class="yp-figure" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48" width="%d" height="%d" aria-hidden="true" focusable="false">`+
		`<path class="yp-figure-body" d="%s" fill="%s" stroke="currentColor" stroke-width="2"/>`+
		`<circle cx="18" cy="22" r="4.5" fill="#ffffff"/><circle cx="30" cy="22" r="4.5" fill="#ffffff"/>`+
		`<circle cx="19" cy="23" r="2.3" fill="%s"/><circle cx="31" cy="23" r="2.3" fill="%s"/>`+
		`<path d="M20 31q4 3.5 8 0" fill="none" stroke="%s" stroke-width="2.2" stroke-linecap="round"/></svg>`,
		size, size, shape.Path, color.Hex, faceInk, faceInk, faceInk)
	// All interpolated values come from the fixed vocabularies above.
	return template.HTML(svg), nil
}
