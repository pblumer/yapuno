// Package lookpage renders the "Mein Look" picker (theme, figure, color) for
// any authenticated subject. It is a renderer and form reader only: the
// caller authenticates, supplies the session's CSRF token, decides whose look
// is edited and stores the result.
package lookpage

import (
	"fmt"
	"html/template"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/fontassets"
	"github.com/pblumer/yapuno/internal/skin"
)

// Page describes one rendering of the picker.
type Page struct {
	Action    string // form target of the same origin
	BackHref  string
	BackLabel string
	CSRF      string
	Saved     bool
	Look      appearance.Preference
}

// User-facing labels (German UI copy). Unknown ids fall back to the skin name.
var (
	skinLabels = map[string][2]string{
		"classic":  {"Klassisch", "Ruhig und schlicht"},
		"playful":  {"Verspielt", "Warm und weich"},
		"vivid":    {"Bunt", "Kräftig, mit Konturen"},
		"night":    {"Nacht", "Dunkel und ruhig"},
		"cloud":    {"Wolke", "Weich, grosse Schrift"},
		"terminal": {"Terminal", "Code-Look, eckig"},
	}
	shapeLabels = map[string]string{"ball": "Kugel", "cushion": "Kissen", "flower": "Blume", "ghost": "Geist"}
	colorLabels = map[string]string{"lavender": "Lavendel", "lime": "Limette", "mint": "Mint", "peach": "Pfirsich", "white": "Weiss"}
)

type skinChoice struct {
	ID, Label, Description string
	Checked                bool
	Background, Accent     string
	OwnBubble              string
}

type figureChoice struct {
	ID, Label string
	Checked   bool
	Small     template.HTML
	Large     template.HTML
}

type colorChoice struct {
	ID, Label, Hex string
	Checked        bool
}

const baseCSS = `body{margin:0;background-color:var(--yp-bg);color:var(--yp-text);font-family:var(--yp-font-body);font-size:var(--yp-font-size)}
.yp-look{box-sizing:border-box;max-width:40rem;margin:0 auto;padding:1rem 1rem 2.5rem}
.yp-look *{box-sizing:border-box}
.yp-back{display:inline-flex;align-items:center;min-height:44px;color:var(--yp-accent-text);font-weight:700;text-decoration:none}
.yp-back:focus-visible,.yp-save:focus-visible{outline:3px solid var(--yp-focus);outline-offset:2px}
.yp-look h1{font-family:var(--yp-font-heading);font-size:2rem;letter-spacing:-.03em;margin:.25rem 0 1rem}
.yp-saved{padding:.75rem 1rem;border-radius:var(--yp-radius);background:var(--yp-surface);font-weight:700}
.yp-preview{display:flex;gap:1rem;align-items:center;padding:1rem;margin-bottom:1.5rem;border:2px solid var(--yp-line);border-radius:var(--yp-radius);background-color:var(--yp-bg);background-image:var(--yp-wallpaper);background-size:var(--yp-wallpaper-size);color:var(--yp-text);font-family:var(--yp-font-body)}
.yp-preview .yp-fig{display:none;flex:none}
.yp-bubbles{flex:1;min-width:0;display:flex;flex-direction:column;gap:.5rem}
.yp-bubble{margin:0;padding:.6rem .9rem;border-radius:var(--yp-bubble-radius);border:var(--yp-bubble-border);box-shadow:var(--yp-bubble-shadow);font-weight:600;max-width:85%}
.yp-other{align-self:flex-start;background:var(--yp-other-bubble);color:var(--yp-on-other-bubble)}
.yp-own{align-self:flex-end;background:var(--yp-own-bubble);color:var(--yp-on-own-bubble)}
.yp-look .yp-figure-body{fill:var(--yp-figure-fill)}
.yp-look fieldset{border:0;padding:0;margin:0 0 1.5rem;min-width:0}
.yp-look legend{font-family:var(--yp-font-heading);font-size:1.25rem;font-weight:800;margin-bottom:.6rem;padding:0}
.yp-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:.6rem}
.yp-row{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:.6rem}
.yp-colors{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:.5rem}
@media (min-width:420px){.yp-colors{grid-template-columns:repeat(5,minmax(0,1fr))}}
.yp-choice{position:relative;min-width:0}
.yp-radio{position:absolute;opacity:0;width:1px;height:1px;margin:0}
.yp-radio+label{display:flex;flex-direction:column;gap:.3rem;min-height:48px;height:100%;padding:.7rem;border:2px solid var(--yp-line);border-radius:var(--yp-radius);background:var(--yp-surface);color:var(--yp-text);cursor:pointer;overflow-wrap:break-word}
.yp-row .yp-radio+label,.yp-colors .yp-radio+label{align-items:center;text-align:center;padding:.5rem .25rem;font-size:.85rem}
.yp-radio+label small{color:var(--yp-text-muted)}
.yp-radio:checked+label{border-color:var(--yp-accent-text);box-shadow:0 0 0 2px var(--yp-accent-text)}
.yp-radio:checked+label::after{content:"✓";position:absolute;top:.35rem;right:.5rem;color:var(--yp-accent-text);font-weight:800}
.yp-radio:focus-visible+label{outline:3px solid var(--yp-focus);outline-offset:2px}
.yp-swatches{display:flex;gap:.25rem}
.yp-swatch{width:1.25rem;height:1.25rem;border-radius:50%;border:1.5px solid var(--yp-text)}
.yp-dot{width:2rem;height:2rem;border-radius:50%;border:2px solid var(--yp-text)}
.yp-note{color:var(--yp-text-muted);line-height:1.45;margin:.4rem 0}
.yp-save{display:block;width:100%;min-height:52px;margin-top:1rem;border:0;border-radius:var(--yp-radius);background:var(--yp-accent);color:var(--yp-on-accent);font:inherit;font-weight:800;cursor:pointer}
@media (prefers-reduced-motion:no-preference){.yp-radio+label{transition:box-shadow .15s ease,border-color .15s ease}}
@media (prefers-reduced-motion:reduce){.yp-look *{transition:none;animation:none}}
`

