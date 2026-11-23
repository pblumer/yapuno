package familyweb

import (
	"errors"
	"html/template"
	"net/http"
	"strings"

	"github.com/pblumer/yapuno/internal/erasure"
	"github.com/pblumer/yapuno/internal/familystore"
)

// Eraser deletes a child profile, or the guardian's whole family, together
// with everything that is stored about it. It must check the guardian's
// authority itself; these pages only pass on who is signed in.
type Eraser interface {
	DeleteChild(guardianID, tenantID, childID string) error
	DeleteFamily(guardianID, tenantID string) error
}

// RemovalRules says whether a deletion would be allowed right now, so that a
// page can explain instead of offering a form that could only fail.
type RemovalRules interface {
	MayRemoveChild(guardianID, tenantID, childID string) error
	MayRemoveFamily(guardianID, tenantID string) error
}

// CookieClearer drops the session cookie of this browser.
type CookieClearer interface {
	Clear(w http.ResponseWriter)
}

// deleteWord is what a guardian types to delete the family.
const deleteWord = "LÖSCHEN"

// NewDeletePages serves "Profil löschen" for one child and "Familie löschen".
// Both are irreversible, so each asks the guardian to type something: the
// child's name, or the word LÖSCHEN. Guardian and tenant always come from
// the session. looks may be nil.
func NewDeletePages(auth Authenticator, eraser Eraser, rules RemovalRules, families FamilyStore, cookie CookieClearer, looks AppearanceStore) (http.Handler, error) {
	if auth == nil || eraser == nil || rules == nil || families == nil || cookie == nil {
		return nil, errors.New("delete pages require sessions, an eraser, removal rules, families and the session cookie")
	}
	h := &deleteHandler{auth: auth, eraser: eraser, rules: rules, families: families, cookie: cookie, looks: looks}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /family/children/delete", h.childPage)
	mux.HandleFunc("POST /family/children/delete", h.deleteChild)
	mux.HandleFunc("GET /family/delete", h.familyPage)
	mux.HandleFunc("POST /family/delete", h.deleteFamily)
	return mux, nil
}

type deleteHandler struct {
	auth     Authenticator
	eraser   Eraser
	rules    RemovalRules
	families FamilyStore
	cookie   CookieClearer
	looks    AppearanceStore
}

const deleteHead = `<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Yapuno – Löschen</title><style>{{.CSS}}</style></head><body><main class="yp-page" data-yp-skin="{{.Skin}}"><div class="yp-shell">`

var deleteChildPage = template.Must(template.New("delete-child").Parse(deleteHead + `
<a class="yp-back" href="/family">← Zurück zur Familie</a>
<h1>Profil von {{.Name}} löschen</h1>
{{if .Alert}}<p class="yp-alert" role="alert">{{.Alert}}</p>{{end}}
{{if .Last}}<section aria-labelledby="yp-last"><h2 id="yp-last">Nicht einzeln möglich</h2>
<p>{{.Name}} ist das einzige Kind eurer Familie. Ohne ein Kind könnte sich niemand von euch mehr anmelden. Das letzte Profil wird deshalb nur zusammen mit der ganzen Familie gelöscht.</p>
<p><a class="yp-back" href="/family/delete">Familie löschen</a></p></section>
{{else}}<section aria-labelledby="yp-what"><h2 id="yp-what">Was gelöscht wird</h2><ul class="yp-facts">
<li>Das Profil von {{.Name}} mit Name und Look.</li>
<li>Alle Geräte von {{.Name}}. Sie sind sofort abgemeldet.</li>
<li>Alle Kontakte von {{.Name}} und die Chats mit diesen Kindern, auch auf deren Geräten.</li>
<li>Die Chats zwischen {{.Name}} und euch Eltern.</li>
<li>Was {{.Name}} gemeldet hat.</li>
</ul>
<p><strong>Das lässt sich nicht rückgängig machen.</strong></p></section>
<section aria-labelledby="yp-know"><h2 id="yp-know">Gut zu wissen</h2><ul class="yp-facts">
<li>Andere Elternteile von {{.Name}} werden nicht gefragt und nicht benachrichtigt.</li>
<li>War {{.Name}} die einzige Verbindung zu einer anderen Familie, endet auch der Chat mit deren Eltern.</li>
<li>Was nicht gelöscht wird: Hat ein anderes Kind eine Nachricht von {{.Name}} gemeldet, bleibt diese Meldung bei dessen Eltern, bis sie sie schliessen oder sie abläuft.</li>
</ul></section>
<section aria-labelledby="yp-go"><h2 id="yp-go">Löschen bestätigen</h2>
<form method="post" action="/family/children/delete"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="child_id" value="{{.Child}}"><label class="yp-field" for="yp-confirm">Tippe zur Bestätigung den Namen ein: {{.Name}}</label><input type="text" id="yp-confirm" name="name" required maxlength="40" autocomplete="off" autocapitalize="off" spellcheck="false"><button class="yp-danger" type="submit">Profil endgültig löschen</button></form></section>{{end}}
<p class="yp-muted">Vorschau mit synthetischen Profilen.</p>
</div></main></body></html>`))

