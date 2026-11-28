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
	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/chatpage"
	"github.com/pblumer/yapuno/internal/childsession"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/fontassets"
	"github.com/pblumer/yapuno/internal/sameorigin"
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

// Contacts lists a child's mutually approved contacts and resolves the other
// party of one of the child's own relationships.
type Contacts interface {
	ContactsOf(contacts.Party) []contacts.Entry
	Grant(id string, self contacts.Party) (contacts.Party, authz.ContactGrant, bool)
	// BlockedBy lists the contacts this child blocked itself; Block suspends
	// one of the child's own active contacts.
	BlockedBy(contacts.Party) []contacts.Entry
	Block(self contacts.Party, id string) error
}

// Looks reads and stores looks. The handler only ever stores the look of the
// authenticated child.
type Looks interface {
	Get(appearance.Subject) appearance.Preference
	Set(appearance.Subject, appearance.Preference) error
}

// Options are optional collaborators; nil values disable the feature.
type Options struct {
	Contacts Contacts
	Looks    Looks
	// Messages enables chats. It requires Contacts, which names the peer.
	Messages Messages
	// Reports lets a child report one message to its own guardians. It
	// requires Messages.
	Reports Reports
	// ReportNotices says that guardians are e-mailed about a new report, so
	// the child is told. It requires Reports.
	ReportNotices bool
	// Family lists the child's conversations with its own guardians. Their
	// messages go through Messages like every other conversation.
	Family FamilyChats
	// Games enables chess with contacts. It requires Contacts, which names
	// the other child.
	Games Games
}

// FamilyThread is the conversation of the authenticated child with one of
// its own guardians. Name is how the guardian is shown to the child.
type FamilyThread struct {
	ID       string
	Name     string
	Guardian appearance.Subject
}

// FamilyChats must compute the list from current state for exactly the
// child and device of the session: grants, guardian status, device approval.
type FamilyChats interface {
	FamilyThreads(s childsession.Session) []FamilyThread
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
	if opt.Messages != nil && opt.Contacts == nil && opt.Family == nil {
		return nil, errors.New("messaging requires the contact store")
	}
	if opt.Family != nil && opt.Messages == nil {
		return nil, errors.New("family conversations require messaging")
	}
	if opt.Reports != nil && opt.Messages == nil {
		return nil, errors.New("reporting requires messaging")
	}
	if opt.ReportNotices && opt.Reports == nil {
		return nil, errors.New("report notices require reporting")
	}
	if opt.Games != nil && opt.Contacts == nil {
		return nil, errors.New("games require the contact store")
	}
	h := &handler{sessions: sessions, children: children, opt: opt}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /child", h.home)
	mux.HandleFunc("GET /child/app.js", h.asset(appJS))
	mux.HandleFunc("GET /child/sig-utils.js", h.asset(sigUtilsJS))
	mux.HandleFunc("POST /child/session/challenge", h.challenge)
	mux.HandleFunc("POST /child/session/login", h.login)
	mux.HandleFunc("POST /child/session/logout", h.logout)
	if opt.Looks != nil {
		mux.HandleFunc("GET /child/look", h.lookView)
		mux.HandleFunc("POST /child/look", h.lookSave)
	}
	if opt.Messages != nil {
		mux.HandleFunc("GET /child/chat.js", h.asset(chatpage.JS))
		mux.HandleFunc("GET /child/chat-utils.js", h.asset(chatpage.UtilsJS))
		mux.HandleFunc("GET /child/chat/{id}", h.chat)
		mux.HandleFunc("POST /child/chat/{id}/send", h.sendForm)
		mux.HandleFunc("GET /child/chat/{id}/messages", h.historyJSON)
		mux.HandleFunc("POST /child/chat/{id}/messages", h.sendJSON)
	}
	if opt.Games != nil {
		h.mountPlay(mux)
	}
	// Blocking and reporting exist for contacts between children only.
	if opt.Messages != nil && opt.Contacts != nil {
		mux.HandleFunc("GET /child/chat/{id}/safety", h.safety)
		mux.HandleFunc("POST /child/chat/{id}/block", h.block)
		if opt.Reports != nil {
			mux.HandleFunc("POST /child/chat/{id}/report", h.report)
		}
	}
	return mux, nil
}

