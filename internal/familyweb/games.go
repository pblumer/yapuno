package familyweb

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/fontassets"
	"github.com/pblumer/yapuno/internal/play"
	"github.com/pblumer/yapuno/internal/skin"
)

// GameSettings stores which games a child may play. SetEnabled must
// re-check the guardian's current contact authority for the named child.
type GameSettings interface {
	Enabled(tenantID, childID, game string) bool
	SetEnabled(guardianID, tenantID, childID, game string, on bool) error
}

// NewGamePages serves "Spiele": a guardian allows or forbids chess for one
// of their children. Guardian and tenant come from the session; the child is
// named by the request and authorized by the store. looks may be nil.
func NewGamePages(auth Authenticator, settings GameSettings, families FamilyStore, looks AppearanceStore) (http.Handler, error) {
	if auth == nil || settings == nil || families == nil {
		return nil, errors.New("authenticator, game settings and family store are required")
	}
	h := &gameHandler{auth: auth, settings: settings, families: families, looks: looks}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /family/games", h.view)
	mux.HandleFunc("POST /family/games", h.save)
	return mux, nil
}

type gameHandler struct {
	auth     Authenticator
	settings GameSettings
	families FamilyStore
	looks    AppearanceStore
}

// gameNotices are the only texts a redirect can select.
var gameNotices = map[string]string{
	"on":  "Schach ist jetzt erlaubt.",
	"off": "Schach ist jetzt nicht mehr erlaubt. Laufende Partien sind ab sofort gesperrt.",
}

var gamePage = template.Must(template.New("games").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Yapuno – Spiele</title><style>{{.CSS}}</style></head><body><main class="yp-page" data-yp-skin="{{.Skin}}"><div class="yp-shell">
<a class="yp-back" href="/family">← Zurück zur Familie</a>
<h1>Spiele von {{.Name}}</h1>
{{if .Notice}}<p class="yp-status" role="status">{{.Notice}}</p>{{end}}
<section aria-labelledby="yp-chess"><h2 id="yp-chess">Schach</h2>
<p class="yp-status">{{if .Chess}}Erlaubt{{else}}Nicht erlaubt{{end}}</p>
<form method="post" action="/family/games"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="child_id" value="{{.Child}}"><input type="hidden" name="game" value="chess">{{if .Chess}}<input type="hidden" name="enabled" value="no"><button class="yp-secondary" type="submit">Schach nicht mehr erlauben</button>{{else}}<input type="hidden" name="enabled" value="yes"><button type="submit">Schach erlauben</button>{{end}}</form>
</section>
<section aria-labelledby="yp-what"><h2 id="yp-what">Was das bedeutet</h2><ul class="yp-facts">
<li>{{.Name}} kann nur mit Kindern aus der eigenen Crew spielen, also mit Kindern, denen beide Familien zugestimmt haben. Fremde gibt es im Spiel nicht.</li>
<li>Eine Partie ist nur möglich, wenn auch die Eltern des anderen Kindes Schach erlaubt haben.</li>
<li>Gespielt wird als Fernschach (jeder zieht, wann es passt) oder live mit Schachuhr.</li>
<li>Im Spiel gibt es keinen Chat und keine Texte, nur Züge, Remis-Angebote und Aufgeben.</li>
<li>Schaltest du Schach aus, kann {{.Name}} ab sofort weder neue Partien beginnen noch laufende ansehen oder weiterspielen. Schreiben bleibt davon unberührt.</li>
<li>Ein Zeitlimit für das Spielen gibt es noch nicht.</li>
</ul></section>
<p class="yp-muted">Vorschau mit synthetischen Profilen.</p>
</div></main></body></html>`))

func (h *gameHandler) css(p Session) (string, template.CSS, error) {
	catalog := skin.Catalog()
	id := "classic"
	if h.looks != nil {
		id = h.looks.Get(appearance.Subject{Kind: appearance.KindGuardian, TenantID: p.TenantID, ID: p.GuardianID}).Skin
	}
	def := skin.Resolve(id, catalog, catalog["classic"])
	decls, err := skin.Declarations(def)
	if err != nil {
		return "", "", err
	}
	// Only validated first-party skin tokens and fixed component rules.
	fonts := fontassets.FaceCSS(def.Typography.Heading, def.Typography.Body)
	return def.ID, template.CSS(fonts + ".yp-page{" + decls + "}\n" + contactCSS + lostCSS), nil
}

// childName returns the child's name if the guardian may approve contacts
// for it right now; enabling a game requires the same authority.
func (h *gameHandler) childName(p Session, child string) (string, bool) {
	for _, c := range h.families.Cards(p.GuardianID, p.TenantID) {
		if c.ID == child && c.Permissions.ApproveContacts {
			return c.DisplayName, true
		}
	}
	return "", false
}

func (h *gameHandler) view(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	if len(query["child_id"]) != 1 || !validChildID(query.Get("child_id")) {
		http.Error(w, "Invalid child", http.StatusBadRequest)
		return
	}
	child := query.Get("child_id")
	name, ok := h.childName(p, child)
	if !ok {
		http.Error(w, "Not authorized", http.StatusForbidden)
		return
	}
	skinID, css, err := h.css(p)
	if err != nil {
		http.Error(w, "Unable to display games", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Only fixed, known notices are shown; the query text is never rendered.
	_ = gamePage.Execute(w, struct {
		Skin, Name, Child, CSRF, Notice string
		CSS                             template.CSS
		Chess                           bool
	}{Skin: skinID, Name: name, Child: child, CSRF: p.CSRFToken, Notice: gameNotices[query.Get("done")], CSS: css,
		Chess: h.settings.Enabled(p.TenantID, child, play.GameChess)})
}

func (h *gameHandler) save(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	form, ok := postedForm(w, r, p, "child_id", "game", "enabled")
	if !ok {
		return
	}
	var on bool
	switch form.Get("enabled") {
	case "yes":
		on = true
	case "no":
	default:
		http.Error(w, "Invalid choice", http.StatusBadRequest)
		return
	}
	child := form.Get("child_id")
	err := h.settings.SetEnabled(p.GuardianID, p.TenantID, child, form.Get("game"), on)
	switch {
	case errors.Is(err, play.ErrForbidden):
		http.Error(w, "Not authorized", http.StatusForbidden)
		return
	case errors.Is(err, play.ErrUnknownGame):
		http.Error(w, "Unknown game", http.StatusBadRequest)
		return
	case err != nil:
		http.Error(w, "Unable to save", http.StatusServiceUnavailable)
		return
	}
	done := "off"
	if on {
		done = "on"
	}
	http.Redirect(w, r, "/family/games?child_id="+url.QueryEscape(child)+"&done="+done, http.StatusSeeOther)
}
