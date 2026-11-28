package childweb

import (
	"crypto/subtle"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strconv"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/chatpage"
	"github.com/pblumer/yapuno/internal/chesspage"
	"github.com/pblumer/yapuno/internal/childsession"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/games"
)

// Games stores chess games. It must authorize every call from current
// contact, block, profile, device and guardian-enablement state.
type Games interface {
	Invite(p games.Principal, contactID, clientID string, mode games.Mode) (games.View, error)
	Act(p games.Principal, gameID string, a games.Action) (games.View, bool, error)
	Game(p games.Principal, gameID string) (games.View, error)
	Latest(p games.Principal, contactID string) (games.View, bool, error)
}

func gamePrincipal(s childsession.Session) games.Principal {
	return games.Principal{Party: contacts.Party{TenantID: s.TenantID, ChildID: s.ChildID}, DeviceID: s.DeviceID}
}

func (h *handler) mountPlay(mux *http.ServeMux) {
	mux.HandleFunc("GET /child/chess.js", h.asset(chesspage.JS))
	mux.HandleFunc("GET /child/chess-utils.js", h.asset(chesspage.UtilsJS))
	mux.HandleFunc("GET /child/play/{id}", h.playView)
	mux.HandleFunc("GET /child/play/{id}/state", h.playState)
	mux.HandleFunc("POST /child/play/{id}/invite", h.playInvite)
	mux.HandleFunc("POST /child/play/{id}/act", h.playAct)
}

// playNote is the short hint next to a contact on the home page. ok is
// false when the two children may not play chess right now.
func (h *handler) playNote(s childsession.Session, contactID string) (string, bool) {
	if h.opt.Games == nil {
		return "", false
	}
	v, found, err := h.opt.Games.Latest(gamePrincipal(s), contactID)
	if err != nil {
		return "", false
	}
	switch {
	case !found:
		return "", true
	case v.YourTurnToAnswer():
		return "Einladung", true
	case v.YourTurn():
		return "Du bist am Zug", true
	}
	return "", true
}

// renderPlay writes the chess page for one of the child's own contacts.
// A child that may not play with this contact is sent back home.
func (h *handler) renderPlay(w http.ResponseWriter, r *http.Request, s childsession.Session, contactID string, status int, alert, moved string) {
	name, ok := h.peerName(s, contactID)
	if !ok {
		http.Redirect(w, r, "/child", http.StatusSeeOther)
		return
	}
	v, found, err := h.opt.Games.Latest(gamePrincipal(s), contactID)
	if err != nil {
		http.Redirect(w, r, "/child", http.StatusSeeOther)
		return
	}
	clientID, err := chatpage.NewClientID()
	if err != nil {
		http.Error(w, "Unable to display game", http.StatusInternalServerError)
		return
	}
	pref := appearance.Default()
	if h.opt.Looks != nil {
		pref = h.opt.Looks.Get(appearance.Subject{Kind: appearance.KindChild, TenantID: s.TenantID, ID: s.ChildID})
	}
	in := chesspage.Input{Peer: name, PeerFigure: h.peerFigure(contacts.Party{TenantID: s.TenantID, ChildID: s.ChildID}, contactID),
		Skin: pref.Skin, Home: "/child", Path: "/child/play/" + contactID, Assets: "/child",
		CSRF: s.CSRFToken, ClientID: clientID, Alert: alert, Moved: moved}
	if found {
		in.View = &v
	}
	headers(w, true)
	if err := chesspage.Render(w, status, chesspage.Build(in)); err != nil {
		http.Error(w, "Unable to display game", http.StatusInternalServerError)
	}
}

func (h *handler) playView(w http.ResponseWriter, r *http.Request) {
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
	h.renderPlay(w, r, s, id, http.StatusOK, "", "")
}

// playState answers the page's poll with a signature of what it shows. It
// carries no identifiers of either child.
func (h *handler) playState(w http.ResponseWriter, r *http.Request) {
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
	v, found, err := h.opt.Games.Latest(gamePrincipal(s), id)
	if err != nil {
		chatpage.JSONError(w, http.StatusForbidden, "not_allowed")
		return
	}
	var view *games.View
	if found {
		view = &v
	}
	chatpage.WriteJSON(w, http.StatusOK, map[string]string{"sig": chesspage.Signature(view)})
}

