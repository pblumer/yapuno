package childweb

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/chatpage"
	"github.com/pblumer/yapuno/internal/childsession"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/messages"
	"github.com/pblumer/yapuno/internal/sameorigin"
)

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
	if h.opt.Contacts == nil {
		return "", false
	}
	for _, c := range h.opt.Contacts.ContactsOf(contacts.Party{TenantID: s.TenantID, ChildID: s.ChildID}) {
		if c.ID == conversationID {
			return c.Name, true
		}
	}
	return "", false
}

// familyThread returns the child's own conversation with a guardian, if the
// id names one right now.
func (h *handler) familyThread(s childsession.Session, conversationID string) (FamilyThread, bool) {
	if h.opt.Family == nil {
		return FamilyThread{}, false
	}
	for _, t := range h.opt.Family.FamilyThreads(s) {
		if t.ID == conversationID {
			return t, true
		}
	}
	return FamilyThread{}, false
}

// renderChat writes the conversation page. A caller that is not allowed to
// read the conversation is sent back to the start page without any content.
func (h *handler) renderChat(w http.ResponseWriter, r *http.Request, s childsession.Session, conversationID string, status int, alert, draft string) {
	self := contacts.Party{TenantID: s.TenantID, ChildID: s.ChildID}
	view := chatpage.Page{Subtitle: "Von beiden Familien freigegeben",
		SafetyHref: "/child/chat/" + conversationID + "/safety", SafetyLabel: h.safetyTitle(), SafetyText: h.safetyShort()}
	if guardian, ok := h.familyThread(s, conversationID); ok {
		// The child's own guardian: nothing to approve, block or report here.
		view = chatpage.Page{Peer: guardian.Name, Subtitle: "Nur du und dein Elternteil", PeerFigure: h.guardianFigure(guardian.Guardian)}
	} else if name, ok := h.peerName(s, conversationID); ok {
		view.Peer, view.PeerFigure = name, h.peerFigure(self, conversationID)
	} else {
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
	clientID, err := chatpage.NewClientID()
	if err != nil {
		http.Error(w, "Unable to display chat", http.StatusInternalServerError)
		return
	}
	for _, m := range recent {
		view.Messages = append(view.Messages, chatpage.NewMessage(m.Seq, m.From == self, m.Body, m.At))
		view.LastSeq = m.Seq
	}
	view.Skin, view.Home, view.HomeLabel = pref.Skin, "/child", "Zurück zu deiner Crew"
	view.Path, view.Assets, view.Conversation = "/child/chat/"+conversationID, "/child", conversationID
	view.CSRF, view.ClientID, view.Alert, view.Draft = s.CSRFToken, clientID, alert, draft
	headers(w, true)
	if err := chatpage.Render(w, status, view); err != nil {
		http.Error(w, "Unable to display chat", http.StatusInternalServerError)
	}
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

// sendJSON is the scripted path. It requires the exact HTTPS origin and the
// session's CSRF token in a header that cross-site pages cannot set.
func (h *handler) sendJSON(w http.ResponseWriter, r *http.Request) {
	headers(w, false)
	id := r.PathValue("id")
	if !conversationPattern.MatchString(id) {
		chatpage.JSONError(w, http.StatusNotFound, "not_found")
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
	s, err := h.sessions.Authenticate(r)
	if err != nil {
		chatpage.JSONError(w, http.StatusUnauthorized, "signed_out")
		return
	}
	token := r.Header.Get("X-CSRF-Token")
	if len(token) != len(s.CSRFToken) || subtle.ConstantTimeCompare([]byte(token), []byte(s.CSRFToken)) != 1 {
		chatpage.JSONError(w, http.StatusForbidden, "csrf")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, jsonBodyLimit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var input sendRequest
	if err := dec.Decode(&input); err != nil {
		chatpage.JSONError(w, http.StatusBadRequest, "invalid")
		return
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		chatpage.JSONError(w, http.StatusBadRequest, "invalid")
		return
	}
	m, duplicate, err := h.opt.Messages.Send(principal(s), id, input.ClientID, input.Body)
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
		}{m.Seq, m.At.UTC().Format(time.RFC3339Nano), duplicate})
	case errors.Is(err, messages.ErrForbidden):
		chatpage.JSONError(w, http.StatusForbidden, "not_allowed")
	case errors.Is(err, messages.ErrInvalid):
		chatpage.JSONError(w, http.StatusBadRequest, "invalid")
	case errors.Is(err, messages.ErrConflict):
		chatpage.JSONError(w, http.StatusConflict, "conflict")
	case errors.Is(err, messages.ErrRateLimited):
		w.Header().Set("Retry-After", "60")
		chatpage.JSONError(w, http.StatusTooManyRequests, "slow_down")
	default:
		chatpage.JSONError(w, http.StatusServiceUnavailable, "not_stored")
	}
}

// historyJSON returns messages after a cursor. The response says only whether
// a message is the reader's own; it carries no identifiers of either child.
func (h *handler) historyJSON(w http.ResponseWriter, r *http.Request) {
	headers(w, false)
	id := r.PathValue("id")
	if !conversationPattern.MatchString(id) {
		chatpage.JSONError(w, http.StatusNotFound, "not_found")
		return
	}
	s, err := h.sessions.Authenticate(r)
	if err != nil {
		chatpage.JSONError(w, http.StatusUnauthorized, "signed_out")
		return
	}
	var after uint64
	if values, ok := r.URL.Query()["after"]; ok {
		if len(values) != 1 {
			chatpage.JSONError(w, http.StatusBadRequest, "invalid")
			return
		}
		if after, err = strconv.ParseUint(values[0], 10, 64); err != nil {
			chatpage.JSONError(w, http.StatusBadRequest, "invalid")
			return
		}
	}
	found, err := h.opt.Messages.History(principal(s), id, after, messages.MaxPage)
	if err != nil {
		if errors.Is(err, messages.ErrForbidden) {
			chatpage.JSONError(w, http.StatusForbidden, "not_allowed")
			return
		}
		chatpage.JSONError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	self := contacts.Party{TenantID: s.TenantID, ChildID: s.ChildID}
	out := make([]chatpage.WireMessage, 0, len(found))
	for _, m := range found {
		out = append(out, chatpage.NewWireMessage(m.Seq, m.From == self, m.Body, m.At))
	}
	chatpage.WriteJSON(w, http.StatusOK, map[string][]chatpage.WireMessage{"messages": out})
}
