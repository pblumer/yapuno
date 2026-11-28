package familyweb

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/chatpage"
	"github.com/pblumer/yapuno/internal/chesspage"
	"github.com/pblumer/yapuno/internal/games"
	"github.com/pblumer/yapuno/internal/review"
)

// FamilyGames stores chess games of guardians with their own children. It
// must authorize every call from current grant, identity, lock and
// game-enablement state.
type FamilyGames interface {
	GuardianInvite(g games.GuardianPrincipal, childID, clientID string, mode games.Mode, tips bool) (games.View, error)
	GuardianAct(g games.GuardianPrincipal, gameID string, a games.Action) (games.View, bool, error)
	GuardianGame(g games.GuardianPrincipal, gameID string) (games.View, error)
	GuardianLatest(g games.GuardianPrincipal, childID string) (games.View, bool, error)
}

// NewPlayPages serves chess of a guardian with one of their own children.
// Guardian and tenant come from the session; the child is named by the path
// and authorized by the store. looks may be nil.
func NewPlayPages(auth Authenticator, store FamilyGames, families FamilyStore, looks AppearanceStore) (http.Handler, error) {
	if auth == nil || store == nil || families == nil {
		return nil, errors.New("authenticator, game store and family store are required")
	}
	h := &playHandler{auth: auth, games: store, families: families, looks: looks}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /family/chess.js", chatAsset(chesspage.JS))
	mux.HandleFunc("GET /family/chess-utils.js", chatAsset(chesspage.UtilsJS))
	mux.HandleFunc("GET /family/play/{child}", h.view)
	mux.HandleFunc("GET /family/play/{child}/state", h.state)
	mux.HandleFunc("POST /family/play/{child}/invite", h.invite)
	mux.HandleFunc("POST /family/play/{child}/act", h.act)
	return mux, nil
}

type playHandler struct {
	auth     Authenticator
	games    FamilyGames
	families FamilyStore
	looks    AppearanceStore
}

func principalOf(p Session) games.GuardianPrincipal {
	return games.GuardianPrincipal{TenantID: p.TenantID, GuardianID: p.GuardianID}
}

// childName returns the name of one of the guardian's own children.
func (h *playHandler) childName(p Session, child string) (string, bool) {
	if !validChildID(child) {
		return "", false
	}
	for _, c := range h.families.Cards(p.GuardianID, p.TenantID) {
		if c.ID == child {
			return c.DisplayName, true
		}
	}
	return "", false
}

func (h *playHandler) render(w http.ResponseWriter, r *http.Request, p Session, child string, status int, alert, moved string) {
	name, ok := h.childName(p, child)
	if !ok {
		http.Redirect(w, r, "/family", http.StatusSeeOther)
		return
	}
	v, found, err := h.games.GuardianLatest(principalOf(p), child)
	if err != nil {
		http.Redirect(w, r, "/family", http.StatusSeeOther)
		return
	}
	clientID, err := chatpage.NewClientID()
	if err != nil {
		http.Error(w, "Unable to display game", http.StatusInternalServerError)
		return
	}
	skinID := "classic"
	if h.looks != nil {
		skinID = h.looks.Get(appearance.Subject{Kind: appearance.KindGuardian, TenantID: p.TenantID, ID: p.GuardianID}).Skin
	}
	in := chesspage.Input{Peer: name, Skin: skinID, Home: "/family", HomeLabel: "Zurück zur Familie",
		Path: "/family/play/" + url.PathEscape(child), Assets: "/family", CSRF: p.CSRFToken, ClientID: clientID, Alert: alert, Moved: moved}
	if found {
		in.View = &v
		if v.Phase == games.Finished && len(v.Moves) > 0 {
			in.Reviewed, in.Review = true, review.Cached(v.Moves, v.YourColor)
		}
	}
	scriptHeaders(w)
	if err := chesspage.Render(w, status, chesspage.Build(in)); err != nil {
		http.Error(w, "Unable to display game", http.StatusInternalServerError)
	}
}

func (h *playHandler) view(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	h.render(w, r, p, r.PathValue("child"), http.StatusOK, "", "")
}

func (h *playHandler) state(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, err := h.auth.Authenticate(r)
	if err != nil || p.GuardianID == "" || p.TenantID == "" {
		chatpage.JSONError(w, http.StatusUnauthorized, "signed_out")
		return
	}
	child := r.PathValue("child")
	if !validChildID(child) {
		chatpage.JSONError(w, http.StatusNotFound, "not_found")
		return
	}
	v, found, err := h.games.GuardianLatest(principalOf(p), child)
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

func (h *playHandler) invite(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	form, ok := postedFields(w, r, p, "client_id", "mode")
	if !ok {
		return
	}
	var tips bool
	switch values := form["tips"]; {
	case len(values) == 0:
	case len(values) == 1 && values[0] == "yes":
		tips = true
	default:
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	child := r.PathValue("child")
	if !validChildID(child) {
		http.NotFound(w, r)
		return
	}
	_, err := h.games.GuardianInvite(principalOf(p), child, form.Get("client_id"), games.Mode(form.Get("mode")), tips)
	h.done(w, r, p, child, err, "", "")
}

func (h *playHandler) act(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	form, ok := postedFields(w, r, p, "client_id", "game", "kind")
	if !ok {
		return
	}
	child := r.PathValue("child")
	if !validChildID(child) {
		http.NotFound(w, r)
		return
	}
	a, ok := chesspage.ActionFrom(form)
	if !ok {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	// The game must be this guardian's game with the child in the path; it
	// is checked before anything is done.
	gp := principalOf(p)
	current, err := h.games.GuardianGame(gp, form.Get("game"))
	if err != nil || current.Scope != games.FamilyScope(p.TenantID, p.GuardianID, child) {
		http.Error(w, "Not authorized", http.StatusForbidden)
		return
	}
	_, _, err = h.games.GuardianAct(gp, form.Get("game"), a)
	h.done(w, r, p, child, err, a.Kind, a.Move)
}

func (h *playHandler) done(w http.ResponseWriter, r *http.Request, p Session, child string, err error, kind games.ActionKind, move string) {
	switch {
	case err == nil:
		http.Redirect(w, r, "/family/play/"+url.PathEscape(child), http.StatusSeeOther)
	case errors.Is(err, games.ErrForbidden):
		http.Error(w, "Not authorized", http.StatusForbidden)
	default:
		status, alert := chesspage.Explain(err, kind)
		h.render(w, r, p, child, status, alert, move)
	}
}
