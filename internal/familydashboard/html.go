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
	selected := skin.Resolve(skinID, catalog, catalog["classic"])
	css, err := skin.CSSVariables(selected)
	if err != nil {
		return "", err
	}
	const markup = `<section class="yp-family" aria-labelledby="yp-family-title" data-yp-skin="{{.Skin}}">
<style>
.yp-family{box-sizing:border-box;{{.CSS}}--yp-shadow:0 12px 40px rgba(18,24,43,.08);background-color:var(--yp-bg);background-image:var(--yp-wallpaper);background-size:var(--yp-wallpaper-size);color:var(--yp-text);padding:clamp(1rem,3vw,2.5rem);font-family:var(--yp-font-body);font-size:var(--yp-font-size);min-height:100vh}
.yp-family *{box-sizing:border-box}
.yp-family .yp-shell{max-width:1080px;margin:auto}
.yp-family .yp-top{display:flex;align-items:center;justify-content:space-between;gap:1rem;padding-bottom:1.5rem}
.yp-family .yp-logo{font-family:var(--yp-font-heading);font-weight:850;font-size:1.45rem;letter-spacing:-.06em}
.yp-family .yp-logo span{color:var(--yp-accent-text)}
.yp-family .yp-avatar{display:grid;place-items:center;width:42px;height:42px;border-radius:50%;background:var(--yp-accent);color:var(--yp-on-accent);font-weight:800}
.yp-family .yp-kicker{text-transform:uppercase;letter-spacing:.14em;font-size:.7rem;font-weight:800;color:var(--yp-accent-text)}
.yp-family h1{font-family:var(--yp-font-heading);font-size:clamp(1.8rem,5vw,3rem);letter-spacing:-.055em;line-height:1.1;margin:.4rem 0}
.yp-family .yp-subtitle{color:var(--yp-text-muted);margin:.5rem 0 1.5rem}
.yp-family .yp-summary{display:flex;gap:1rem;align-items:center;padding:1.1rem 1.3rem;background:var(--yp-surface);border-radius:var(--yp-radius);margin-bottom:1.5rem}
.yp-family .yp-summary-icon{font-size:1.7rem}
.yp-family .yp-summary strong{display:block}
.yp-family .yp-summary small{color:var(--yp-text-muted)}
.yp-family .yp-grid{display:grid;grid-template-columns:1fr;gap:1rem}
.yp-family .yp-card{border:1px solid var(--yp-line);border-radius:var(--yp-radius);padding:1.3rem;min-width:0;box-shadow:var(--yp-shadow);background:var(--yp-bg)}
.yp-family .yp-card-head{display:flex;align-items:center;gap:.9rem}
.yp-family .yp-child-avatar{width:54px;height:54px;border-radius:var(--yp-radius);background:var(--yp-surface);display:grid;place-items:center;font-size:1.4rem;color:var(--yp-accent-text);font-weight:850}
.yp-family .yp-card h2{font-family:var(--yp-font-heading);font-size:1.2rem;margin:0;overflow-wrap:anywhere}
.yp-family .yp-card p{color:var(--yp-text-muted);font-size:.86rem;margin:.3rem 0 0}
.yp-family .yp-actions{display:grid;gap:.65rem;margin-top:1.4rem}
.yp-family .yp-action{display:flex;justify-content:space-between;align-items:center;border:1px solid var(--yp-line);border-radius:var(--yp-radius);padding:.85rem 1rem;min-height:48px;font-weight:650}
.yp-family .yp-action::after{content:"✓";color:var(--yp-accent-text);font-weight:800}
.yp-family .yp-empty{border:1px dashed var(--yp-line);padding:2rem;border-radius:var(--yp-radius)}
.yp-family .yp-note{margin-top:1.5rem;font-size:.8rem;color:var(--yp-text-muted)}
@media(min-width:640px){.yp-family .yp-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}
@media(min-width:1050px){.yp-family .yp-grid{grid-template-columns:repeat(3,minmax(0,1fr))}}
@media(prefers-reduced-motion:no-preference){.yp-family .yp-card{transition:transform .2s ease}.yp-family .yp-card:hover{transform:translateY(-3px)}}
</style>
<div class="yp-shell">
<header class="yp-top"><div class="yp-logo">ya<span>puno</span><span aria-hidden="true"> ●</span></div><div class="yp-avatar" aria-label="Demo guardian">G</div></header>
<main>
<p class="yp-kicker">Family space</p>
<h1 id="yp-family-title">Your family, your rules.</h1>
<p class="yp-subtitle">One safe place to keep your crew connected.</p>
<div class="yp-summary"><span class="yp-summary-icon" aria-hidden="true">♡</span><div><strong>{{len .Cards}} family profiles</strong><small>Permissions are scoped separately for every child</small></div></div>
{{if .Cards}}<div class="yp-grid">{{range .Cards}}<article class="yp-card"><div class="yp-card-head"><div class="yp-child-avatar" aria-hidden="true">☺</div><div><h2>{{.DisplayName}}</h2><p>Child profile</p></div></div><div class="yp-actions">{{if .Permissions.ManageDevices}}<span class="yp-action">Devices</span>{{end}}{{if .Permissions.ApproveContacts}}<span class="yp-action">Contacts</span>{{end}}{{if not .Permissions.ManageDevices}}{{if not .Permissions.ApproveContacts}}<p>No management permissions</p>{{end}}{{end}}</div></article>{{end}}</div>{{else}}<p class="yp-empty">No children available.</p>{{end}}
<p class="yp-note">Preview only · No real accounts, device pairing or editable settings yet.</p>
</main></div>
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
