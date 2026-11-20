// Package childweb serves the child-facing entry point: a login shell for
// paired devices and a minimal home page for an authenticated child.
//
// Identity comes only from a childsession.Session. Nothing on these pages is
// selected by a client-supplied child, tenant or device identifier.
package childweb

import (
	"bytes"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/childsession"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/skin"
)

//go:embed shell.html
var shellHTML []byte

//go:embed app.js
var appJS []byte

//go:embed sig-utils.js
var sigUtilsJS []byte

// Sessions authenticates paired child devices.
type Sessions interface {
	Challenge(deviceID string) (string, error)
	Login(w http.ResponseWriter, deviceID, nonce string, signature []byte) error
	Authenticate(r *http.Request) (childsession.Session, error)
	Logout(w http.ResponseWriter, r *http.Request)
}

// Children resolves the authenticated child's own display name.
type Children interface {
	ChildName(tenantID, childID string) (string, bool)
}

// Contacts lists a child's mutually approved contacts.
type Contacts interface {
	ContactsOf(contacts.Party) []contacts.Entry
}

// Looks returns a saved look.
type Looks interface {
	Get(appearance.Subject) appearance.Preference
}

// Options are optional collaborators; nil values disable the feature.
type Options struct {
	Contacts Contacts
	Looks    Looks
}

type handler struct {
	sessions Sessions
	children Children
	opt      Options
}

// New mounts the child pages under /child.
func New(sessions Sessions, children Children, opt Options) (http.Handler, error) {
	if sessions == nil || children == nil {
		return nil, errors.New("child sessions and child directory are required")
	}
	h := &handler{sessions: sessions, children: children, opt: opt}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /child", h.home)
	mux.HandleFunc("GET /child/app.js", h.asset(appJS))
	mux.HandleFunc("GET /child/sig-utils.js", h.asset(sigUtilsJS))
	mux.HandleFunc("POST /child/session/challenge", h.challenge)
	mux.HandleFunc("POST /child/session/login", h.login)
	mux.HandleFunc("POST /child/session/logout", h.logout)
	return mux, nil
}

func headers(w http.ResponseWriter, scripts bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	csp := "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
	if scripts {
		csp = "default-src 'none'; script-src 'self'; connect-src 'self'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
	}
	w.Header().Set("Content-Security-Policy", csp)
}

func (h *handler) asset(content []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		headers(w, true)
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(content)
	}
}

