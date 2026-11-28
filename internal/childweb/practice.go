package childweb

import (
	"errors"
	"net/http"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/chatpage"
	"github.com/pblumer/yapuno/internal/chesspage"
	"github.com/pblumer/yapuno/internal/childsession"
	"github.com/pblumer/yapuno/internal/games"
	"github.com/pblumer/yapuno/internal/practice"
)

// Practice stores one game per child against the computer. It must
// authorize every call from current device and guardian-enablement state.
type Practice interface {
	Current(p games.Principal) (practice.View, bool, error)
	Start(p games.Principal, level practice.Level, color string) (practice.View, error)
	Act(p games.Principal, a games.Action) (practice.View, error)
}

const computerName = "Der Computer"

func (h *handler) mountPractice(mux *http.ServeMux) {
	if h.opt.Games == nil {
		mux.HandleFunc("GET /child/chess.js", h.asset(chesspage.JS))
		mux.HandleFunc("GET /child/chess-utils.js", h.asset(chesspage.UtilsJS))
	}
	mux.HandleFunc("GET /child/practice", h.practiceView)
	mux.HandleFunc("POST /child/practice/start", h.practiceStart)
	mux.HandleFunc("POST /child/practice/act", h.practiceAct)
}

// mayPractice reports whether the home page offers practice right now.
func (h *handler) mayPractice(s childsession.Session) bool {
	if h.opt.Practice == nil {
		return false
	}
	_, _, err := h.opt.Practice.Current(gamePrincipal(s))
	return err == nil
}

func (h *handler) renderPractice(w http.ResponseWriter, r *http.Request, s childsession.Session, status int, alert, moved string) {
	v, found, err := h.opt.Practice.Current(gamePrincipal(s))
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
	in := chesspage.Input{Peer: computerName, Skin: pref.Skin, Home: "/child", Path: "/child/practice", Assets: "/child",
		CSRF: s.CSRFToken, ClientID: clientID, Alert: alert, Moved: moved, Practice: &chesspage.Practice{}}
	if found {
		in.View = &v.View
		in.Practice.Level, in.Practice.CanTakeback = string(v.Level), v.CanTakeback
	}
	headers(w, true)
	if err := chesspage.Render(w, status, chesspage.Build(in)); err != nil {
		http.Error(w, "Unable to display game", http.StatusInternalServerError)
	}
}

func (h *handler) practiceView(w http.ResponseWriter, r *http.Request) {
	s, err := h.sessions.Authenticate(r)
	if err != nil {
		headers(w, false)
		http.Redirect(w, r, "/child", http.StatusSeeOther)
		return
	}
	h.renderPractice(w, r, s, http.StatusOK, "", "")
}

func (h *handler) practiceStart(w http.ResponseWriter, r *http.Request) {
	s, form, ok := h.childForm(w, r, "level", "color")
	if !ok {
		return
	}
	_, err := h.opt.Practice.Start(gamePrincipal(s), practice.Level(form.Get("level")), form.Get("color"))
	h.practiceDone(w, r, s, err, "", "")
}

func (h *handler) practiceAct(w http.ResponseWriter, r *http.Request) {
	s, form, ok := h.childForm(w, r, "kind")
	if !ok {
		return
	}
	// The page's forms carry an empty game field; practice has one game.
	if g := form["game"]; len(g) > 1 || len(g) == 1 && g[0] != "" {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	a, ok := chesspage.ActionFrom(form)
	if !ok {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	_, err := h.opt.Practice.Act(gamePrincipal(s), a)
	h.practiceDone(w, r, s, err, a.Kind, a.Move)
}

func (h *handler) practiceDone(w http.ResponseWriter, r *http.Request, s childsession.Session, err error, kind games.ActionKind, move string) {
	switch {
	case err == nil:
		http.Redirect(w, r, "/child/practice", http.StatusSeeOther)
	case errors.Is(err, games.ErrForbidden):
		http.Error(w, "Not allowed", http.StatusForbidden)
	default:
		if errors.Is(err, games.ErrRateLimited) {
			kind = "practice"
		}
		status, alert := chesspage.Explain(err, kind)
		h.renderPractice(w, r, s, status, alert, move)
	}
}
