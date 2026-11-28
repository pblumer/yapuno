package chesspage

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/pblumer/yapuno/internal/games"
)

// actionFields says which fields beyond the kind each action carries.
var actionFields = map[games.ActionKind][]string{
	games.Move: {"move", "ply"},
	games.Hint: {"level", "ply"},
	"takeback": {"ply"},
}

// ActionFrom reads an action from a form: every field the kind needs
// exactly once, no field it does not need.
func ActionFrom(form url.Values) (games.Action, bool) {
	a := games.Action{Kind: games.ActionKind(form.Get("kind")), ClientID: form.Get("client_id")}
	need := map[string]bool{}
	for _, f := range actionFields[a.Kind] {
		need[f] = true
	}
	for _, f := range []string{"move", "ply", "level"} {
		if n := len(form[f]); need[f] && n != 1 || !need[f] && n != 0 {
			return games.Action{}, false
		}
	}
	number := func(f string) (int, bool) {
		if !need[f] {
			return 0, true
		}
		n, err := strconv.Atoi(form.Get(f))
		return n, err == nil && n >= 0 && n < 10000
	}
	var ok1, ok2 bool
	a.Move = form.Get("move")
	a.Ply, ok1 = number("ply")
	a.Level, ok2 = number("level")
	return a, ok1 && ok2
}

// Explain maps a store error to a status and a German message.
func Explain(err error, kind games.ActionKind) (int, string) {
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
	case errors.Is(err, games.ErrRateLimited) && kind == games.Hint:
		return http.StatusTooManyRequests, "Deine Tipps für diese Partie sind aufgebraucht."
	case errors.Is(err, games.ErrRateLimited) && kind == "practice":
		return http.StatusTooManyRequests, "Das war sehr schnell. Warte einen Moment und versuch es nochmals."
	case errors.Is(err, games.ErrRateLimited):
		return http.StatusTooManyRequests, "Du hast heute schon viele Einladungen geschickt. Versuch es morgen wieder."
	case errors.Is(err, games.ErrConflict):
		return http.StatusConflict, "Das wurde schon anders gesendet. Lade die Seite neu."
	case errors.Is(err, games.ErrInvalid):
		return http.StatusBadRequest, "Das hat nicht geklappt. Lade die Seite neu."
	}
	return http.StatusServiceUnavailable, "Das konnte nicht gespeichert werden. Versuch es nochmals."
}
