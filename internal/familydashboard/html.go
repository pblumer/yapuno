package familydashboard

import (
	"bytes"
	"html/template"

	"github.com/pblumer/yapuno/internal/skin"
)

// RenderHTML returns an accessible, mobile-first guardian dashboard fragment.
// The caller MUST authenticate the guardian and use Build to filter cards.
// This function is a renderer, not an authorization boundary or HTTP endpoint.
func RenderHTML(cards []ChildCard, skinID string) (string, error) {
	catalog := skin.Catalog()
	selected, ok := catalog[skinID]
	if !ok {
		selected = catalog["classic"]
	}
	css, err := skin.CSSVariables(selected)
	if err != nil {
		return "", err
	}
	const markup = `<section class="yp-family" aria-labelledby="yp-family-title" data-yp-skin="{{.Skin}}">
<style>
.yp-family{box-sizing:border-box;{{.CSS}}background:var(--yp-background);color:var(--yp-foreground);padding:1rem;font-family:system-ui,sans-serif}
.yp-family *{box-sizing:border-box}
.yp-family .yp-grid{display:grid;grid-template-columns:1fr;gap:var(--yp-spacing)}
.yp-family .yp-card{border:1px solid currentColor;border-radius:var(--yp-radius);padding:1rem;min-width:0}
.yp-family .yp-actions{display:flex;flex-wrap:wrap;gap:.5rem}
.yp-family .yp-action{display:inline-block;border:2px solid var(--yp-accent);border-radius:var(--yp-radius);padding:.65rem;min-height:44px}
@media(min-width:640px){.yp-family .yp-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}
@media(min-width:1000px){.yp-family .yp-grid{grid-template-columns:repeat(3,minmax(0,1fr))}}
</style>
<h1 id="yp-family-title">Family</h1>
<p>Manage each child's devices and contacts separately.</p>
{{if .Cards}}<div class="yp-grid">{{range .Cards}}<article class="yp-card"><h2>{{.DisplayName}}</h2><div class="yp-actions">{{if .Permissions.ManageDevices}}<span class="yp-action">Devices</span>{{end}}{{if .Permissions.ApproveContacts}}<span class="yp-action">Contacts</span>{{end}}</div></article>{{end}}</div>{{else}}<p>No children available.</p>{{end}}
</section>`
	// CSS is generated exclusively from validated, first-party skin definitions.
	// Strip :root selector so variables apply to the scoped family element.
	css = css[len(":root{") : len(css)-1]
	tmpl, err := template.New("dashboard").Parse(markup)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, struct {
		Skin  string
		CSS   template.CSS
		Cards []ChildCard
	}{Skin: selected.ID, CSS: template.CSS(css), Cards: cards})
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}