var deleteFamilyPage = template.Must(template.New("delete-family").Parse(deleteHead + `
<a class="yp-back" href="/family/security">← Zurück zu Sicherheit</a>
<h1>Familie löschen</h1>
{{if .Alert}}<p class="yp-alert" role="alert">{{.Alert}}</p>{{end}}
{{if .Last}}<section aria-labelledby="yp-last"><h2 id="yp-last">Nicht möglich</h2>
<p>Eure Familie ist die letzte Familie auf dieser Yapuno-Installation. Würde sie gelöscht, könnte sich niemand mehr anmelden und niemand mehr einladen. Wende dich an den Betreiber, wenn alles entfernt werden soll.</p></section>
{{else}}<section aria-labelledby="yp-what"><h2 id="yp-what">Was gelöscht wird</h2><ul class="yp-facts">
<li>Die Profile aller Kinder eurer Familie: {{.Children}}.</li>
<li>Der Zugang für alle Elternteile eurer Familie, auch für die, die gerade nicht angemeldet sind. Sie werden nicht gefragt und nicht benachrichtigt.</li>
<li>Alle Geräte, Kontakte und Chats eurer Kinder, und eure Chats mit ihnen und mit anderen Eltern.</li>
<li>Eure Looks, eure Meldungen und eure offenen Einladungen.</li>
</ul>
<p><strong>Das lässt sich nicht rückgängig machen.</strong></p></section>
<section aria-labelledby="yp-know"><h2 id="yp-know">Gut zu wissen</h2><ul class="yp-facts">
<li>Du wirst sofort abgemeldet, auch auf Geräten, auf denen du angemeldet geblieben bist.</li>
<li>Eure E-Mail-Adressen sind danach frei. Mit einer neuen Einladung könnt ihr euch neu registrieren.</li>
<li>Familien, die ihr eingeladen habt, bleiben bestehen. Ihre Kinder verlieren nur den Kontakt zu euren.</li>
<li>Was nicht gelöscht wird: Hat ein Kind einer anderen Familie eine Nachricht eurer Kinder gemeldet, bleibt diese Meldung bei dessen Eltern, bis sie sie schliessen oder sie abläuft.</li>
</ul></section>
<section aria-labelledby="yp-go"><h2 id="yp-go">Löschen bestätigen</h2>
<form method="post" action="/family/delete"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><label class="yp-field" for="yp-confirm">Tippe zur Bestätigung das Wort {{.Word}} ein</label><input type="text" id="yp-confirm" name="word" required maxlength="20" autocomplete="off" autocapitalize="characters" spellcheck="false"><button class="yp-danger" type="submit">Familie endgültig löschen</button></form></section>{{end}}
<p class="yp-muted">Vorschau mit synthetischen Profilen.</p>
</div></main></body></html>`))

const (
	notTypedAlert   = "Das stimmt nicht mit der verlangten Eingabe überein. Nichts wurde gelöscht."
	incompleteAlert = "Das Löschen wurde unterbrochen. Versuche es noch einmal; was schon entfernt ist, bleibt entfernt."
)

// childName returns the display name of one child of the session's guardian.
func (h *deleteHandler) childName(p Session, child string) string {
	for _, c := range h.families.Cards(p.GuardianID, p.TenantID) {
		if c.ID == child {
			return c.DisplayName
		}
	}
	return ""
}

func (h *deleteHandler) renderChild(w http.ResponseWriter, p Session, child string, status int, last bool, alert string) {
	skinID, css, err := skinnedCSS(h.looks, p, "")
	if err != nil {
		http.Error(w, "Unable to display the page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = deleteChildPage.Execute(w, struct {
		Skin, CSRF, Child, Name, Alert string
		Last                           bool
		CSS                            template.CSS
	}{Skin: skinID, CSRF: p.CSRFToken, Child: child, Name: h.childName(p, child), Alert: alert, Last: last, CSS: css})
}

func (h *deleteHandler) childPage(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	ids := r.URL.Query()["child_id"]
	if len(ids) != 1 || !validChildID(ids[0]) {
		http.Error(w, "Invalid child", http.StatusBadRequest)
		return
	}
	switch err := h.rules.MayRemoveChild(p.GuardianID, p.TenantID, ids[0]); {
	case err == nil:
		h.renderChild(w, p, ids[0], http.StatusOK, false, "")
	case errors.Is(err, familystore.ErrLastChild):
		h.renderChild(w, p, ids[0], http.StatusOK, true, "")
	default:
		http.Error(w, "Not authorized", http.StatusForbidden)
	}
}