// playForm checks a no-script post: session, form encoding, a small body,
// exactly one value per expected field and the session's CSRF token.
func (h *handler) playForm(w http.ResponseWriter, r *http.Request, fields ...string) (childsession.Session, url.Values, bool) {
	headers(w, false)
	if !conversationPattern.MatchString(r.PathValue("id")) {
		http.NotFound(w, r)
		return childsession.Session{}, nil, false
	}
	s, err := h.sessions.Authenticate(r)
	if err != nil {
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return childsession.Session{}, nil, false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		http.Error(w, "Expected form data", http.StatusUnsupportedMediaType)
		return childsession.Session{}, nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return childsession.Session{}, nil, false
	}
	for _, field := range append([]string{"csrf_token", "client_id"}, fields...) {
		if len(r.PostForm[field]) != 1 {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return childsession.Session{}, nil, false
		}
	}
	token := r.PostForm.Get("csrf_token")
	if len(token) != len(s.CSRFToken) || subtle.ConstantTimeCompare([]byte(token), []byte(s.CSRFToken)) != 1 {
		http.Error(w, "Invalid CSRF token", http.StatusForbidden)
		return childsession.Session{}, nil, false
	}
	return s, r.PostForm, true
}

// explainPlay maps a store error to a status and a German message.
func explainPlay(err error, kind games.ActionKind) (int, string) {
	switch {
	case errors.Is(err, games.ErrStale):
		return http.StatusConflict, "Inzwischen hat sich die Partie verändert. Schau dir das Brett nochmals an."
	case errors.Is(err, games.ErrIllegal):
		return http.StatusUnprocessableEntity, "Dieser Zug ist nicht erlaubt. Tippe auf eine Figur, dann siehst du, wohin sie ziehen darf."
	case errors.Is(err, games.ErrInvalid) && kind == games.Move:
		return http.StatusBadRequest, "Diesen Zug verstehe ich nicht. Schreib zuerst das Feld, von dem die Figur kommt, dann das Ziel, zum Beispiel e2e4."
	case errors.Is(err, games.ErrNotNow):
		return http.StatusConflict, "Das geht gerade nicht. Schau dir die Partie nochmals an."
	case errors.Is(err, games.ErrBusy):
		return http.StatusConflict, "Ihr habt schon eine Partie. Spielt zuerst diese fertig."
	case errors.Is(err, games.ErrRateLimited) && kind == games.OfferDraw:
		return http.StatusTooManyRequests, "Du hast in dieser Partie schon oft Remis angeboten."
	case errors.Is(err, games.ErrRateLimited):
		return http.StatusTooManyRequests, "Du hast heute schon viele Einladungen geschickt. Versuch es morgen wieder."
	case errors.Is(err, games.ErrConflict):
		return http.StatusConflict, "Das wurde schon anders gesendet. Lade die Seite neu."
	case errors.Is(err, games.ErrInvalid):
		return http.StatusBadRequest, "Das hat nicht geklappt. Lade die Seite neu."
	}
	return http.StatusServiceUnavailable, "Das konnte nicht gespeichert werden. Versuch es nochmals."
}

func (h *handler) playInvite(w http.ResponseWriter, r *http.Request) {
	s, form, ok := h.playForm(w, r, "mode")
	if !ok {
		return
	}
	id := r.PathValue("id")
	_, err := h.opt.Games.Invite(gamePrincipal(s), id, form.Get("client_id"), games.Mode(form.Get("mode")))
	switch {
	case err == nil:
		http.Redirect(w, r, "/child/play/"+id, http.StatusSeeOther)
	case errors.Is(err, games.ErrForbidden):
		http.Error(w, "Not allowed", http.StatusForbidden)
	default:
		status, alert := explainPlay(err, "")
		h.renderPlay(w, r, s, id, status, alert, "")
	}
}

func (h *handler) playAct(w http.ResponseWriter, r *http.Request) {
	s, form, ok := h.playForm(w, r, "game", "kind")
	if !ok {
		return
	}
	id, gameID := r.PathValue("id"), form.Get("game")
	a := games.Action{Kind: games.ActionKind(form.Get("kind")), ClientID: form.Get("client_id")}
	if a.Kind == games.Move {
		if len(form["move"]) != 1 || len(form["ply"]) != 1 {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}
		ply, err := strconv.Atoi(form.Get("ply"))
		if err != nil || ply < 0 {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}
		a.Move, a.Ply = form.Get("move"), ply
	} else if len(form["move"]) != 0 || len(form["ply"]) != 0 {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	// The game must belong to the contact in the path; it is checked before
	// anything is done, since the store authorizes by game only.
	p := gamePrincipal(s)
	current, err := h.opt.Games.Game(p, gameID)
	if err != nil || current.Contact != id {
		http.Error(w, "Not allowed", http.StatusForbidden)
		return
	}
	_, _, err = h.opt.Games.Act(p, gameID, a)
	switch {
	case err == nil:
		http.Redirect(w, r, "/child/play/"+id, http.StatusSeeOther)
	case errors.Is(err, games.ErrForbidden):
		http.Error(w, "Not allowed", http.StatusForbidden)
	default:
		status, alert := explainPlay(err, a.Kind)
		h.renderPlay(w, r, s, id, status, alert, a.Move)
	}
}
