package familyweb

import (
	"crypto/subtle"
	"errors"
	"html/template"
	"mime"
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/skin"
)

// AppearanceStore persists per-subject looks. Implementations must validate
// preferences; the handler additionally derives the subject from the session.
type AppearanceStore interface {
	Get(appearance.Subject) appearance.Preference
	Set(appearance.Subject, appearance.Preference) error
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

// NewLookPages serves the "Mein Look" page for the authenticated guardian.
// The subject is always taken from the session, never from the form.
func NewLookPages(auth Authenticator, looks AppearanceStore) (http.Handler, error) {
	if auth == nil || looks == nil {
		return nil, errors.New("authenticator and appearance store are required")
	}
	h := &lookHandler{auth: auth, looks: looks}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /family/look", h.view)
	mux.HandleFunc("POST /family/look", h.save)
	return mux, nil
}

type lookHandler struct {
	auth  Authenticator
	looks AppearanceStore
}

func guardianSubject(p Session) appearance.Subject {
	return appearance.Subject{Kind: appearance.KindGuardian, TenantID: p.TenantID, ID: p.GuardianID}
}

func (h *lookHandler) session(w http.ResponseWriter, r *http.Request) (Session, bool) {
	p, err := h.auth.Authenticate(r)
	if err != nil || p.GuardianID == "" || p.TenantID == "" || p.CSRFToken == "" {
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return Session{}, false
	}
	return p, true
}

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

type lookView struct {
	Token  string
	Skin   string
	Saved  bool
	CSS    template.CSS
	Skins  []skinChoice
	Shapes []figureChoice
	Colors []colorChoice
}

func declarationsOf(def skin.Definition) (string, error) {
	css, err := skin.CSSVariables(def)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimPrefix(css, ":root{"), "}"), nil
}

const lookBaseCSS = `body{margin:0;background-color:var(--yp-bg);color:var(--yp-text);font-family:var(--yp-font-body);font-size:var(--yp-font-size)}
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
func pageCSS(pref appearance.Preference, ids []string, catalog map[string]skin.Definition) (template.CSS, error) {
	var b strings.Builder
	current, err := declarationsOf(catalog[pref.Skin])
	if err != nil {
		return "", err
	}
	savedHex := ""
	for _, c := range appearance.Colors() {
		if c.ID == pref.Figure.Color {
			savedHex = c.Hex
		}
	}
	b.WriteString(":root{" + current + "--yp-figure-fill:" + savedHex + ";}\n")
	b.WriteString(lookBaseCSS)
	b.WriteString(".yp-preview .yp-fig-" + pref.Figure.Shape + "{display:block}\n")
	for _, id := range ids {
		decls, err := declarationsOf(catalog[id])
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

var lookPage = template.Must(template.New("look").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Yapuno – Mein Look</title><style>{{.CSS}}</style></head><body>
<main class="yp-look" data-yp-skin="{{.Skin}}">
<a class="yp-back" href="/family">← Zurück zur Familie</a>
<h1>Mein Look</h1>
{{if .Saved}}<p class="yp-saved" role="status">Gespeichert. Dein Look ist aktiv.</p>{{end}}
<section class="yp-preview" aria-label="Vorschau">
<div>{{range .Shapes}}<span class="yp-fig yp-fig-{{.ID}}">{{.Large}}</span>{{end}}</div>
<div class="yp-bubbles"><p class="yp-bubble yp-other">Neuer Look?</p><p class="yp-bubble yp-own">Gefällt mir!</p></div>
</section>
<form method="post" action="/family/look">
<input type="hidden" name="csrf_token" value="{{.Token}}">
<fieldset><legend>Thema</legend><div class="yp-grid">{{range .Skins}}<div class="yp-choice"><input class="yp-radio" type="radio" id="yp-skin-{{.ID}}" name="skin" value="{{.ID}}"{{if .Checked}} checked{{end}} required><label for="yp-skin-{{.ID}}"><span class="yp-swatches" aria-hidden="true"><span class="yp-swatch" style="background:{{.Background}}"></span><span class="yp-swatch" style="background:{{.Accent}}"></span><span class="yp-swatch" style="background:{{.OwnBubble}}"></span></span><strong>{{.Label}}</strong><small>{{.Description}}</small></label></div>{{end}}</div></fieldset>
<fieldset><legend>Figur</legend><div class="yp-row">{{range .Shapes}}<div class="yp-choice"><input class="yp-radio" type="radio" id="yp-shape-{{.ID}}" name="shape" value="{{.ID}}"{{if .Checked}} checked{{end}} required><label for="yp-shape-{{.ID}}">{{.Small}}<span>{{.Label}}</span></label></div>{{end}}</div></fieldset>
<fieldset><legend>Farbe</legend><div class="yp-colors">{{range .Colors}}<div class="yp-choice"><input class="yp-radio" type="radio" id="yp-color-{{.ID}}" name="color" value="{{.ID}}"{{if .Checked}} checked{{end}} required><label for="yp-color-{{.ID}}"><span class="yp-dot" aria-hidden="true" style="background:{{.Hex}}"></span><span>{{.Label}}</span></label></div>{{end}}</div></fieldset>
<p class="yp-note">Nur das Aussehen ändert sich. Freigaben, Kontakte und Sicherheitshinweise bleiben gleich.</p>
<p class="yp-note">Kein Foto nötig: Deine Figur ist dein Profilbild.</p>
<button class="yp-save" type="submit">Speichern</button>
</form>
</main></body></html>`))