// pageCSS assembles the stylesheet from validated skin tokens and the fixed
// figure vocabulary only. The :has() rules give a live preview without
// JavaScript; browsers without :has() keep showing the saved look.
func pageCSS(look appearance.Preference, ids []string, catalog map[string]skin.Definition) (template.CSS, error) {
	var b strings.Builder
	current, err := skin.Declarations(catalog[look.Skin])
	if err != nil {
		return "", err
	}
	savedHex := ""
	for _, c := range appearance.Colors() {
		if c.ID == look.Figure.Color {
			savedHex = c.Hex
		}
	}
	b.WriteString(fontassets.FaceCSS(fontassets.Keys()...))
	b.WriteString(":root{" + current + "--yp-figure-fill:" + savedHex + ";}\n")
	b.WriteString(baseCSS)
	b.WriteString(".yp-preview .yp-fig-" + look.Figure.Shape + "{display:block}\n")
	for _, id := range ids {
		decls, err := skin.Declarations(catalog[id])
		if err != nil {
			return "", err
		}
		b.WriteString(".yp-look:has(#yp-skin-" + id + ":checked) .yp-preview{" + decls + "}\n")
	}
	for _, s := range appearance.Shapes() {
		b.WriteString(".yp-look:has(#yp-shape-" + s.ID + ":checked) .yp-preview .yp-fig{display:none}\n")
		b.WriteString(".yp-look:has(#yp-shape-" + s.ID + ":checked) .yp-preview .yp-fig-" + s.ID + "{display:block}\n")
	}
	for _, c := range appearance.Colors() {
		b.WriteString(".yp-look:has(#yp-color-" + c.ID + ":checked){--yp-figure-fill:" + c.Hex + "}\n")
	}
	// Every interpolated value is a catalog id, vocabulary id or validated token.
	return template.CSS(b.String()), nil
}

