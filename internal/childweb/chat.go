package childweb

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/childsession"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/messages"
	"github.com/pblumer/yapuno/internal/skin"
)

//go:embed chat.js
var chatJS []byte

//go:embed chat-utils.js
var chatUtilsJS []byte

// Messages stores and returns messages. It must authorize every call from
// current contact, profile and device state.
type Messages interface {
	Send(p messages.Principal, conversationID, clientID, body string) (messages.Message, bool, error)
	History(p messages.Principal, conversationID string, after uint64, limit int) ([]messages.Message, error)
	Recent(p messages.Principal, conversationID string, limit int) ([]messages.Message, error)
}

const (
	chatPageSize  = 50
	formBodyLimit = 16 << 10 // 1000 characters, percent-encoded
	jsonBodyLimit = 8 << 10
)

var conversationPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func principal(s childsession.Session) messages.Principal {
	return messages.Principal{Party: contacts.Party{TenantID: s.TenantID, ChildID: s.ChildID}, DeviceID: s.DeviceID}
}

// peerName returns the display name of the other child in one of the
// authenticated child's own active conversations.
func (h *handler) peerName(s childsession.Session, conversationID string) (string, bool) {
	for _, c := range h.opt.Contacts.ContactsOf(contacts.Party{TenantID: s.TenantID, ChildID: s.ChildID}) {
		if c.ID == conversationID {
			return c.Name, true
		}
	}
	return "", false
}

const chatCSS = `body{margin:0}
.yp-chat{box-sizing:border-box;min-height:100vh;display:flex;flex-direction:column;background-color:var(--yp-bg);background-image:var(--yp-wallpaper);background-size:var(--yp-wallpaper-size);color:var(--yp-text);font-family:var(--yp-font-body);font-size:var(--yp-font-size);line-height:1.4}
.yp-chat *{box-sizing:border-box}
.yp-sr{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap}
.yp-head{display:flex;align-items:center;gap:.6rem;padding:.6rem .75rem;padding-top:max(.6rem,env(safe-area-inset-top));background:var(--yp-surface);border-bottom:1px solid var(--yp-line)}
.yp-back{flex:none;display:grid;place-items:center;min-width:44px;min-height:44px;border-radius:var(--yp-radius);color:var(--yp-accent-text);font-weight:800;text-decoration:none}
.yp-head h1{font-family:var(--yp-font-heading);font-size:1.15rem;margin:0;overflow-wrap:anywhere}
.yp-head p{margin:0;font-size:.8rem;color:var(--yp-text-muted)}
.yp-banner{margin:0;padding:.6rem .9rem;background:var(--yp-surface);border-bottom:2px solid var(--yp-danger-text);font-weight:700}
.yp-list{flex:1;list-style:none;margin:0;padding:1rem .75rem;display:flex;flex-direction:column;gap:.5rem}
.yp-msg{max-width:82%;display:flex;flex-direction:column;gap:.15rem}
.yp-mine{align-self:flex-end;align-items:flex-end}
.yp-theirs{align-self:flex-start;align-items:flex-start}
.yp-text{margin:0;padding:.6rem .85rem;border-radius:var(--yp-bubble-radius);border:var(--yp-bubble-border);box-shadow:var(--yp-bubble-shadow);white-space:pre-wrap;overflow-wrap:anywhere}
.yp-mine .yp-text{background:var(--yp-own-bubble);color:var(--yp-on-own-bubble)}
.yp-theirs .yp-text{background:var(--yp-other-bubble);color:var(--yp-on-other-bubble)}
.yp-meta{margin:0;font-size:.75rem;font-weight:600;color:var(--yp-text-muted)}
.yp-empty{align-self:center;margin:auto 0;color:var(--yp-text-muted);text-align:center}
.yp-retry{min-height:44px;padding:.4rem .9rem;border:2px solid var(--yp-text-muted);border-radius:var(--yp-radius);background:var(--yp-bg);color:var(--yp-text);font:inherit;font-weight:700;cursor:pointer}
.yp-compose{position:sticky;bottom:0;display:flex;gap:.5rem;align-items:flex-end;padding:.6rem .75rem;padding-bottom:max(.6rem,env(safe-area-inset-bottom));background:var(--yp-bg);border-top:1px solid var(--yp-line)}
.yp-compose textarea{flex:1;min-width:0;min-height:48px;max-height:9rem;padding:.7rem .9rem;border:2px solid var(--yp-text-muted);border-radius:var(--yp-radius);background:var(--yp-bg);color:var(--yp-text);font:inherit;resize:none}
.yp-send{flex:none;min-width:48px;min-height:48px;padding:0 1rem;border:0;border-radius:var(--yp-radius);background:var(--yp-accent);color:var(--yp-on-accent);font:inherit;font-weight:800;cursor:pointer}
.yp-chat :disabled{opacity:.55;cursor:not-allowed}
.yp-back:focus-visible,.yp-send:focus-visible,.yp-retry:focus-visible,.yp-compose textarea:focus-visible{outline:3px solid var(--yp-focus);outline-offset:2px}
`