func (h *lookHandler) view(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := h.session(w, r)
	if !ok {
		return
	}
	pref := h.looks.Get(guardianSubject(p))
	catalog := skin.Catalog()
	ids := make([]string, 0, len(catalog))
	for id := range catalog {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	view := lookView{Token: p.CSRFToken, Skin: pref.Skin, Saved: r.URL.Query().Get("saved") == "1"}
	css, err := pageCSS(pref, ids, catalog)
	if err != nil {
		http.Error(w, "Unable to display look", http.StatusInternalServerError)
		return
	}
	view.CSS = css
	for _, id := range ids {
		def := catalog[id]
		label, ok := skinLabels[id]
		if !ok {
			label = [2]string{def.Name, ""}
		}
		view.Skins = append(view.Skins, skinChoice{ID: id, Label: label[0], Description: label[1], Checked: id == pref.Skin,
			Background: def.Colors.Background, Accent: def.Colors.Accent, OwnBubble: def.Colors.OwnBubble})
	}
	for _, s := range appearance.Shapes() {
		f := appearance.Figure{Shape: s.ID, Color: pref.Figure.Color}
		small, err := appearance.FigureSVG(f, 44)
		if err != nil {
			http.Error(w, "Unable to display look", http.StatusInternalServerError)
			return
		}
		large, err := appearance.FigureSVG(f, 88)
		if err != nil {
			http.Error(w, "Unable to display look", http.StatusInternalServerError)
			return
		}
		view.Shapes = append(view.Shapes, figureChoice{ID: s.ID, Label: shapeLabels[s.ID], Checked: s.ID == pref.Figure.Shape, Small: small, Large: large})
	}
	for _, c := range appearance.Colors() {
		view.Colors = append(view.Colors, colorChoice{ID: c.ID, Label: colorLabels[c.ID], Hex: c.Hex, Checked: c.ID == pref.Figure.Color})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = lookPage.Execute(w, view)
}

func (h *lookHandler) save(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := h.session(w, r)
	if !ok {
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		http.Error(w, "Expected form data", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	for _, field := range []string{"csrf_token", "skin", "shape", "color"} {
		if len(r.PostForm[field]) != 1 {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}
	}
	token := r.PostForm.Get("csrf_token")
	if len(token) != len(p.CSRFToken) || subtle.ConstantTimeCompare([]byte(token), []byte(p.CSRFToken)) != 1 {
		http.Error(w, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	pref := appearance.Preference{
		Skin:   r.PostForm.Get("skin"),
		Figure: appearance.Figure{Shape: r.PostForm.Get("shape"), Color: r.PostForm.Get("color")},
	}
	if err := h.looks.Set(guardianSubject(p), pref); err != nil {
		if errors.Is(err, appearance.ErrInvalid) {
			http.Error(w, "Invalid look", http.StatusBadRequest)
			return
		}
		http.Error(w, "Unable to save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/family/look?saved=1", http.StatusSeeOther)
}
