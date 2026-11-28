package childweb

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/chatpage"
	"github.com/pblumer/yapuno/internal/childsession"
	"github.com/pblumer/yapuno/internal/fontassets"
	"github.com/pblumer/yapuno/internal/sameorigin"
	"github.com/pblumer/yapuno/internal/skin"
	"github.com/pblumer/yapuno/internal/webpush"
)

//go:embed sw.js
var serviceWorkerJS []byte

//go:embed push.js
var pushJS []byte

//go:embed push-utils.js
var pushUtilsJS []byte

// PushKey is the server's public VAPID key.
type PushKey interface {
	PublicKey() string
}

// PushSubscriptions stores one push endpoint per approved device. The pages
// only ever name the device of the verified session.
type PushSubscriptions interface {
	Subscribe(d webpush.Device, endpoint string) error
	Unsubscribe(d webpush.Device) error
	Subscribed(d webpush.Device) bool
}

// Notices answers a device what it was told about by its last pushes, and
// forgets it. It is asked only for the device of the verified session.
type Notices interface {
	Take(d webpush.Device) webpush.Kind
}

// Push enables content-free notifications. Key and Subscriptions are needed
// together; both nil switches the feature off. Notices, optional, lets the
// worker ask what a push was about. See docs/adr/draft-web-push.md.
type Push struct {
	Key           PushKey
	Subscriptions PushSubscriptions
	Notices       Notices
}

func (p Push) enabled() bool { return p.Key != nil && p.Subscriptions != nil }

func (p Push) valid() bool { return (p.Key == nil) == (p.Subscriptions == nil) }

func device(s childsession.Session) webpush.Device {
	return webpush.Device{TenantID: s.TenantID, ChildID: s.ChildID, DeviceID: s.DeviceID}
}

// The manifest names the child pages as an installable app. It is public and
// contains nothing about anybody. The scope is the whole origin so that
// pairing, which lives under /pair, stays inside the installed app.
const manifestJSON = `{
  "id": "/child",
  "name": "Yapuno",
  "short_name": "Yapuno",
  "description": "Deine Crew. Dein Chat.",
  "lang": "de",
  "start_url": "/child",
  "scope": "/",
  "display": "standalone",
  "background_color": "#ffffff",
  "theme_color": "#3355aa",
  "icons": [
    {"src": "/child/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
    {"src": "/child/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
    {"src": "/child/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"}
  ]
}
`

// appHead is the part of every child page's head that makes it installable.
const appHead = `<link rel="manifest" href="/child/manifest.webmanifest"><link rel="icon" href="/child/icon-192.png" type="image/png"><link rel="apple-touch-icon" href="/child/apple-touch-icon.png"><meta name="theme-color" content="#3355aa"><meta name="apple-mobile-web-app-title" content="Yapuno">`

func publicHeaders(w http.ResponseWriter, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
}

func manifest(w http.ResponseWriter, _ *http.Request) {
	publicHeaders(w, "application/manifest+json")
	_, _ = io.WriteString(w, manifestJSON)
}

func iconHandler(size int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		publicHeaders(w, "image/png")
		_, _ = w.Write(icon(size))
	}
}

// serviceWorker is served for the scope /child/. It must be revalidated so a
// changed worker reaches devices; it may load the notification icon only.
func serviceWorker(w http.ResponseWriter, _ *http.Request) {
	publicHeaders(w, "text/javascript; charset=utf-8")
	// The worker loads its own helpers and asks its own origin what a push
	// was about; nothing else.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; script-src 'self'; connect-src 'self'")
	_, _ = w.Write(serviceWorkerJS)
}

const notificationsCSS = `.yp-actions{display:flex;flex-wrap:wrap;gap:.6rem;margin-top:1rem}
.yp-actions button{border-color:var(--yp-accent);background:var(--yp-accent);color:var(--yp-on-accent);font-weight:800}
.yp-actions button[hidden]{display:none}
.yp-note h2{margin-top:0}
.yp-steps{margin:.4rem 0 0;padding-left:1.3rem}
.yp-steps li{margin:.3rem 0}
`