var chatPage = template.Must(template.New("child-chat").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><meta name="referrer" content="no-referrer"><title>Yapuno – {{.Peer}}</title><style>{{.CSS}}</style></head><body>
<main class="yp-chat" id="yp-chat" data-yp-skin="{{.Skin}}" data-conversation="{{.Conversation}}" data-csrf="{{.CSRF}}" data-last-seq="{{.LastSeq}}">
<header class="yp-head"><a class="yp-back" href="/child" aria-label="Zurück zu deiner Crew">←</a><div><h1>{{.Peer}}</h1><p>Von beiden Familien freigegeben</p></div></header>
<p class="yp-banner" id="yp-banner" role="{{if .Alert}}alert{{else}}status{{end}}"{{if not .Alert}} hidden{{end}}>{{.Alert}}</p>
<ol class="yp-list" id="yp-messages" aria-label="Nachrichten">{{range .Messages}}<li class="yp-msg {{if .Mine}}yp-mine{{else}}yp-theirs{{end}}" data-seq="{{.Seq}}"><p class="yp-text">{{.Body}}</p><p class="yp-meta">{{if .Mine}}Gesendet · {{end}}<time datetime="{{.ISO}}">{{.Clock}}</time></p></li>{{else}}<li class="yp-empty">Noch keine Nachrichten. Schreib die erste!</li>{{end}}</ol>
<form class="yp-compose" id="yp-compose" method="post" action="/child/chat/{{.Conversation}}/send"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="client_id" value="{{.ClientID}}"><label class="yp-sr" for="yp-body">Nachricht an {{.Peer}}</label><textarea id="yp-body" name="body" rows="1" required maxlength="1000" autocomplete="off" enterkeyhint="send" placeholder="Nachricht schreiben">{{.Draft}}</textarea><button class="yp-send" type="submit">Senden</button></form>
</main>
<script src="/child/chat-utils.js" defer></script>
<script src="/child/chat.js" defer></script>
</body></html>`))

type chatMessage struct {
	Seq   uint64
	Mine  bool
	Body  string
	ISO   string
	Clock string
}

func newClientID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// renderChat writes the conversation page. A caller that is not allowed to
// read the conversation is sent back to the start page without any content.
func (h *handler) renderChat(w http.ResponseWriter, r *http.Request, s childsession.Session, conversationID string, status int, alert, draft string) {
	peer, ok := h.peerName(s, conversationID)
	if !ok {
		http.Redirect(w, r, "/child", http.StatusSeeOther)
		return
	}
	recent, err := h.opt.Messages.Recent(principal(s), conversationID, chatPageSize)
	if err != nil {
		http.Redirect(w, r, "/child", http.StatusSeeOther)
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
		http.Error(w, "Unable to display chat", http.StatusInternalServerError)
		return
	}
	clientID, err := newClientID()
	if err != nil {
		http.Error(w, "Unable to display chat", http.StatusInternalServerError)
		return
	}
	self := contacts.Party{TenantID: s.TenantID, ChildID: s.ChildID}
	view := make([]chatMessage, 0, len(recent))
	var last uint64
	for _, m := range recent {
		view = append(view, chatMessage{Seq: m.Seq, Mine: m.From == self, Body: m.Body,
			ISO: m.At.UTC().Format(time.RFC3339), Clock: m.At.Local().Format("15:04")})
		last = m.Seq
	}
	decls := strings.TrimSuffix(strings.TrimPrefix(tokens, ":root{"), "}")
	headers(w, true)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = chatPage.Execute(w, struct {
		Skin, Peer, Conversation, CSRF, ClientID, Alert, Draft string
		LastSeq                                                uint64
		CSS                                                    template.CSS
		Messages                                               []chatMessage
	}{
		Skin: def.ID, Peer: peer, Conversation: conversationID, CSRF: s.CSRFToken, ClientID: clientID, Alert: alert, Draft: draft,
		LastSeq: last,
		// Validated first-party skin tokens and fixed component rules only.
		CSS:      template.CSS(".yp-chat{" + decls + "}\n" + chatCSS),
		Messages: view,
	})
}

func (h *handler) chat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !conversationPattern.MatchString(id) {
		headers(w, false)
		http.NotFound(w, r)
		return
	}
	s, err := h.sessions.Authenticate(r)
	if err != nil {
		headers(w, false)
		http.Redirect(w, r, "/child", http.StatusSeeOther)
		return
	}
	h.renderChat(w, r, s, id, http.StatusOK, "", "")
}

// explainSend maps a store error to a status and a German message for the
// no-script form.
func explainSend(err error) (int, string) {
	switch {
	case errors.Is(err, messages.ErrInvalid):
		return http.StatusBadRequest, "Diese Nachricht geht so nicht. Sie darf nicht leer sein und höchstens 1000 Zeichen haben."
	case errors.Is(err, messages.ErrConflict):
		return http.StatusConflict, "Diese Nachricht wurde schon mit anderem Text gesendet. Lade die Seite neu und versuch es nochmal."
	case errors.Is(err, messages.ErrRateLimited):
		return http.StatusTooManyRequests, "Das waren viele Nachrichten. Warte kurz und versuch es nochmal."
	default:
		return http.StatusServiceUnavailable, "Die Nachricht konnte nicht gespeichert werden. Sie wurde nicht gesendet."
	}
}

// sendForm is the no-script path: a normal form post followed by a redirect.
func (h *handler) sendForm(w http.ResponseWriter, r *http.Request) {
	headers(w, false)
	id := r.PathValue("id")
	if !conversationPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}
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
	r.Body = http.MaxBytesReader(w, r.Body, formBodyLimit)
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
	if len(token) != len(s.CSRFToken) || subtle.ConstantTimeCompare([]byte(token), []byte(s.CSRFToken)) != 1 {
		http.Error(w, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	body := r.PostForm.Get("body")
	_, _, err = h.opt.Messages.Send(principal(s), id, r.PostForm.Get("client_id"), body)
	switch {
	case err == nil:
		http.Redirect(w, r, "/child/chat/"+id, http.StatusSeeOther)
	case errors.Is(err, messages.ErrForbidden):
		http.Error(w, "Not allowed", http.StatusForbidden)
	default:
		status, alert := explainSend(err)
		h.renderChat(w, r, s, id, status, alert, body)
	}
}

type sendRequest struct {
	ClientID string `json:"client_id"`
	Body     string `json:"body"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(value); err != nil {
		http.Error(w, "Unable to encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b.Bytes())
}

func jsonError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// sendJSON is the scripted path. It requires the exact HTTPS origin and the
// session's CSRF token in a header that cross-site pages cannot set.
func (h *handler) sendJSON(w http.ResponseWriter, r *http.Request) {
	headers(w, false)
	id := r.PathValue("id")
	if !conversationPattern.MatchString(id) {
		jsonError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Header.Get("Origin") != "https://"+r.Host {
		jsonError(w, http.StatusForbidden, "origin")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		jsonError(w, http.StatusUnsupportedMediaType, "content_type")
		return
	}
	s, err := h.sessions.Authenticate(r)
	if err != nil {
		jsonError(w, http.StatusUnauthorized, "signed_out")
		return
	}
	token := r.Header.Get("X-CSRF-Token")
	if len(token) != len(s.CSRFToken) || subtle.ConstantTimeCompare([]byte(token), []byte(s.CSRFToken)) != 1 {
		jsonError(w, http.StatusForbidden, "csrf")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, jsonBodyLimit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var input sendRequest
	if err := dec.Decode(&input); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid")
		return
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		jsonError(w, http.StatusBadRequest, "invalid")
		return
	}
	m, duplicate, err := h.opt.Messages.Send(principal(s), id, input.ClientID, input.Body)
	switch {
	case err == nil:
		status := http.StatusCreated
		if duplicate {
			status = http.StatusOK
		}
		writeJSON(w, status, struct {
			Seq       uint64 `json:"seq"`
			At        string `json:"at"`
			Duplicate bool   `json:"duplicate"`
		}{m.Seq, m.At.UTC().Format(time.RFC3339Nano), duplicate})
	case errors.Is(err, messages.ErrForbidden):
		jsonError(w, http.StatusForbidden, "not_allowed")
	case errors.Is(err, messages.ErrInvalid):
		jsonError(w, http.StatusBadRequest, "invalid")
	case errors.Is(err, messages.ErrConflict):
		jsonError(w, http.StatusConflict, "conflict")
	case errors.Is(err, messages.ErrRateLimited):
		w.Header().Set("Retry-After", "60")
		jsonError(w, http.StatusTooManyRequests, "slow_down")
	default:
		jsonError(w, http.StatusServiceUnavailable, "not_stored")
	}
}

type wireMessage struct {
	Seq  uint64 `json:"seq"`
	Mine bool   `json:"mine"`
	Body string `json:"body"`
	At   string `json:"at"`
}

// historyJSON returns messages after a cursor. The response says only whether
// a message is the reader's own; it carries no identifiers of either child.
func (h *handler) historyJSON(w http.ResponseWriter, r *http.Request) {
	headers(w, false)
	id := r.PathValue("id")
	if !conversationPattern.MatchString(id) {
		jsonError(w, http.StatusNotFound, "not_found")
		return
	}
	s, err := h.sessions.Authenticate(r)
	if err != nil {
		jsonError(w, http.StatusUnauthorized, "signed_out")
		return
	}
	var after uint64
	if values, ok := r.URL.Query()["after"]; ok {
		if len(values) != 1 {
			jsonError(w, http.StatusBadRequest, "invalid")
			return
		}
		if after, err = strconv.ParseUint(values[0], 10, 64); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid")
			return
		}
	}
	found, err := h.opt.Messages.History(principal(s), id, after, messages.MaxPage)
	if err != nil {
		if errors.Is(err, messages.ErrForbidden) {
			jsonError(w, http.StatusForbidden, "not_allowed")
			return
		}
		jsonError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	self := contacts.Party{TenantID: s.TenantID, ChildID: s.ChildID}
	out := make([]wireMessage, 0, len(found))
	for _, m := range found {
		out = append(out, wireMessage{Seq: m.Seq, Mine: m.From == self, Body: m.Body, At: m.At.UTC().Format(time.RFC3339Nano)})
	}
	writeJSON(w, http.StatusOK, map[string][]wireMessage{"messages": out})
}
