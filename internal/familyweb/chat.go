package familyweb

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/chatpage"
	"github.com/pblumer/yapuno/internal/guardianchat"
	"github.com/pblumer/yapuno/internal/sameorigin"
)

// GuardianChat stores the conversations in which a guardian takes part. It
// must authorize every call from current state for the actor it is given.
type GuardianChat interface {
	Threads(a guardianchat.Actor) []guardianchat.Thread
	Send(a guardianchat.Actor, id, clientID, body string) (guardianchat.Message, bool, error)
	History(a guardianchat.Actor, id string, after uint64, limit int) ([]guardianchat.Message, error)
	Recent(a guardianchat.Actor, id string, limit int) ([]guardianchat.Message, error)
}

// ChildNames resolves display names of children. It is used only for the
// guardian's own children and for children connected to them.
type ChildNames interface {
	ChildName(tenantID, childID string) (string, bool)
}

const (
	chatPageSize      = 50
	chatFormBodyLimit = 16 << 10 // 1000 characters, percent-encoded
	chatJSONBodyLimit = 8 << 10
)

var threadPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// NewChatPages serves the guardian's chats: with their own children, and
// with guardians of families whose children are connected to theirs. The
// guardian is always the one of the session; no request names a participant.
// These pages never show what children write to each other. looks may be nil.
func NewChatPages(auth Authenticator, chat GuardianChat, names ChildNames, looks AppearanceStore) (http.Handler, error) {
	if auth == nil || chat == nil || names == nil {
		return nil, errors.New("authenticator, chat store and child names are required")
	}
	h := &chatHandler{auth: auth, chat: chat, names: names, looks: looks}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /family/chat", h.inbox)
	mux.HandleFunc("GET /family/chat.js", chatAsset(chatpage.JS))
	mux.HandleFunc("GET /family/chat-utils.js", chatAsset(chatpage.UtilsJS))
	mux.HandleFunc("GET /family/chat/{id}", h.view)
	mux.HandleFunc("POST /family/chat/{id}/send", h.sendForm)
	mux.HandleFunc("GET /family/chat/{id}/messages", h.historyJSON)
	mux.HandleFunc("POST /family/chat/{id}/messages", h.sendJSON)
	return mux, nil
}

type chatHandler struct {
	auth  Authenticator
	chat  GuardianChat
	names ChildNames
	looks AppearanceStore
}

// scriptHeaders is headers for the one page that runs the first-party chat
// script, and for the script files themselves.
func scriptHeaders(w http.ResponseWriter) {
	headers(w)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; connect-src 'self'; style-src 'unsafe-inline'; font-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
}

func chatAsset(content []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		scriptHeaders(w)
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(content)
	}
}

func actorOf(p Session) guardianchat.Actor {
	return guardianchat.Guardian(p.TenantID, p.GuardianID)
}

// threadView is how one conversation is shown to the guardian.
type threadView struct {
	ID       string
	Name     string
	Subtitle string
	Figure   template.HTML
}

// describe names the other participant without any identifier: a child of
// the guardian by its name, another guardian through the connected children.
func (h *chatHandler) describe(t guardianchat.Thread, size int) threadView {
	v := threadView{ID: t.ID}
	subject := appearance.Subject{Kind: appearance.KindGuardian, TenantID: t.Peer.TenantID, ID: t.Peer.ID}
	switch t.Kind {
	case guardianchat.Family:
		v.Name, _ = h.names.ChildName(t.Peer.TenantID, t.Peer.ID)
		v.Subtitle = "Nur du und dein Kind"
		subject.Kind = appearance.KindChild
	case guardianchat.Parents:
		var children []string
		for _, c := range t.Via {
			if name, ok := h.names.ChildName(c.TenantID, c.ChildID); ok {
				children = append(children, name)
			}
		}
		v.Name = "Elternteil von " + strings.Join(children, " und ")
		if len(children) == 0 {
			v.Name = "Elternteil einer verbundenen Familie"
		}
		v.Subtitle = "Eure Kinder sind verbunden"
	}
	if h.looks != nil && t.Peer.Kind != authz.ParticipantUnknown {
		if figure, err := appearance.FigureSVG(h.looks.Get(subject).Figure, size); err == nil {
			v.Figure = figure
		}
	}
	return v
}