func headers(w http.ResponseWriter, scripts bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	csp := "default-src 'none'; style-src 'unsafe-inline'; font-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
	if scripts {
		csp = "default-src 'none'; script-src 'self'; connect-src 'self'; style-src 'unsafe-inline'; font-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
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
	if !sameorigin.Request(r) {
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
.yp-crew li:has(a){padding:0}
.yp-crew a{flex:1;display:flex;align-items:center;gap:.8rem;min-height:64px;padding:.7rem .9rem;border-radius:var(--yp-radius);color:var(--yp-text);text-decoration:none}
.yp-crew a:focus-visible{outline:3px solid var(--yp-focus);outline-offset:2px}
.yp-crew a.yp-play-link{flex:none;flex-direction:column;justify-content:center;gap:0;min-width:64px;padding:.4rem .6rem;border-left:2px solid var(--yp-line);border-radius:0 var(--yp-radius) var(--yp-radius) 0;font-size:1.6rem;line-height:1}
.yp-crew a.yp-play-link small{font-size:.7rem;font-weight:800;color:var(--yp-accent-text);text-align:center}
.yp-go{margin-left:auto;color:var(--yp-accent-text);font-size:1.4rem}
.yp-crew svg{flex:none}
.yp-me{display:grid;place-items:center;min-width:52px;min-height:52px;border-radius:50%;color:var(--yp-text)}
.yp-link{display:inline-flex;align-items:center;min-height:48px;padding:.7rem 1rem;border-radius:var(--yp-radius);background:var(--yp-accent);color:var(--yp-on-accent);font-weight:800;text-decoration:none}
.yp-me:focus-visible,.yp-link:focus-visible{outline:3px solid var(--yp-focus);outline-offset:2px}
.yp-initial{flex:none;display:grid;place-items:center;width:44px;height:44px;border-radius:50%;background:var(--yp-accent);color:var(--yp-on-accent);font-weight:800}
.yp-empty{padding:1rem;border:2px dashed var(--yp-line);border-radius:var(--yp-radius);color:var(--yp-text-muted)}
.yp-note{margin-top:1.6rem;padding:1rem;border-radius:var(--yp-radius);background:var(--yp-surface)}
.yp-child form{margin-top:1.6rem}
.yp-child button{min-height:48px;padding:.7rem 1rem;border:2px solid var(--yp-text-muted);border-radius:var(--yp-radius);background:var(--yp-bg);color:var(--yp-text);font:inherit;font-weight:700;cursor:pointer}
.yp-child button:focus-visible{outline:3px solid var(--yp-focus);outline-offset:2px}
.yp-status,.yp-alert{padding:.8rem 1rem;border-radius:var(--yp-radius);background:var(--yp-surface);font-weight:700}
.yp-status{border:2px solid var(--yp-line)}
.yp-alert{border:2px solid var(--yp-danger-text)}
`

var homePage = template.Must(template.New("child-home").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><meta name="referrer" content="no-referrer"><title>Yapuno</title><style>{{.CSS}}</style></head><body>
<main class="yp-child" data-yp-skin="{{.Skin}}"><div class="yp-shell">
<header class="yp-top"><div class="yp-logo">ya<span>puno</span></div>{{if .LookHref}}<a class="yp-me" href="{{.LookHref}}" aria-label="Mein Look">{{.Figure}}</a>{{else}}{{.Figure}}{{end}}</header>
<h1>Hoi {{.Name}}!</h1>
{{if .Notice}}<p class="yp-status" role="status">{{.Notice}}</p>{{end}}<p class="yp-muted">Schön, dass du da bist.</p>
{{if .Parents}}<h2>Deine Eltern</h2><ul class="yp-crew">{{range .Parents}}<li><a href="/child/chat/{{.Chat}}">{{template "face" .}}<span>{{.Name}}</span><span class="yp-go" aria-hidden="true">›</span></a></li>{{end}}</ul><p class="yp-muted">Was ihr hier schreibt, sehen nur du und dieser Elternteil.</p>{{end}}
<h2>Deine Crew</h2>
{{if .Crew}}<ul class="yp-crew">{{range .Crew}}<li>{{if .Chat}}<a href="/child/chat/{{.Chat}}">{{template "face" .}}<span>{{.Name}}</span><span class="yp-go" aria-hidden="true">›</span></a>{{else}}{{template "face" .}}<span>{{.Name}}</span>{{end}}{{if .Play}}<a class="yp-play-link" href="/child/play/{{.Play}}" aria-label="Schach mit {{.Name}}{{if .PlayNote}}: {{.PlayNote}}{{end}}"><span aria-hidden="true">♞&#xFE0E;</span>{{if .PlayNote}}<small aria-hidden="true">{{.PlayNote}}</small>{{end}}</a>{{end}}</li>{{end}}</ul>{{else}}<p class="yp-empty">Noch niemand in deiner Crew. Deine Eltern können dich mit anderen Kindern verbinden.</p>{{end}}
<p class="yp-muted">In deiner Crew sind nur Kinder, denen beide Familien zugestimmt haben. Fremde können dich nicht finden.</p>
{{if .Blocked}}<h2>Blockiert</h2><ul class="yp-crew">{{range .Blocked}}<li><span class="yp-initial" aria-hidden="true">{{.Initial}}</span><span>{{.Name}}</span></li>{{end}}</ul><p class="yp-muted">Ihr könnt euch nicht schreiben. Nur deine Eltern können eine Blockierung aufheben.</p>{{end}}
<p class="yp-note">{{if .Chats}}Tippe auf einen Namen, um zu schreiben. Das hier ist eine Vorschau.{{else}}Nachrichten schreiben kommt bald. Das hier ist eine Vorschau.{{end}}</p>
{{if .LookHref}}<p><a class="yp-link" href="{{.LookHref}}">Mein Look ändern</a></p>{{end}}
<form method="post" action="/child/session/logout"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><button type="submit">Auf diesem Gerät abmelden</button></form>
</div></main></body></html>{{define "face"}}{{if .Figure}}{{.Figure}}{{else}}<span class="yp-initial" aria-hidden="true">{{.Initial}}</span>{{end}}{{end}}`))

type crewMember struct {
	Name     string
	Initial  string
	Chat     string        // conversation id when messaging is enabled
	Play     string        // contact id when the two may play chess
	PlayNote string        // "Einladung", "Du bist am Zug" or empty
	Figure   template.HTML // the contact's figure, when looks are enabled
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
	var crew, blocked []crewMember
	self := contacts.Party{TenantID: s.TenantID, ChildID: s.ChildID}
	if h.opt.Contacts != nil {
		for _, c := range h.opt.Contacts.BlockedBy(self) {
			blocked = append(blocked, crewMember{Name: c.Name, Initial: initialOf(c.Name)})
		}
		for _, c := range h.opt.Contacts.ContactsOf(self) {
			initial := initialOf(c.Name)
			member := crewMember{Name: c.Name, Initial: initial, Figure: h.peerFigure(self, c.ID)}
			if h.opt.Messages != nil {
				member.Chat = c.ID
			}
			if note, ok := h.playNote(s, c.ID); ok {
				member.Play, member.PlayNote = c.ID, note
			}
			crew = append(crew, member)
		}
	}
	lookHref := ""
	if h.opt.Looks != nil {
		lookHref = "/child/look"
	}
	decls := strings.TrimSuffix(strings.TrimPrefix(tokens, ":root{"), "}")
	headers(w, false)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = homePage.Execute(w, struct {
		Skin, Name, CSRF string
		Notice           string
		CSS              template.CSS
		Figure           template.HTML
		Crew, Blocked    []crewMember
		Parents          []crewMember
		Chats            bool
		LookHref         string
	}{
		Skin: def.ID, Name: name, CSRF: s.CSRFToken, Notice: fixedNotice(r, homeNotices), Blocked: blocked,
		// Validated first-party skin tokens and fixed component rules only.
		CSS:    template.CSS(fontassets.FaceCSS(def.Typography.Heading, def.Typography.Body) + ".yp-child{" + decls + "}\n" + homeCSS),
		Figure: figure, Crew: crew, Parents: h.parents(s), Chats: h.opt.Messages != nil, LookHref: lookHref,
	})
}

// parents lists the child's conversations with its own guardians.
func (h *handler) parents(s childsession.Session) []crewMember {
	if h.opt.Family == nil {
		return nil
	}
	var out []crewMember
	for _, t := range h.opt.Family.FamilyThreads(s) {
		out = append(out, crewMember{Name: t.Name, Initial: initialOf(t.Name), Chat: t.ID, Figure: h.guardianFigure(t.Guardian)})
	}
	return out
}

// guardianFigure returns the figure a guardian chose. Only the figure is
// shown to the child, never the guardian's theme or identifiers.
func (h *handler) guardianFigure(subject appearance.Subject) template.HTML {
	if h.opt.Looks == nil || subject.Kind != appearance.KindGuardian {
		return ""
	}
	figure, err := appearance.FigureSVG(h.opt.Looks.Get(subject).Figure, 44)
	if err != nil {
		return ""
	}
	return figure
}

func initialOf(name string) string {
	for _, r := range name {
		return strings.ToUpper(string(r))
	}
	return ""
}

// peerFigure returns the figure of the other child in one of self's own
// active relationships. A figure is shown to mutually approved contacts only;
// the contact's theme is never exposed. The other child's identifiers stay on
// the server.
func (h *handler) peerFigure(self contacts.Party, relationshipID string) template.HTML {
	if h.opt.Looks == nil || h.opt.Contacts == nil {
		return ""
	}
	peer, grant, ok := h.opt.Contacts.Grant(relationshipID, self)
	if !ok || !authz.CanContact(grant, self.Participant(), peer.Participant()) {
		return ""
	}
	look := h.opt.Looks.Get(appearance.Subject{Kind: appearance.KindChild, TenantID: peer.TenantID, ID: peer.ChildID})
	figure, err := appearance.FigureSVG(look.Figure, 44)
	if err != nil {
		return ""
	}
	return figure
}

// shell is static: it contains no child data and starts the key-based login.
func (h *handler) shell(w http.ResponseWriter) {
	headers(w, true)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(shellHTML)
}
