package familyweb

import (
	"errors"
	"html/template"
	"net/http"
	"strconv"

	"github.com/pblumer/yapuno/internal/guardiannames"
)

// GuardianNameStore keeps the names a guardian chooses to be shown by. Set
// must only ever be given the guardian of the session.
type GuardianNameStore interface {
	GuardianNames
	Set(tenantID, guardianID string, names guardiannames.Names) error
}

// NewNamePages serves "Mein Name": a guardian chooses, changes or removes
// the name their own children see and the name guardians of connected
// families see. Both are optional. The guardian is always the one of the
// session; no request names another. looks may be nil.
func NewNamePages(auth Authenticator, names GuardianNameStore, looks AppearanceStore) (http.Handler, error) {
	if auth == nil || names == nil {
		return nil, errors.New("name pages require sessions and a name store")
	}
	h := &nameHandler{auth: auth, names: names, looks: looks}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /family/name", h.view)
	mux.HandleFunc("POST /family/name", h.save)
	return mux, nil
}

type nameHandler struct {
	auth  Authenticator
	names GuardianNameStore
	looks AppearanceStore
}

// nameNotices are the only texts a redirect can select.
var nameNotices = map[string]string{
	"saved": "Deine Namen sind gespeichert.",
}

var namePage = template.Must(template.New("name").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Yapuno – Mein Name</title><style>{{.CSS}}</style></head><body><main class="yp-page" data-yp-skin="{{.Skin}}"><div class="yp-shell">
<a class="yp-back" href="/family">← Zurück zur Familie</a>
<h1>Mein Name</h1>
<p class="yp-muted">Beide Namen sind freiwillig. Lässt du ein Feld leer, wird dieser Name entfernt.</p>
{{if .Alert}}<p class="yp-alert" role="alert">{{.Alert}}</p>{{end}}
{{if .Notice}}<p class="yp-status" role="status">{{.Notice}}</p>{{end}}
<form method="post" action="/family/name"><input type="hidden" name="csrf_token" value="{{.CSRF}}">
<section aria-labelledby="yp-kids"><h2 id="yp-kids">Für deine Kinder</h2>
<p class="yp-muted">So sehen dich deine eigenen Kinder in Yapuno, zum Beispiel «Mami» oder «Papi». Ohne Namen steht dort «Dein Elternteil».</p>
<label class="yp-field" for="yp-for-children">Name für deine Kinder</label><input type="text" id="yp-for-children" name="for_children" value="{{.ForChildren}}" maxlength="{{.Max}}" autocomplete="off"></section>
<section aria-labelledby="yp-adults"><h2 id="yp-adults">Für andere Eltern</h2>
<p class="yp-muted">So sehen dich die Eltern von Familien, deren Kinder mit deinen verbunden sind, zum Beispiel dein Vorname. Ohne Namen steht dort «Elternteil von …» mit den Namen deiner Kinder.</p>
<label class="yp-field" for="yp-for-adults">Name für andere Eltern</label><input type="text" id="yp-for-adults" name="for_adults" value="{{.ForAdults}}" maxlength="{{.Max}}" autocomplete="off"></section>
<button class="yp-primary" type="submit">Speichern</button></form>
<section aria-labelledby="yp-know"><h2 id="yp-know">Gut zu wissen</h2><ul class="yp-facts">
<li>Kinder anderer Familien sehen keinen deiner Namen.</li>
<li>Andere Eltern sehen deinen Namen nur, solange eure Kinder verbunden sind, und immer zusammen mit «Elternteil von …».</li>
<li>Yapuno prüft Namen nicht. Auch andere nennen sich so, wie sie möchten: Ein Name sagt nicht, wer jemand ist.</li>
<li>In E-Mails von Yapuno steht keiner deiner Namen.</li>
</ul></section>
<p class="yp-muted">Vorschau mit synthetischen Profilen.</p>
</div></main></body></html>`))

const nameCSS = `.yp-page form>button.yp-primary{width:100%;margin-top:.4rem}
`

func (h *nameHandler) render(w http.ResponseWriter, p Session, status int, shown guardiannames.Names, alert, notice string) {
	skinID, css, err := skinnedCSS(h.looks, p, nameCSS)
	if err != nil {
		http.Error(w, "Unable to display the page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = namePage.Execute(w, struct {
		Skin, CSRF, ForChildren, ForAdults, Alert, Notice string
		Max                                               int
		CSS                                               template.CSS
	}{Skin: skinID, CSRF: p.CSRFToken, ForChildren: shown.ForChildren, ForAdults: shown.ForAdults, Alert: alert, Notice: notice, Max: guardiannames.MaxLength, CSS: css})
}

func (h *nameHandler) view(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	// Only fixed, known notices are shown; the query text is never rendered.
	h.render(w, p, http.StatusOK, h.names.Get(p.TenantID, p.GuardianID), "", nameNotices[r.URL.Query().Get("done")])
}

func (h *nameHandler) save(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	form, ok := postedFields(w, r, p, "for_children", "for_adults")
	if !ok {
		return
	}
	typed := guardiannames.Names{ForChildren: form.Get("for_children"), ForAdults: form.Get("for_adults")}
	// Guardian and family come from the session; the form names nobody.
	switch err := h.names.Set(p.TenantID, p.GuardianID, typed); {
	case err == nil:
		http.Redirect(w, r, "/family/name?done=saved", http.StatusSeeOther)
	case errors.Is(err, guardiannames.ErrInvalid):
		h.render(w, p, http.StatusBadRequest, typed, "Dieser Name geht nicht. Erlaubt sind höchstens "+strconv.Itoa(guardiannames.MaxLength)+" Zeichen ohne spitze Klammern und ohne Steuerzeichen.", "")
	default:
		http.Error(w, "Unable to save", http.StatusInternalServerError)
	}
}
