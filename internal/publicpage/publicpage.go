// Package publicpage holds what the pages without a session share: sign-in
// and registration. Nobody is signed in there, so they always use the classic
// skin; they carry no script and load nothing from elsewhere.
package publicpage

import (
	"html/template"
	"net/http"

	"github.com/pblumer/yapuno/internal/fontassets"
	"github.com/pblumer/yapuno/internal/skin"
)

// Open and Close frame a page. The template data needs Title and CSS.
const (
	Open  = `<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Yapuno – {{.Title}}</title><style>{{.CSS}}</style></head><body><main class="yp-page" data-yp-skin="classic"><div class="yp-shell">`
	Close = `<p class="yp-muted">Vorschau mit synthetischen Profilen.</p></div></main></body></html>`
)

const componentCSS = `body{margin:0}
.yp-page{box-sizing:border-box;min-height:100vh;background-color:var(--yp-bg);background-image:var(--yp-wallpaper);background-size:var(--yp-wallpaper-size);color:var(--yp-text);font-family:var(--yp-font-body);font-size:var(--yp-font-size);line-height:1.45}
.yp-page *{box-sizing:border-box}
.yp-shell{max-width:40rem;margin:0 auto;padding:1rem 1rem 2.5rem}
.yp-page h1{font-family:var(--yp-font-heading);font-size:1.9rem;line-height:1.15;letter-spacing:-.03em;margin:.75rem 0 .5rem;overflow-wrap:anywhere}
.yp-page h2{font-family:var(--yp-font-heading);font-size:1.2rem;margin:0 0 .6rem}
.yp-muted{color:var(--yp-text-muted);margin:.3rem 0}
.yp-page section{margin:1.25rem 0;padding:1rem;border:1px solid var(--yp-line);border-radius:var(--yp-radius);background:var(--yp-surface)}
.yp-page ul{margin:0;padding-left:1.3rem;color:var(--yp-text-muted)}
.yp-page li{margin:.4rem 0}
.yp-page form{display:flex;flex-direction:column;gap:.6rem}
.yp-page p+form{margin-top:.9rem}
.yp-page label.yp-field{display:flex;flex-direction:column;gap:.3rem;font-weight:700}
.yp-page input[type=email],.yp-page input[type=text]{width:100%;min-height:48px;padding:.6rem .8rem;border:2px solid var(--yp-text-muted);border-radius:var(--yp-radius);background:var(--yp-bg);color:var(--yp-text);font:inherit}
.yp-check{display:flex;gap:.6rem;align-items:flex-start;min-height:44px;padding:.5rem 0}
.yp-check input{width:1.4rem;height:1.4rem;flex:none;margin:0;accent-color:var(--yp-accent)}
.yp-page button.yp-quiet{border:2px solid var(--yp-text-muted);background:var(--yp-surface);color:var(--yp-text)}
.yp-page button{min-height:48px;padding:.7rem 1rem;border:0;border-radius:var(--yp-radius);background:var(--yp-accent);color:var(--yp-on-accent);font:inherit;font-weight:800;cursor:pointer}
.yp-link{display:inline-flex;align-items:center;min-height:44px;color:var(--yp-accent-text);font-weight:700}
.yp-page button:focus-visible,.yp-page input:focus-visible,.yp-link:focus-visible{outline:3px solid var(--yp-focus);outline-offset:2px}
.yp-status{padding:.8rem 1rem;border:2px solid var(--yp-line);border-radius:var(--yp-radius);background:var(--yp-surface);font-weight:700}
`

// Styles returns the validated tokens of the classic skin, its self-hosted
// fonts and the fixed component rules.
func Styles() (template.CSS, error) {
	def := skin.Catalog()["classic"]
	decls, err := skin.Declarations(def)
	if err != nil {
		return "", err
	}
	fonts := fontassets.FaceCSS(def.Typography.Heading, def.Typography.Body)
	return template.CSS(fonts + ".yp-page{" + decls + "}\n" + componentCSS), nil
}

// Headers sets what every page without a session sends: no caching, no
// framing, no referrer, and a policy that allows inline styles, fonts of
// this origin and forms to this origin only.
func Headers(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; font-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
}