var page = template.Must(template.New("look").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Yapuno – Mein Look</title><style>{{.CSS}}</style></head><body>
<main class="yp-look" data-yp-skin="{{.Skin}}">
<a class="yp-back" href="{{.BackHref}}">{{.BackLabel}}</a>
<h1>Mein Look</h1>
{{if .Saved}}<p class="yp-saved" role="status">Gespeichert. Dein Look ist aktiv.</p>{{end}}
<section class="yp-preview" aria-label="Vorschau">
<div>{{range .Shapes}}<span class="yp-fig yp-fig-{{.ID}}">{{.Large}}</span>{{end}}</div>
<div class="yp-bubbles"><p class="yp-bubble yp-other">Neuer Look?</p><p class="yp-bubble yp-own">Gefällt mir!</p></div>
</section>
<form method="post" action="{{.Action}}">
<input type="hidden" name="csrf_token" value="{{.CSRF}}">
<fieldset><legend>Thema</legend><div class="yp-grid">{{range .Skins}}<div class="yp-choice"><input class="yp-radio" type="radio" id="yp-skin-{{.ID}}" name="skin" value="{{.ID}}"{{if .Checked}} checked{{end}} required><label for="yp-skin-{{.ID}}"><span class="yp-swatches" aria-hidden="true"><span class="yp-swatch" style="background:{{.Background}}"></span><span class="yp-swatch" style="background:{{.Accent}}"></span><span class="yp-swatch" style="background:{{.OwnBubble}}"></span></span><strong>{{.Label}}</strong><small>{{.Description}}</small></label></div>{{end}}</div></fieldset>
<fieldset><legend>Figur</legend><div class="yp-row">{{range .Shapes}}<div class="yp-choice"><input class="yp-radio" type="radio" id="yp-shape-{{.ID}}" name="shape" value="{{.ID}}"{{if .Checked}} checked{{end}} required><label for="yp-shape-{{.ID}}">{{.Small}}<span>{{.Label}}</span></label></div>{{end}}</div></fieldset>
<fieldset><legend>Farbe</legend><div class="yp-colors">{{range .Colors}}<div class="yp-choice"><input class="yp-radio" type="radio" id="yp-color-{{.ID}}" name="color" value="{{.ID}}"{{if .Checked}} checked{{end}} required><label for="yp-color-{{.ID}}"><span class="yp-dot" aria-hidden="true" style="background:{{.Hex}}"></span><span>{{.Label}}</span></label></div>{{end}}</div></fieldset>
<p class="yp-note">Nur das Aussehen ändert sich. Freigaben, Kontakte und Sicherheitshinweise bleiben gleich.</p>
<p class="yp-note">Kein Foto nötig: Deine Figur ist dein Profilbild.</p>
<button class="yp-save" type="submit">Speichern</button>
</form>
</main></body></html>`))

// Render writes the complete page. It refuses a look that is not valid, so a
// caller cannot be made to interpolate unchecked values into the stylesheet.
// Nothing is written when an error is returned.
func Render(w io.Writer, p Page) error {
	if err := appearance.Validate(p.Look); err != nil {
		return fmt.Errorf("lookpage: %w", err)
	}
	catalog := skin.Catalog()
	ids := make([]string, 0, len(catalog))
	for id := range catalog {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	css, err := pageCSS(p.Look, ids, catalog)
	if err != nil {
		return err
	}
	view := struct {
		Page
		Skin   string
		CSS    template.CSS
		Skins  []skinChoice
		Shapes []figureChoice
		Colors []colorChoice
	}{Page: p, Skin: p.Look.Skin, CSS: css}
	for _, id := range ids {
		def := catalog[id]
		label, ok := skinLabels[id]
		if !ok {
			label = [2]string{def.Name, ""}
		}
		view.Skins = append(view.Skins, skinChoice{ID: id, Label: label[0], Description: label[1], Checked: id == p.Look.Skin,
			Background: def.Colors.Background, Accent: def.Colors.Accent, OwnBubble: def.Colors.OwnBubble})
	}
	for _, s := range appearance.Shapes() {
		f := appearance.Figure{Shape: s.ID, Color: p.Look.Figure.Color}
		small, err := appearance.FigureSVG(f, 44)
		if err != nil {
			return err
		}
		large, err := appearance.FigureSVG(f, 88)
		if err != nil {
			return err
		}
		view.Shapes = append(view.Shapes, figureChoice{ID: s.ID, Label: shapeLabels[s.ID], Checked: s.ID == p.Look.Figure.Shape, Small: small, Large: large})
	}
	for _, c := range appearance.Colors() {
		view.Colors = append(view.Colors, colorChoice{ID: c.ID, Label: colorLabels[c.ID], Hex: c.Hex, Checked: c.ID == p.Look.Figure.Color})
	}
	var out strings.Builder
	if err := page.Execute(&out, view); err != nil {
		return err
	}
	_, err = io.WriteString(w, out.String())
	return err
}

// Parse reads exactly one skin, shape and color from a posted form. It does
// not check that the values exist; the appearance store validates them.
func Parse(form url.Values) (appearance.Preference, bool) {
	for _, field := range []string{"skin", "shape", "color"} {
		if len(form[field]) != 1 {
			return appearance.Preference{}, false
		}
	}
	return appearance.Preference{
		Skin:   form.Get("skin"),
		Figure: appearance.Figure{Shape: form.Get("shape"), Color: form.Get("color")},
	}, true
}
