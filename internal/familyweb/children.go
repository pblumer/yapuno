package familyweb

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/pblumer/yapuno/internal/familystore"
)

// childNotices are the only texts a redirect can select.
var childNotices = map[string]string{
	"added": "Das Kind ist angelegt. Du findest es in der Familienübersicht.",
}

var childPage = template.Must(template.New("child").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Yapuno – Kind anlegen</title><style>{{.CSS}}</style></head><body><main class="yp-page" data-yp-skin="{{.Skin}}"><div class="yp-shell">
<a class="yp-back" href="/family">← Zurück zur Familie</a>
<h1>Weiteres Kind anlegen</h1>
<p class="yp-muted">Lege ein Profil für ein weiteres Kind deiner Familie an.</p>
{{if .Alert}}<p class="yp-alert" role="alert">{{.Alert}}</p>{{end}}
{{if .Notice}}<p class="yp-status" role="status">{{.Notice}}</p>{{end}}
<section aria-labelledby="yp-new"><h2 id="yp-new">Neues Profil</h2>
{{if .CanAdd}}<form method="post" action="/family/children"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><label class="yp-field" for="yp-name">Vorname oder Spitzname</label><input type="text" id="yp-name" name="name" required maxlength="40" autocomplete="off"><button class="yp-primary" type="submit">Kind anlegen</button></form>
{{else}}<p class="yp-muted">In einer Familie sind höchstens {{.Max}} Kinder möglich.</p>{{end}}</section>
<section aria-labelledby="yp-know"><h2 id="yp-know">Gut zu wissen</h2><ul class="yp-facts">
<li>Das Profil gehört zu deiner Familie. Den Namen kannst du später ändern.</li>
<li>Danach verbindest du in der Familienübersicht unter «Geräte von …» ein Gerät für das Kind.</li>
<li>Du verwaltest das neue Profil allein. Einen zweiten Elternteil fügt zurzeit nur der Betreiber dieser Yapuno-Installation hinzu.</li>
<li>In einer Familie sind höchstens {{.Max}} Kinder möglich.</li>
</ul></section>
<p class="yp-muted">Vorschau mit synthetischen Profilen.</p>
</div></main></body></html>`))

func (h *handler) renderChildForm(w http.ResponseWriter, p Session, status int, alert, notice string) {
	skinID, css, err := skinnedCSS(h.looks, p, "")
	if err != nil {
		http.Error(w, "Unable to display the form", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = childPage.Execute(w, struct {
		Skin, CSRF, Alert, Notice string
		CSS                       template.CSS
		CanAdd                    bool
		Max                       int
	}{Skin: skinID, CSRF: p.CSRFToken, Alert: alert, Notice: notice, CSS: css,
		CanAdd: len(h.store.Cards(p.GuardianID, p.TenantID)) < familystore.MaxChildrenPerFamily, Max: familystore.MaxChildrenPerFamily})
}

func (h *handler) childForm(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := h.session(w, r)
	if !ok {
		return
	}
	if !fullGuardian(h.store, p) {
		http.Error(w, "Not authorized", http.StatusForbidden)
		return
	}
	// Only fixed, known notices are shown; the query text is never rendered.
	h.renderChildForm(w, p, http.StatusOK, "", childNotices[r.URL.Query().Get("done")])
}

// newChildID returns a random, server-chosen id for a child profile.
func newChildID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "c-" + hex.EncodeToString(raw), nil
}

// addChild creates another child profile in the guardian's own family. The
// family comes from the session and the id from the server; the request
// names nothing but the display name. The store checks the authority.
func (h *handler) addChild(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := h.session(w, r)
	if !ok {
		return
	}
	form, ok := postedFields(w, r, p, "name")
	if !ok {
		return
	}
	id, err := newChildID()
	if err != nil {
		http.Error(w, "Unable to save", http.StatusInternalServerError)
		return
	}
	switch err := h.children.AddChild(p.GuardianID, p.TenantID, id, strings.TrimSpace(form.Get("name"))); {
	case err == nil:
		http.Redirect(w, r, "/family/children?done=added", http.StatusSeeOther)
	case errors.Is(err, familystore.ErrForbidden):
		http.Error(w, "Not authorized", http.StatusForbidden)
	case errors.Is(err, familystore.ErrInvalid):
		h.renderChildForm(w, p, http.StatusBadRequest, "Dieser Name geht nicht. Erlaubt sind 1 bis 40 Zeichen ohne spitze Klammern.", "")
	case errors.Is(err, familystore.ErrLimit):
		h.renderChildForm(w, p, http.StatusConflict, "Mehr Profile sind nicht möglich: In einer Familie sind höchstens "+strconv.Itoa(familystore.MaxChildrenPerFamily)+" Kinder möglich.", "")
	default:
		http.Error(w, "Unable to save", http.StatusInternalServerError)
	}
}