var notificationsPage = template.Must(template.New("child-notifications").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><meta name="referrer" content="no-referrer"><title>Benachrichtigungen – Yapuno</title>` + appHead + `<style>{{.CSS}}</style></head><body>
<main class="yp-child" data-yp-skin="{{.Skin}}" data-key="{{.Key}}" data-csrf="{{.CSRF}}" data-subscribed="{{if .Subscribed}}1{{else}}0{{end}}"><div class="yp-shell">
<header class="yp-top"><div class="yp-logo">ya<span>puno</span></div></header>
<h1>Benachrich&shy;tigungen</h1>
<p>Wenn dir jemand schreibt, zeigt dein Gerät: «Du hast eine neue Nachricht.» Wer geschrieben hat und was, steht nicht drin. Das siehst du erst in Yapuno.</p>
<p id="push-status" class="yp-status" role="status" aria-live="polite">{{if .Subscribed}}Benachrichtigungen sind auf diesem Gerät eingeschaltet.{{else}}Benachrichtigungen sind auf diesem Gerät ausgeschaltet.{{end}}</p>
<div class="yp-actions"><button type="button" id="push-on" hidden>Einschalten</button><button type="button" id="push-off" hidden>Ausschalten</button></div>
<noscript><p class="yp-note">Für Benachrichtigungen braucht Yapuno JavaScript. Bitte schalte es für diese Seite ein.</p></noscript>
<section id="push-install" class="yp-note" hidden aria-labelledby="push-install-title">
<h2 id="push-install-title">Auf dem iPhone oder iPad</h2>
<p>Benachrichtigungen gehen dort nur, wenn Yapuno auf dem Home-Bildschirm ist:</p>
<ol class="yp-steps"><li>Tippe in Safari auf «Teilen» (das Quadrat mit dem Pfeil).</li><li>Tippe auf «Zum Home-Bildschirm» und dann auf «Hinzufügen».</li><li>Öffne Yapuno über das neue Symbol. Deine Eltern verbinden das Gerät dort noch einmal mit dir.</li><li>Komm dann wieder hierher und tippe auf «Einschalten».</li></ol>
</section>
<p class="yp-muted">Du kannst Benachrichtigungen jederzeit hier oder in den Einstellungen deines Geräts ausschalten. Wenn du dich abmeldest, gehen sie auch aus.</p>
<p><a class="yp-link" href="/child">Zurück</a></p>
</div></main>
<script src="/child/push-utils.js" defer></script>
<script src="/child/push.js" defer></script>
</body></html>`))

// skinCSS returns the validated first-party skin of the child together with
// the fixed component rules of the child pages.
func (h *handler) skinCSS(s childsession.Session, extra string) (string, template.CSS, error) {
	pref := appearance.Default()
	if h.opt.Looks != nil {
		pref = h.opt.Looks.Get(appearance.Subject{Kind: appearance.KindChild, TenantID: s.TenantID, ID: s.ChildID})
	}
	catalog := skin.Catalog()
	def := skin.Resolve(pref.Skin, catalog, catalog["classic"])
	tokens, err := skin.CSSVariables(def)
	if err != nil {
		return "", "", err
	}
	decls := strings.TrimSuffix(strings.TrimPrefix(tokens, ":root{"), "}")
	return def.ID, template.CSS(fontassets.FaceCSS(def.Typography.Heading, def.Typography.Body) + ".yp-child{" + decls + "}\n" + homeCSS + extra), nil
}

func (h *handler) notifications(w http.ResponseWriter, r *http.Request) {
	s, err := h.sessions.Authenticate(r)
	if err != nil {
		headers(w, false)
		http.Redirect(w, r, "/child", http.StatusSeeOther)
		return
	}
	id, css, err := h.skinCSS(s, notificationsCSS)
	if err != nil {
		headers(w, false)
		http.Error(w, "Unable to display page", http.StatusInternalServerError)
		return
	}
	headers(w, true)
	w.Header().Set("Content-Security-Policy", cspWorker)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = notificationsPage.Execute(w, struct {
		Skin, Key, CSRF string
		Subscribed      bool
		CSS             template.CSS
	}{id, h.opt.Push.Key.PublicKey(), s.CSRFToken, h.opt.Push.Subscriptions.Subscribed(device(s)), css})
}

// pushRequest authenticates a scripted request of the notifications page:
// exact HTTPS origin, JSON, a session, and its CSRF token in a header that
// cross-site pages cannot set.
func (h *handler) pushRequest(w http.ResponseWriter, r *http.Request) (childsession.Session, bool) {
	headers(w, false)
	if !sameorigin.Request(r) {
		chatpage.JSONError(w, http.StatusForbidden, "origin")
		return childsession.Session{}, false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		chatpage.JSONError(w, http.StatusUnsupportedMediaType, "content_type")
		return childsession.Session{}, false
	}
	s, err := h.sessions.Authenticate(r)
	if err != nil {
		chatpage.JSONError(w, http.StatusUnauthorized, "signed_out")
		return childsession.Session{}, false
	}
	token := r.Header.Get("X-CSRF-Token")
	if len(token) != len(s.CSRFToken) || subtle.ConstantTimeCompare([]byte(token), []byte(s.CSRFToken)) != 1 {
		chatpage.JSONError(w, http.StatusForbidden, "csrf")
		return childsession.Session{}, false
	}
	return s, true
}

// decodeStrict reads exactly one JSON object with known fields only.
func decodeStrict(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return false
	}
	var extra any
	return dec.Decode(&extra) == io.EOF
}

func (h *handler) subscribe(w http.ResponseWriter, r *http.Request) {
	s, ok := h.pushRequest(w, r)
	if !ok {
		return
	}
	var input struct {
		Endpoint string `json:"endpoint"`
	}
	if !decodeStrict(w, r, &input) {
		chatpage.JSONError(w, http.StatusBadRequest, "invalid")
		return
	}
	switch err := h.opt.Push.Subscriptions.Subscribe(device(s), input.Endpoint); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, webpush.ErrInvalid):
		chatpage.JSONError(w, http.StatusBadRequest, "invalid")
	case errors.Is(err, webpush.ErrForbidden):
		chatpage.JSONError(w, http.StatusForbidden, "not_allowed")
	case errors.Is(err, webpush.ErrFull):
		chatpage.JSONError(w, http.StatusConflict, "full")
	default:
		chatpage.JSONError(w, http.StatusServiceUnavailable, "unavailable")
	}
}

func (h *handler) unsubscribe(w http.ResponseWriter, r *http.Request) {
	s, ok := h.pushRequest(w, r)
	if !ok {
		return
	}
	var input struct{}
	if !decodeStrict(w, r, &input) {
		chatpage.JSONError(w, http.StatusBadRequest, "invalid")
		return
	}
	if err := h.opt.Push.Subscriptions.Unsubscribe(device(s)); err != nil {
		chatpage.JSONError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// notice tells the worker of the signed-in device which kinds of notices
// were delivered to it. The answer holds three booleans and nothing else;
// the worker turns them into fixed sentences.
func (h *handler) notice(w http.ResponseWriter, r *http.Request) {
	headers(w, false)
	if !sameorigin.Request(r) {
		chatpage.JSONError(w, http.StatusForbidden, "origin")
		return
	}
	s, err := h.sessions.Authenticate(r)
	if err != nil {
		chatpage.JSONError(w, http.StatusUnauthorized, "signed_out")
		return
	}
	k := h.opt.Push.Notices.Take(device(s))
	chatpage.WriteJSON(w, http.StatusOK, map[string]bool{
		"message": k&webpush.KindMessage != 0,
		"turn":    k&webpush.KindTurn != 0,
		"invite":  k&webpush.KindInvite != 0,
	})
}