var chatInboxPage = template.Must(template.New("chat-inbox").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Yapuno – Chats</title><style>{{.CSS}}</style></head><body><main class="yp-page" data-yp-skin="{{.Skin}}"><div class="yp-shell">
<a class="yp-back" href="/family">← Zurück zur Familie</a>
<h1>Chats</h1>
<p class="yp-muted">Schreib mit deinen Kindern und mit den Eltern von Familien, deren Kinder mit deinen verbunden sind.</p>
<section aria-labelledby="yp-own"><h2 id="yp-own">Deine Kinder</h2>
{{if .Children}}<ul class="yp-threads">{{range .Children}}<li><a href="/family/chat/{{.ID}}">{{.Figure}}<span>{{.Name}}</span><span class="yp-go" aria-hidden="true">›</span></a></li>{{end}}</ul>
<p class="yp-muted">Dein Kind sieht den Chat auf seinem verbundenen Gerät unter «Deine Eltern».</p>
{{else}}<p class="yp-muted">Hier erscheinen deine Kinder.</p>{{end}}</section>
<section aria-labelledby="yp-others"><h2 id="yp-others">Andere Eltern</h2>
{{if .Parents}}<ul class="yp-threads">{{range .Parents}}<li><a href="/family/chat/{{.ID}}">{{.Figure}}<span>{{.Name}}</span><span class="yp-go" aria-hidden="true">›</span></a></li>{{end}}</ul>
{{else}}<p class="yp-muted">Andere Eltern erscheinen hier, sobald eure Kinder verbunden sind. Das richtest du unter «Kontakte» bei deinem Kind ein.</p>{{end}}</section>
<section aria-labelledby="yp-know"><h2 id="yp-know">Gut zu wissen</h2><ul class="yp-facts">
<li>Was Kinder untereinander schreiben, kannst du hier nicht lesen. Dieser Bereich zeigt nur Chats, an denen du selbst teilnimmst.</li>
<li>Ein Chat mit anderen Eltern besteht, solange eure Kinder verbunden sind. Beendet eine Familie den Kontakt, endet er und die Nachrichten werden gelöscht.</li>
<li>Mit Kindern anderer Familien kannst du nicht schreiben.</li>
<li>Nachrichten werden nach 30 Tagen gelöscht und liegen unverschlüsselt auf dem Server dieser Installation.</li>
</ul></section>
<p class="yp-muted">Vorschau mit synthetischen Profilen.</p>
</div></main></body></html>`))

const chatInboxCSS = `.yp-page ul.yp-threads{display:flex;flex-direction:column;gap:.6rem}
.yp-threads a{display:flex;align-items:center;gap:.8rem;min-height:64px;padding:.7rem .9rem;border:1px solid var(--yp-line);border-radius:var(--yp-radius);background:var(--yp-bg);color:var(--yp-text);font-weight:700;text-decoration:none;overflow-wrap:anywhere}
.yp-threads a:focus-visible{outline:3px solid var(--yp-focus);outline-offset:2px}
.yp-threads svg{flex:none}
.yp-go{margin-left:auto;color:var(--yp-accent-text);font-size:1.4rem}
`

func (h *chatHandler) inbox(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	skinID, css, err := skinnedCSS(h.looks, p, chatInboxCSS)
	if err != nil {
		http.Error(w, "Unable to display chats", http.StatusInternalServerError)
		return
	}
	var children, parents []threadView
	for _, t := range h.chat.Threads(actorOf(p)) {
		if t.Kind == guardianchat.Family {
			children = append(children, h.describe(t, 44))
		} else {
			parents = append(parents, h.describe(t, 44))
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = chatInboxPage.Execute(w, struct {
		Skin              string
		CSS               template.CSS
		Children, Parents []threadView
	}{Skin: skinID, CSS: css, Children: children, Parents: parents})
}

// thread returns one of the guardian's own current conversations.
func (h *chatHandler) thread(p Session, id string) (guardianchat.Thread, bool) {
	for _, t := range h.chat.Threads(actorOf(p)) {
		if t.ID == id {
			return t, true
		}
	}
	return guardianchat.Thread{}, false
}

// render writes the conversation page. A guardian who may not read the
// conversation is sent back to the overview without any content.
func (h *chatHandler) render(w http.ResponseWriter, r *http.Request, p Session, id string, status int, alert, draft string) {
	t, ok := h.thread(p, id)
	if !ok {
		http.Redirect(w, r, "/family/chat", http.StatusSeeOther)
		return
	}
	recent, err := h.chat.Recent(actorOf(p), id, chatPageSize)
	if err != nil {
		http.Redirect(w, r, "/family/chat", http.StatusSeeOther)
		return
	}
	clientID, err := chatpage.NewClientID()
	if err != nil {
		http.Error(w, "Unable to display chat", http.StatusInternalServerError)
		return
	}
	about := h.describe(t, 44)
	self := actorOf(p).Participant
	page := chatpage.Page{Peer: about.Name, Subtitle: about.Subtitle, PeerFigure: about.Figure,
		Home: "/family/chat", HomeLabel: "Zurück zu den Chats", Path: "/family/chat/" + id, Assets: "/family",
		Conversation: id, CSRF: p.CSRFToken, ClientID: clientID, Alert: alert, Draft: draft, Skin: "classic"}
	if h.looks != nil {
		page.Skin = h.looks.Get(guardianSubject(p)).Skin
	}
	for _, m := range recent {
		page.Messages = append(page.Messages, chatpage.NewMessage(m.Seq, m.From == self, m.Body, m.At))
		page.LastSeq = m.Seq
	}
	scriptHeaders(w)
	if err := chatpage.Render(w, status, page); err != nil {
		http.Error(w, "Unable to display chat", http.StatusInternalServerError)
	}
}

func (h *chatHandler) view(w http.ResponseWriter, r *http.Request) {
	headers(w)
	id := r.PathValue("id")
	if !threadPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	h.render(w, r, p, id, http.StatusOK, "", "")
}

// explainChatSend maps a store error to a status and a German message for
// the no-script form.
func explainChatSend(err error) (int, string) {
	switch {
	case errors.Is(err, guardianchat.ErrInvalid):
		return http.StatusBadRequest, "Diese Nachricht geht so nicht. Sie darf nicht leer sein und höchstens 1000 Zeichen haben."
	case errors.Is(err, guardianchat.ErrConflict):
		return http.StatusConflict, "Diese Nachricht wurde schon mit anderem Text gesendet. Lade die Seite neu und versuch es nochmal."
	case errors.Is(err, guardianchat.ErrRateLimited):
		return http.StatusTooManyRequests, "Das waren viele Nachrichten. Warte kurz und versuch es nochmal."
	default:
		return http.StatusServiceUnavailable, "Die Nachricht konnte nicht gespeichert werden. Sie wurde nicht gesendet."
	}
}

// sendForm is the no-script path: a normal form post followed by a redirect.
func (h *chatHandler) sendForm(w http.ResponseWriter, r *http.Request) {
	headers(w)
	id := r.PathValue("id")
	if !threadPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		http.Error(w, "Expected form data", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, chatFormBodyLimit)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	for _, field := range []string{"csrf_token", "client_id", "body"} {
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
	body := r.PostForm.Get("body")
	_, _, err = h.chat.Send(actorOf(p), id, r.PostForm.Get("client_id"), body)
	switch {
	case err == nil:
		http.Redirect(w, r, "/family/chat/"+id, http.StatusSeeOther)
	case errors.Is(err, guardianchat.ErrForbidden):
		http.Error(w, "Not allowed", http.StatusForbidden)
	default:
		status, alert := explainChatSend(err)
		h.render(w, r, p, id, status, alert, body)
	}
}

// apiSession authenticates a scripted request and answers in JSON.
func (h *chatHandler) apiSession(w http.ResponseWriter, r *http.Request) (Session, string, bool) {
	headers(w)
	id := r.PathValue("id")
	if !threadPattern.MatchString(id) {
		chatpage.JSONError(w, http.StatusNotFound, "not_found")
		return Session{}, "", false
	}
	p, err := h.auth.Authenticate(r)
	if err != nil || p.GuardianID == "" || p.TenantID == "" || p.CSRFToken == "" {
		chatpage.JSONError(w, http.StatusUnauthorized, "signed_out")
		return Session{}, "", false
	}
	return p, id, true
}

// sendJSON is the scripted path. It requires the exact HTTPS origin and the
// session's CSRF token in a header that cross-site pages cannot set.
func (h *chatHandler) sendJSON(w http.ResponseWriter, r *http.Request) {
	p, id, ok := h.apiSession(w, r)
	if !ok {
		return
	}
	if !sameorigin.Request(r) {
		chatpage.JSONError(w, http.StatusForbidden, "origin")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		chatpage.JSONError(w, http.StatusUnsupportedMediaType, "content_type")
		return
	}
	token := r.Header.Get("X-CSRF-Token")
	if len(token) != len(p.CSRFToken) || subtle.ConstantTimeCompare([]byte(token), []byte(p.CSRFToken)) != 1 {
		chatpage.JSONError(w, http.StatusForbidden, "csrf")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, chatJSONBodyLimit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var input struct {
		ClientID string `json:"client_id"`
		Body     string `json:"body"`
	}
	if err := dec.Decode(&input); err != nil {
		chatpage.JSONError(w, http.StatusBadRequest, "invalid")
		return
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		chatpage.JSONError(w, http.StatusBadRequest, "invalid")
		return
	}
	m, duplicate, err := h.chat.Send(actorOf(p), id, input.ClientID, input.Body)
	switch {
	case err == nil:
		status := http.StatusCreated
		if duplicate {
			status = http.StatusOK
		}
		chatpage.WriteJSON(w, status, struct {
			Seq       uint64 `json:"seq"`
			At        string `json:"at"`
			Duplicate bool   `json:"duplicate"`
		}{m.Seq, chatpage.NewWireMessage(m.Seq, true, "", m.At).At, duplicate})
	case errors.Is(err, guardianchat.ErrForbidden):
		chatpage.JSONError(w, http.StatusForbidden, "not_allowed")
	case errors.Is(err, guardianchat.ErrInvalid):
		chatpage.JSONError(w, http.StatusBadRequest, "invalid")
	case errors.Is(err, guardianchat.ErrConflict):
		chatpage.JSONError(w, http.StatusConflict, "conflict")
	case errors.Is(err, guardianchat.ErrRateLimited):
		w.Header().Set("Retry-After", "60")
		chatpage.JSONError(w, http.StatusTooManyRequests, "slow_down")
	default:
		chatpage.JSONError(w, http.StatusServiceUnavailable, "not_stored")
	}
}

// historyJSON returns messages after a cursor. The response says only whether
// a message is the reader's own; it carries no identifiers.
func (h *chatHandler) historyJSON(w http.ResponseWriter, r *http.Request) {
	p, id, ok := h.apiSession(w, r)
	if !ok {
		return
	}
	var after uint64
	if values, given := r.URL.Query()["after"]; given {
		var err error
		if len(values) != 1 {
			chatpage.JSONError(w, http.StatusBadRequest, "invalid")
			return
		}
		if after, err = strconv.ParseUint(values[0], 10, 64); err != nil {
			chatpage.JSONError(w, http.StatusBadRequest, "invalid")
			return
		}
	}
	found, err := h.chat.History(actorOf(p), id, after, guardianchat.MaxPage)
	if err != nil {
		if errors.Is(err, guardianchat.ErrForbidden) {
			chatpage.JSONError(w, http.StatusForbidden, "not_allowed")
			return
		}
		chatpage.JSONError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	self := actorOf(p).Participant
	out := make([]chatpage.WireMessage, 0, len(found))
	for _, m := range found {
		out = append(out, chatpage.NewWireMessage(m.Seq, m.From == self, m.Body, m.At))
	}
	chatpage.WriteJSON(w, http.StatusOK, map[string][]chatpage.WireMessage{"messages": out})
}