type sessionRequest struct {
	DeviceID  string `json:"device_id"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}

// readJSON accepts only same-origin HTTPS JSON requests with a small body,
// no unknown fields and no trailing data.
func readJSON(w http.ResponseWriter, r *http.Request) (sessionRequest, bool) {
	if r.Header.Get("Origin") != "https://"+r.Host {
		http.Error(w, "Same-origin HTTPS required", http.StatusForbidden)
		return sessionRequest{}, false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
		return sessionRequest{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var input sessionRequest
	if err := dec.Decode(&input); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return sessionRequest{}, false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return sessionRequest{}, false
	}
	return input, true
}

func (h *handler) challenge(w http.ResponseWriter, r *http.Request) {
	headers(w, false)
	input, ok := readJSON(w, r)
	if !ok {
		return
	}
	if input.Nonce != "" || input.Signature != "" {
		http.Error(w, "Unexpected fields", http.StatusBadRequest)
		return
	}
	nonce, err := h.sessions.Challenge(input.DeviceID)
	switch {
	case errors.Is(err, childsession.ErrInvalid):
		http.Error(w, "Invalid device", http.StatusBadRequest)
		return
	case err != nil:
		http.Error(w, "Login temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(map[string]string{"nonce": nonce}); err != nil {
		http.Error(w, "Login temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b.Bytes())
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	headers(w, false)
	input, ok := readJSON(w, r)
	if !ok {
		return
	}
	// Every authentication failure, including a malformed signature, gets the
	// same answer.
	var signature []byte
	if len(input.Signature) <= 512 {
		signature, _ = base64.RawURLEncoding.DecodeString(input.Signature)
	}
	err := h.sessions.Login(w, input.DeviceID, input.Nonce, signature)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, childsession.ErrUnauthenticated):
		http.Error(w, "Login failed", http.StatusUnauthorized)
	default:
		http.Error(w, "Login temporarily unavailable", http.StatusServiceUnavailable)
	}
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	headers(w, false)
	s, err := h.sessions.Authenticate(r)
	if err != nil {
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		http.Error(w, "Expected form data", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil || len(r.PostForm["csrf_token"]) != 1 {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	token := r.PostForm.Get("csrf_token")
	if len(token) != len(s.CSRFToken) || subtle.ConstantTimeCompare([]byte(token), []byte(s.CSRFToken)) != 1 {
		http.Error(w, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	h.sessions.Logout(w, r)
	// The marker stops the login shell from signing the device in again at once.
	http.Redirect(w, r, "/child?signed_out=1", http.StatusSeeOther)
}

const homeCSS = `body{margin:0}
.yp-child{box-sizing:border-box;min-height:100vh;background-color:var(--yp-bg);background-image:var(--yp-wallpaper);background-size:var(--yp-wallpaper-size);color:var(--yp-text);font-family:var(--yp-font-body);font-size:var(--yp-font-size);line-height:1.45}
.yp-child *{box-sizing:border-box}
.yp-shell{max-width:34rem;margin:0 auto;padding:1.25rem 1rem 2.5rem}
.yp-top{display:flex;align-items:center;justify-content:space-between;gap:1rem}
.yp-logo{font-family:var(--yp-font-heading);font-weight:850;font-size:1.4rem;letter-spacing:-.05em}
.yp-logo span{color:var(--yp-accent-text)}
.yp-child h1{font-family:var(--yp-font-heading);font-size:2.2rem;line-height:1.1;letter-spacing:-.035em;margin:1.2rem 0 .3rem;overflow-wrap:anywhere}
.yp-child h2{font-family:var(--yp-font-heading);font-size:1.3rem;margin:1.6rem 0 .7rem}
.yp-muted{color:var(--yp-text-muted);margin:.4rem 0}
.yp-crew{list-style:none;margin:0;padding:0;display:flex;flex-direction:column;gap:.6rem}
.yp-crew li{display:flex;align-items:center;gap:.8rem;min-height:64px;padding:.7rem .9rem;border-radius:var(--yp-radius);background:var(--yp-surface);border:var(--yp-bubble-border);box-shadow:var(--yp-bubble-shadow);font-weight:700;overflow-wrap:anywhere}
.yp-initial{flex:none;display:grid;place-items:center;width:44px;height:44px;border-radius:50%;background:var(--yp-accent);color:var(--yp-on-accent);font-weight:800}
.yp-empty{padding:1rem;border:2px dashed var(--yp-line);border-radius:var(--yp-radius);color:var(--yp-text-muted)}
.yp-note{margin-top:1.6rem;padding:1rem;border-radius:var(--yp-radius);background:var(--yp-surface)}
.yp-child form{margin-top:1.6rem}
.yp-child button{min-height:48px;padding:.7rem 1rem;border:2px solid var(--yp-text-muted);border-radius:var(--yp-radius);background:var(--yp-bg);color:var(--yp-text);font:inherit;font-weight:700;cursor:pointer}
.yp-child button:focus-visible{outline:3px solid var(--yp-focus);outline-offset:2px}
`

var homePage = template.Must(template.New("child-home").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><meta name="referrer" content="no-referrer"><title>Yapuno</title><style>{{.CSS}}</style></head><body>
<main class="yp-child" data-yp-skin="{{.Skin}}"><div class="yp-shell">
<header class="yp-top"><div class="yp-logo">ya<span>puno</span></div>{{.Figure}}</header>
<h1>Hoi {{.Name}}!</h1>
<p class="yp-muted">Schön, dass du da bist.</p>
<h2>Deine Crew</h2>
{{if .Crew}}<ul class="yp-crew">{{range .Crew}}<li><span class="yp-initial" aria-hidden="true">{{.Initial}}</span><span>{{.Name}}</span></li>{{end}}</ul>{{else}}<p class="yp-empty">Noch niemand in deiner Crew. Deine Eltern können dich mit anderen Kindern verbinden.</p>{{end}}
<p class="yp-muted">In deiner Crew sind nur Kinder, denen beide Familien zugestimmt haben. Fremde können dich nicht finden.</p>
<p class="yp-note">Nachrichten schreiben kommt bald. Das hier ist eine Vorschau.</p>
<form method="post" action="/child/session/logout"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><button type="submit">Auf diesem Gerät abmelden</button></form>
</div></main></body></html>`))

type crewMember struct {
	Name    string
	Initial string
}

func (h *handler) home(w http.ResponseWriter, r *http.Request) {
	s, err := h.sessions.Authenticate(r)
	if err != nil {
		h.shell(w)
		return
	}
	name, ok := h.children.ChildName(s.TenantID, s.ChildID)
	if !ok {
		h.shell(w)
		return
	}
	pref := appearance.Default()
	if h.opt.Looks != nil {
		pref = h.opt.Looks.Get(appearance.Subject{Kind: appearance.KindChild, TenantID: s.TenantID, ID: s.ChildID})
	}
	catalog := skin.Catalog()
	def := skin.Resolve(pref.Skin, catalog, catalog["classic"])
	tokens, err := skin.CSSVariables(def)
	if err != nil {
		http.Error(w, "Unable to display page", http.StatusInternalServerError)
		return
	}
	figure, err := appearance.FigureSVG(pref.Figure, 52)
	if err != nil {
		figure = ""
	}
	var crew []crewMember
	if h.opt.Contacts != nil {
		for _, c := range h.opt.Contacts.ContactsOf(contacts.Party{TenantID: s.TenantID, ChildID: s.ChildID}) {
			initial := ""
			for _, r := range c.Name {
				initial = strings.ToUpper(string(r))
				break
			}
			crew = append(crew, crewMember{Name: c.Name, Initial: initial})
		}
	}
	decls := strings.TrimSuffix(strings.TrimPrefix(tokens, ":root{"), "}")
	headers(w, false)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = homePage.Execute(w, struct {
		Skin, Name, CSRF string
		CSS              template.CSS
		Figure           template.HTML
		Crew             []crewMember
	}{
		Skin: def.ID, Name: name, CSRF: s.CSRFToken,
		// Validated first-party skin tokens and fixed component rules only.
		CSS:    template.CSS(".yp-child{" + decls + "}\n" + homeCSS),
		Figure: figure, Crew: crew,
	})
}

// shell is static: it contains no child data and starts the key-based login.
func (h *handler) shell(w http.ResponseWriter) {
	headers(w, true)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(shellHTML)
}