func (h *deleteHandler) deleteChild(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	form, ok := postedForm(w, r, p, "child_id", "name")
	if !ok {
		return
	}
	child := form.Get("child_id")
	switch err := h.rules.MayRemoveChild(p.GuardianID, p.TenantID, child); {
	case err == nil:
	case errors.Is(err, familystore.ErrLastChild):
		h.renderChild(w, p, child, http.StatusConflict, true, "")
		return
	default:
		http.Error(w, "Not authorized", http.StatusForbidden)
		return
	}
	// The guardian shows that this child is meant by typing its name.
	name := h.childName(p, child)
	if name == "" || !strings.EqualFold(strings.TrimSpace(form.Get("name")), name) {
		h.renderChild(w, p, child, http.StatusBadRequest, false, notTypedAlert)
		return
	}
	switch err := h.eraser.DeleteChild(p.GuardianID, p.TenantID, child); {
	case err == nil:
		http.Redirect(w, r, "/family?done=child-deleted", http.StatusSeeOther)
	case errors.Is(err, erasure.ErrLeftovers):
		http.Redirect(w, r, "/family?done=child-deleted-leftovers", http.StatusSeeOther)
	case errors.Is(err, erasure.ErrIncomplete):
		h.renderChild(w, p, child, http.StatusServiceUnavailable, false, incompleteAlert)
	case errors.Is(err, familystore.ErrLastChild):
		h.renderChild(w, p, child, http.StatusConflict, true, "")
	case errors.Is(err, familystore.ErrForbidden):
		http.Error(w, "Not authorized", http.StatusForbidden)
	default:
		http.Error(w, "Unable to delete", http.StatusInternalServerError)
	}
}

func (h *deleteHandler) renderFamily(w http.ResponseWriter, p Session, status int, last bool, alert string) {
	skinID, css, err := skinnedCSS(h.looks, p, "")
	if err != nil {
		http.Error(w, "Unable to display the page", http.StatusInternalServerError)
		return
	}
	var names []string
	for _, c := range h.families.Cards(p.GuardianID, p.TenantID) {
		names = append(names, c.DisplayName)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = deleteFamilyPage.Execute(w, struct {
		Skin, CSRF, Children, Word, Alert string
		Last                              bool
		CSS                               template.CSS
	}{Skin: skinID, CSRF: p.CSRFToken, Children: strings.Join(names, ", "), Word: deleteWord, Alert: alert, Last: last, CSS: css})
}

func (h *deleteHandler) familyPage(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	switch err := h.rules.MayRemoveFamily(p.GuardianID, p.TenantID); {
	case err == nil:
		h.renderFamily(w, p, http.StatusOK, false, "")
	case errors.Is(err, familystore.ErrLastFamily):
		h.renderFamily(w, p, http.StatusOK, true, "")
	default:
		http.Error(w, "Not authorized", http.StatusForbidden)
	}
}

func (h *deleteHandler) deleteFamily(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	form, ok := postedFields(w, r, p, "word")
	if !ok {
		return
	}
	switch err := h.rules.MayRemoveFamily(p.GuardianID, p.TenantID); {
	case err == nil:
	case errors.Is(err, familystore.ErrLastFamily):
		h.renderFamily(w, p, http.StatusConflict, true, "")
		return
	default:
		http.Error(w, "Not authorized", http.StatusForbidden)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(form.Get("word")), deleteWord) {
		h.renderFamily(w, p, http.StatusBadRequest, false, notTypedAlert)
		return
	}
	switch err := h.eraser.DeleteFamily(p.GuardianID, p.TenantID); {
	case err == nil:
		// The sessions are ended on the server; this drops the cookie too.
		h.cookie.Clear(w)
		http.Redirect(w, r, "/login?notice=family-deleted", http.StatusSeeOther)
	case errors.Is(err, erasure.ErrLeftovers):
		h.cookie.Clear(w)
		http.Redirect(w, r, "/login?notice=family-deleted-incomplete", http.StatusSeeOther)
	case errors.Is(err, erasure.ErrIncomplete):
		h.renderFamily(w, p, http.StatusServiceUnavailable, false, incompleteAlert)
	case errors.Is(err, familystore.ErrLastFamily):
		h.renderFamily(w, p, http.StatusConflict, true, "")
	case errors.Is(err, familystore.ErrForbidden):
		http.Error(w, "Not authorized", http.StatusForbidden)
	default:
		http.Error(w, "Unable to delete", http.StatusInternalServerError)
	}
}
