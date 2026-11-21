package familyweb

import (
	"crypto/subtle"
	"errors"
	"html/template"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/fontassets"
	"github.com/pblumer/yapuno/internal/reports"
	"github.com/pblumer/yapuno/internal/skin"
)

// ContactStore manages guardian-approved contact relationships. It must
// re-check the guardian's current authority on every call.
type ContactStore interface {
	Overview(guardianID, tenantID, childID string) (contacts.Overview, error)
	Invite(guardianID, tenantID, childID string) (string, error)
	Redeem(guardianID, tenantID, childID, code string) (string, error)
	Confirm(guardianID, tenantID, childID, linkID string) error
	Remove(guardianID, tenantID, childID, linkID string) error
	// Unblock lifts a block that this child set itself.
	Unblock(guardianID, tenantID, childID, linkID string) error
}

// ReportStore holds the reports a child filed about single messages. It must
// show them only to guardians with current authority for that child.
type ReportStore interface {
	ForChild(guardianID, tenantID, childID string) ([]reports.Report, error)
	Dismiss(guardianID, tenantID, childID, reportID string) error
	// MarkSeen records that a guardian opened the page showing this child's
	// reports, which ends reminders for them.
	MarkSeen(guardianID, tenantID, childID string) error
}

// ReportCounter tells the family overview how many reports are open.
type ReportCounter interface {
	Count(guardianID, tenantID, childID string) int
}

var linkIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// NewContactPages serves contact management for authenticated guardians.
// Guardian and tenant always come from the session; the child is named by the
// request and authorized against current grants by the store. looks may be nil.
func NewContactPages(auth Authenticator, store ContactStore, families FamilyStore, looks AppearanceStore) (http.Handler, error) {
	if auth == nil || store == nil || families == nil {
		return nil, errors.New("authenticator, contact store and family store are required")
	}
	return newContactPages(auth, store, families, looks, nil)
}

// NewContactPagesWithReports additionally shows the messages a child reported
// and lets a guardian dismiss them.
func NewContactPagesWithReports(auth Authenticator, store ContactStore, families FamilyStore, looks AppearanceStore, reported ReportStore) (http.Handler, error) {
	if auth == nil || store == nil || families == nil || reported == nil {
		return nil, errors.New("authenticator, contact store, family store and report store are required")
	}
	return newContactPages(auth, store, families, looks, reported)
}

func newContactPages(auth Authenticator, store ContactStore, families FamilyStore, looks AppearanceStore, reported ReportStore) (http.Handler, error) {
	h := &contactHandler{auth: auth, store: store, families: families, looks: looks, reports: reported}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /family/contacts", h.view)
	mux.HandleFunc("POST /family/contacts/unblock", h.unblock)
	if reported != nil {
		mux.HandleFunc("POST /family/contacts/report-done", h.reportDone)
	}
	mux.HandleFunc("POST /family/contacts/invite", h.invite)
	mux.HandleFunc("POST /family/contacts/redeem", h.redeem)
	mux.HandleFunc("POST /family/contacts/confirm", h.confirm)
	mux.HandleFunc("POST /family/contacts/remove", h.remove)
	return mux, nil
}

type contactHandler struct {
	auth     Authenticator
	store    ContactStore
	families FamilyStore
	looks    AppearanceStore
	reports  ReportStore // nil: reports are not shown
}

func authenticated(auth Authenticator, w http.ResponseWriter, r *http.Request) (Session, bool) {
	p, err := auth.Authenticate(r)
	if err != nil || p.GuardianID == "" || p.TenantID == "" || p.CSRFToken == "" {
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return Session{}, false
	}
	return p, true
}

// postedForm is postedFields for requests about one child: it additionally
// requires a well-formed child id.
func postedForm(w http.ResponseWriter, r *http.Request, p Session, fields ...string) (url.Values, bool) {
	form, ok := postedFields(w, r, p, fields...)
	if !ok {
		return nil, false
	}
	if !validChildID(form.Get("child_id")) {
		http.Error(w, "Invalid child", http.StatusBadRequest)
		return nil, false
	}
	return form, true
}

// postedFields enforces form encoding, a small body, exactly one value for
// each expected field and the session-bound CSRF token.
func postedFields(w http.ResponseWriter, r *http.Request, p Session, fields ...string) (url.Values, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		http.Error(w, "Expected form data", http.StatusUnsupportedMediaType)
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return nil, false
	}
	for _, field := range append([]string{"csrf_token"}, fields...) {
		if len(r.PostForm[field]) != 1 {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return nil, false
		}
	}
	token := r.PostForm.Get("csrf_token")
	if len(token) != len(p.CSRFToken) || subtle.ConstantTimeCompare([]byte(token), []byte(p.CSRFToken)) != 1 {
		http.Error(w, "Invalid CSRF token", http.StatusForbidden)
		return nil, false
	}
	return r.PostForm, true
}

func respondContactError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, contacts.ErrForbidden):
		http.Error(w, "Not authorized", http.StatusForbidden)
	case errors.Is(err, contacts.ErrNotFound):
		http.Error(w, "Contact request not found", http.StatusNotFound)
	case errors.Is(err, contacts.ErrInvalid):
		http.Error(w, "Invalid contact request", http.StatusBadRequest)
	case errors.Is(err, contacts.ErrExpired):
		http.Error(w, "Contact request expired", http.StatusGone)
	case errors.Is(err, contacts.ErrNotPending), errors.Is(err, contacts.ErrExists), errors.Is(err, contacts.ErrConsumed):
		http.Error(w, "Contact request not available", http.StatusConflict)
	case errors.Is(err, contacts.ErrLimit):
		http.Error(w, "Too many open invitations", http.StatusTooManyRequests)
	default:
		http.Error(w, "Unable to manage contacts", http.StatusInternalServerError)
	}
}

// explained maps expected problems to a German message shown on the page.
// Messages never say which child or family is behind a code.
func explained(err error) (string, int, bool) {
	switch {
	case errors.Is(err, contacts.ErrConsumed):
		return "Dieser Code wurde bereits eingelöst. Bittet die andere Familie um einen neuen Code. Sie sollte eine unerwartete Anfrage ablehnen.", http.StatusConflict, true
	case errors.Is(err, contacts.ErrExpired):
		return "Dieser Code ist abgelaufen. Bittet die andere Familie um einen neuen Code.", http.StatusGone, true
	case errors.Is(err, contacts.ErrExists):
		return "Zwischen diesen Kindern besteht bereits eine Anfrage oder ein Kontakt.", http.StatusConflict, true
	case errors.Is(err, contacts.ErrInvalid):
		return "Dieser Code ist ungültig. Bitte prüft die Eingabe.", http.StatusBadRequest, true
	case errors.Is(err, contacts.ErrLimit):
		return "Für dieses Kind gibt es bereits zu viele offene Einladungen. Wartet, bis eine abläuft.", http.StatusTooManyRequests, true
	}
	return "", 0, false
}

var notices = map[string]string{
	"requested":   "Anfrage gesendet. Die andere Familie muss sie jetzt noch freigeben.",
	"confirmed":   "Kontakt freigegeben.",
	"removed":     "Entfernt.",
	"unblocked":   "Blockierung aufgehoben.",
	"report-done": "Meldung geschlossen. Die Kopie der Nachricht ist gelöscht.",
}

const contactCSS = `body{margin:0}
.yp-page{box-sizing:border-box;min-height:100vh;background-color:var(--yp-bg);background-image:var(--yp-wallpaper);background-size:var(--yp-wallpaper-size);color:var(--yp-text);font-family:var(--yp-font-body);font-size:var(--yp-font-size);line-height:1.45}
.yp-page *{box-sizing:border-box}
.yp-shell{max-width:40rem;margin:0 auto;padding:1rem 1rem 2.5rem}
.yp-back{display:inline-flex;align-items:center;min-height:44px;color:var(--yp-accent-text);font-weight:700;text-decoration:none}
.yp-page h1{font-family:var(--yp-font-heading);font-size:1.9rem;line-height:1.15;letter-spacing:-.03em;margin:.25rem 0 .5rem;overflow-wrap:anywhere}
.yp-page h2{font-family:var(--yp-font-heading);font-size:1.2rem;margin:0 0 .6rem}
.yp-muted{color:var(--yp-text-muted);margin:.3rem 0}
.yp-page section{margin:1.25rem 0;padding:1rem;border:1px solid var(--yp-line);border-radius:var(--yp-radius);background:var(--yp-surface)}
.yp-page ul,.yp-page ol{margin:0;padding:0;list-style:none}
.yp-page ol{list-style:decimal;padding-left:1.3rem;color:var(--yp-text-muted)}
.yp-page li.yp-item{padding:.8rem 0;border-top:1px solid var(--yp-line);overflow-wrap:anywhere}
.yp-page li.yp-item:first-child{border-top:0;padding-top:0}
.yp-page form{display:flex;flex-direction:column;gap:.6rem;margin-top:.6rem}
.yp-check{display:flex;gap:.6rem;align-items:flex-start;min-height:44px;padding:.5rem 0}
.yp-check input{width:1.4rem;height:1.4rem;flex:none;margin:0;accent-color:var(--yp-accent)}
.yp-page button{min-height:48px;padding:.7rem 1rem;border-radius:var(--yp-radius);font:inherit;font-weight:800;cursor:pointer}
.yp-primary{border:0;background:var(--yp-accent);color:var(--yp-on-accent)}
.yp-secondary{border:2px solid var(--yp-line);background:var(--yp-bg);color:var(--yp-text)}
.yp-danger{border:2px solid var(--yp-danger-text);background:var(--yp-bg);color:var(--yp-danger-text)}
.yp-page label.yp-field{font-weight:700}
.yp-page input[type=text]{width:100%;min-height:48px;padding:.6rem .8rem;border:2px solid var(--yp-text-muted);border-radius:var(--yp-radius);background:var(--yp-bg);color:var(--yp-text);font:inherit;letter-spacing:.04em}
.yp-page button:focus-visible,.yp-page input:focus-visible,.yp-back:focus-visible{outline:3px solid var(--yp-focus);outline-offset:2px}
.yp-status,.yp-alert{padding:.8rem 1rem;border-radius:var(--yp-radius);background:var(--yp-surface);font-weight:700}
.yp-status{border:2px solid var(--yp-line)}
.yp-alert{border:2px solid var(--yp-danger-text)}
.yp-reported{margin:.5rem 0;padding:.7rem .9rem;border-left:4px solid var(--yp-accent-text);border-radius:var(--yp-radius);background:var(--yp-bg);white-space:pre-wrap;overflow-wrap:anywhere}
.yp-code{display:block;padding:1rem;border:2px dashed var(--yp-accent-text);border-radius:var(--yp-radius);background:var(--yp-bg);font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:1.25rem;font-weight:700;letter-spacing:.06em;text-align:center;overflow-wrap:anywhere;user-select:all}
`

func (h *contactHandler) css(p Session) (string, template.CSS, error) {
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
	return def.ID, template.CSS(fonts + ".yp-page{" + decls + "}\n" + contactCSS), nil
}

const contactHead = `<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Yapuno – Kontakte</title><style>{{.CSS}}</style></head><body><main class="yp-page" data-yp-skin="{{.Skin}}"><div class="yp-shell">`

var contactPage = template.Must(template.New("contacts").Parse(contactHead + `
<a class="yp-back" href="/family">← Zurück zur Familie</a>
<h1>Kontakte von {{.Name}}</h1>
<p class="yp-muted">{{.Name}} kann nur mit Kindern schreiben, denen beide Familien zugestimmt haben. Es gibt kein Verzeichnis und keine Suche.</p>
{{if .Alert}}<p class="yp-alert" role="alert">{{.Alert}}</p>{{end}}{{if .Notice}}<p class="yp-status" role="status">{{.Notice}}</p>{{end}}
{{if .Reports}}<section aria-labelledby="yp-reports"><h2 id="yp-reports">Meldungen von {{.Name}}</h2>
<p class="yp-muted">{{.Name}} hat diese Nachrichten gemeldet. Du siehst nur die gemeldete Nachricht, nicht den übrigen Chat. Die andere Familie wurde nicht informiert. {{.Name}} sieht in Yapuno, dass ihr die Meldung gesehen habt.</p>
<ul>{{range .Reports}}<li class="yp-item"><strong>Nachricht von {{if .From}}{{.From}}{{else}}einem Kind{{end}}</strong>
<blockquote class="yp-reported">{{.Body}}</blockquote>
<p class="yp-muted">Geschrieben am {{.Sent}} · Gemeldet am {{.Filed}}</p>
<form method="post" action="/family/contacts/report-done"><input type="hidden" name="csrf_token" value="{{$.CSRF}}"><input type="hidden" name="child_id" value="{{$.Child}}"><input type="hidden" name="report_id" value="{{.ID}}"><label class="yp-check"><input type="checkbox" name="confirm" value="yes" required> <span>Wir haben darüber gesprochen. Meldung schliessen und Kopie der Nachricht löschen.</span></label><button class="yp-secondary" type="submit">Erledigt</button></form></li>{{end}}</ul>
<p class="yp-muted">Eine Meldung wird nach {{.RetentionDays}} Tagen automatisch gelöscht. Nach «Erledigt» bleibt bis dahin nur ein Vermerk ohne Nachrichtentext, damit {{.Name}} weiter sieht, dass ihr sie gesehen habt. Einen Kontakt kannst du weiter unten entfernen.</p></section>{{end}}
{{if .View.Blocked}}<section aria-labelledby="yp-blocked"><h2 id="yp-blocked">Von {{.Name}} blockiert</h2><ul>{{range .View.Blocked}}<li class="yp-item"><strong>{{.Name}}</strong>
<p class="yp-muted">{{$.Name}} hat diesen Kontakt blockiert. Die beiden können sich nicht schreiben. Sprich mit {{$.Name}}, bevor du die Blockierung aufhebst.</p>
<form method="post" action="/family/contacts/unblock"><input type="hidden" name="csrf_token" value="{{$.CSRF}}"><input type="hidden" name="child_id" value="{{$.Child}}"><input type="hidden" name="link_id" value="{{.ID}}"><label class="yp-check"><input type="checkbox" name="confirm" value="yes" required> <span>{{$.Name}} ist einverstanden. Die beiden dürfen sich wieder schreiben.</span></label><button class="yp-secondary" type="submit">Blockierung aufheben</button></form>
<form method="post" action="/family/contacts/remove"><input type="hidden" name="csrf_token" value="{{$.CSRF}}"><input type="hidden" name="child_id" value="{{$.Child}}"><input type="hidden" name="link_id" value="{{.ID}}"><label class="yp-check"><input type="checkbox" name="confirm" value="yes" required> <span>Kontakt mit {{.Name}} beenden. Zum erneuten Verbinden braucht es einen neuen Code.</span></label><button class="yp-danger" type="submit">Kontakt entfernen</button></form></li>{{end}}</ul></section>{{end}}
{{if .View.Paused}}<section aria-labelledby="yp-paused"><h2 id="yp-paused">Pausierte Kontakte</h2><ul>{{range .View.Paused}}<li class="yp-item"><strong>{{.Name}}</strong>
<p class="yp-muted">Dieser Kontakt ist zurzeit pausiert. Die beiden können sich nicht schreiben.</p>
<form method="post" action="/family/contacts/remove"><input type="hidden" name="csrf_token" value="{{$.CSRF}}"><input type="hidden" name="child_id" value="{{$.Child}}"><input type="hidden" name="link_id" value="{{.ID}}"><label class="yp-check"><input type="checkbox" name="confirm" value="yes" required> <span>Kontakt mit {{.Name}} beenden. Zum erneuten Verbinden braucht es einen neuen Code.</span></label><button class="yp-danger" type="submit">Kontakt entfernen</button></form></li>{{end}}</ul></section>{{end}}
{{if .View.Incoming}}<section aria-labelledby="yp-incoming"><h2 id="yp-incoming">Wartet auf eure Zustimmung</h2><ul>{{range .View.Incoming}}<li class="yp-item"><strong>{{.Name}}</strong>
<p class="yp-muted">Die andere Familie hat euren Einladungscode eingelöst und zugestimmt. Gebt nur frei, wenn sie euch das selbst bestätigt hat.</p>
<form method="post" action="/family/contacts/confirm"><input type="hidden" name="csrf_token" value="{{$.CSRF}}"><input type="hidden" name="child_id" value="{{$.Child}}"><input type="hidden" name="link_id" value="{{.ID}}"><label class="yp-check"><input type="checkbox" name="confirm" value="yes" required> <span>Wir kennen diese Familie und stimmen dem Kontakt zu.</span></label><button class="yp-primary" type="submit">Freigeben</button></form>
<form method="post" action="/family/contacts/remove"><input type="hidden" name="csrf_token" value="{{$.CSRF}}"><input type="hidden" name="child_id" value="{{$.Child}}"><input type="hidden" name="link_id" value="{{.ID}}"><input type="hidden" name="confirm" value="yes"><button class="yp-secondary" type="submit">Ablehnen</button></form></li>{{end}}</ul></section>{{end}}
{{if .View.Outgoing}}<section aria-labelledby="yp-outgoing"><h2 id="yp-outgoing">Wartet auf die andere Familie</h2><ul>{{range .View.Outgoing}}<li class="yp-item"><strong>{{.Name}}</strong>
<p class="yp-muted">Eure Zustimmung liegt vor. Die andere Familie muss noch freigeben.</p>
<form method="post" action="/family/contacts/remove"><input type="hidden" name="csrf_token" value="{{$.CSRF}}"><input type="hidden" name="child_id" value="{{$.Child}}"><input type="hidden" name="link_id" value="{{.ID}}"><input type="hidden" name="confirm" value="yes"><button class="yp-secondary" type="submit">Anfrage zurückziehen</button></form></li>{{end}}</ul></section>{{end}}
<section aria-labelledby="yp-active"><h2 id="yp-active">Freigegebene Kontakte</h2>{{if .View.Contacts}}<ul>{{range .View.Contacts}}<li class="yp-item"><strong>{{.Name}}</strong>
<p class="yp-muted">Von beiden Familien freigegeben.</p>
<form method="post" action="/family/contacts/remove"><input type="hidden" name="csrf_token" value="{{$.CSRF}}"><input type="hidden" name="child_id" value="{{$.Child}}"><input type="hidden" name="link_id" value="{{.ID}}"><label class="yp-check"><input type="checkbox" name="confirm" value="yes" required> <span>Kontakt mit {{.Name}} beenden. Zum erneuten Verbinden braucht es einen neuen Code.</span></label><button class="yp-danger" type="submit">Kontakt entfernen</button></form></li>{{end}}</ul>{{else}}<p class="yp-muted">Noch keine freigegebenen Kontakte.</p>{{end}}</section>
<section aria-labelledby="yp-new"><h2 id="yp-new">Neuen Kontakt verbinden</h2>
<ol><li>Eine Familie erstellt einen Einladungscode und gibt ihn der anderen persönlich oder am Telefon weiter.</li><li>Die andere Familie löst den Code für ihr Kind ein.</li><li>Die erste Familie gibt die Anfrage frei. Erst dann besteht der Kontakt.</li></ol>
<form method="post" action="/family/contacts/invite"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="child_id" value="{{.Child}}"><button class="yp-primary" type="submit">Einladungscode erstellen</button></form>
<p class="yp-muted">Offene Einladungen: {{.View.OpenInvitations}}. Ein Code gilt 24 Stunden und nur einmal.</p>
<form method="post" action="/family/contacts/redeem"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="child_id" value="{{.Child}}"><label class="yp-field" for="yp-code">Code der anderen Familie</label><input type="text" id="yp-code" name="code" required maxlength="64" autocomplete="off" autocapitalize="characters" autocorrect="off" spellcheck="false"><button class="yp-secondary" type="submit">Code einlösen</button></form></section>
<p class="yp-muted">Vorschau mit synthetischen Profilen.</p>
</div></main></body></html>`))

var contactInvitePage = template.Must(template.New("contact-invite").Parse(contactHead + `
<a class="yp-back" href="/family/contacts?child_id={{.Child}}">← Zurück zu den Kontakten</a>
<h1>Einladungscode für {{.Name}}</h1>
<output class="yp-code">{{.Code}}</output>
<p>Gebt diesen Code nur der Familie weiter, mit deren Kind {{.Name}} sich verbinden soll – am besten persönlich oder am Telefon.</p>
<p class="yp-muted">Der Code gilt 24 Stunden und nur einmal. Er wird hier nur jetzt angezeigt. Der Code allein erlaubt noch keinen Kontakt: Nach dem Einlösen müsst ihr die Anfrage freigeben.</p>
</div></main></body></html>`))

func (h *contactHandler) childName(p Session, child string) (string, bool) {
	for _, c := range h.families.Cards(p.GuardianID, p.TenantID) {
		if c.ID == child && c.Permissions.ApproveContacts {
			return c.DisplayName, true
		}
	}
	return "", false
}

func (h *contactHandler) render(w http.ResponseWriter, p Session, child string, status int, alert, notice string) {
	name, ok := h.childName(p, child)
	if !ok {
		respondContactError(w, contacts.ErrForbidden)
		return
	}
	view, err := h.store.Overview(p.GuardianID, p.TenantID, child)
	if err != nil {
		respondContactError(w, err)
		return
	}
	var reported []reportView
	if h.reports != nil {
		list, err := h.reports.ForChild(p.GuardianID, p.TenantID, child)
		if err != nil {
			// An unreadable report store must not look like "nothing reported".
			if errors.Is(err, reports.ErrForbidden) {
				http.Error(w, "Not authorized", http.StatusForbidden)
				return
			}
			http.Error(w, "Unable to display reports", http.StatusInternalServerError)
			return
		}
		for _, r := range list {
			reported = append(reported, reportView{ID: r.ID, From: r.From, Body: r.Body,
				Sent: r.SentAt.Local().Format("02.01.2006 15:04"), Filed: r.ReportedAt.Local().Format("02.01.2006 15:04")})
		}
	}
	skinID, css, err := h.css(p)
	if err != nil {
		http.Error(w, "Unable to display contacts", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	err = contactPage.Execute(w, struct {
		Skin, Name, Child, CSRF, Alert, Notice string
		CSS                                    template.CSS
		View                                   contacts.Overview
		Reports                                []reportView
		RetentionDays                          int
	}{Skin: skinID, Name: name, Child: child, CSRF: p.CSRFToken, Alert: alert, Notice: notice, CSS: css, View: view,
		Reports: reported, RetentionDays: int(reports.Retention / (24 * time.Hour))})
	if err == nil && len(reported) > 0 {
		// The guardian has been shown these reports: no more reminders. If
		// this cannot be recorded, a reminder too many is the safe failure.
		_ = h.reports.MarkSeen(p.GuardianID, p.TenantID, child)
	}
}

// reportView is one reported message as shown to the reporting child's
// guardian: the copy of that single message and two dates.
type reportView struct {
	ID, From, Body, Sent, Filed string
}

func (h *contactHandler) view(w http.ResponseWriter, r *http.Request) {
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
	// Only fixed, known notices are shown; the query text is never rendered.
	h.render(w, p, query.Get("child_id"), http.StatusOK, "", notices[query.Get("notice")])
}

func (h *contactHandler) invite(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	form, ok := postedForm(w, r, p, "child_id")
	if !ok {
		return
	}
	child := form.Get("child_id")
	name, ok := h.childName(p, child)
	if !ok {
		respondContactError(w, contacts.ErrForbidden)
		return
	}
	code, err := h.store.Invite(p.GuardianID, p.TenantID, child)
	if err != nil {
		if alert, status, ok := explained(err); ok {
			h.render(w, p, child, status, alert, "")
			return
		}
		respondContactError(w, err)
		return
	}
	skinID, css, err := h.css(p)
	if err != nil {
		http.Error(w, "Unable to display invitation", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = contactInvitePage.Execute(w, struct {
		Skin, Name, Child, Code string
		CSS                     template.CSS
	}{Skin: skinID, Name: name, Child: child, Code: code, CSS: css})
}

func (h *contactHandler) redeem(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	form, ok := postedForm(w, r, p, "child_id", "code")
	if !ok {
		return
	}
	child := form.Get("child_id")
	if _, err := h.store.Redeem(p.GuardianID, p.TenantID, child, form.Get("code")); err != nil {
		if alert, status, ok := explained(err); ok {
			h.render(w, p, child, status, alert, "")
			return
		}
		respondContactError(w, err)
		return
	}
	http.Redirect(w, r, "/family/contacts?child_id="+url.QueryEscape(child)+"&notice=requested", http.StatusSeeOther)
}

// decided handles the two operations on an existing request or contact. Both
// require an explicit confirmation field and a well-formed link id.
func (h *contactHandler) decided(w http.ResponseWriter, r *http.Request, notice string, act func(p Session, child, linkID string) error) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	form, ok := postedForm(w, r, p, "child_id", "link_id", "confirm")
	if !ok {
		return
	}
	if form.Get("confirm") != "yes" || !linkIDPattern.MatchString(form.Get("link_id")) {
		http.Error(w, "Explicit confirmation required", http.StatusBadRequest)
		return
	}
	child := form.Get("child_id")
	if err := act(p, child, form.Get("link_id")); err != nil {
		respondContactError(w, err)
		return
	}
	http.Redirect(w, r, "/family/contacts?child_id="+url.QueryEscape(child)+"&notice="+notice, http.StatusSeeOther)
}

func (h *contactHandler) confirm(w http.ResponseWriter, r *http.Request) {
	h.decided(w, r, "confirmed", func(p Session, child, linkID string) error {
		return h.store.Confirm(p.GuardianID, p.TenantID, child, linkID)
	})
}

func (h *contactHandler) remove(w http.ResponseWriter, r *http.Request) {
	h.decided(w, r, "removed", func(p Session, child, linkID string) error {
		return h.store.Remove(p.GuardianID, p.TenantID, child, linkID)
	})
}

func (h *contactHandler) unblock(w http.ResponseWriter, r *http.Request) {
	h.decided(w, r, "unblocked", func(p Session, child, linkID string) error {
		return h.store.Unblock(p.GuardianID, p.TenantID, child, linkID)
	})
}

// reportDone deletes one report of the guardian's own child together with
// the copy of the reported message.
func (h *contactHandler) reportDone(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	form, ok := postedForm(w, r, p, "child_id", "report_id", "confirm")
	if !ok {
		return
	}
	if form.Get("confirm") != "yes" || !linkIDPattern.MatchString(form.Get("report_id")) {
		http.Error(w, "Explicit confirmation required", http.StatusBadRequest)
		return
	}
	child := form.Get("child_id")
	switch err := h.reports.Dismiss(p.GuardianID, p.TenantID, child, form.Get("report_id")); {
	case err == nil:
		http.Redirect(w, r, "/family/contacts?child_id="+url.QueryEscape(child)+"&notice=report-done", http.StatusSeeOther)
	case errors.Is(err, reports.ErrForbidden):
		http.Error(w, "Not authorized", http.StatusForbidden)
	case errors.Is(err, reports.ErrNotFound):
		http.Error(w, "Report not found", http.StatusNotFound)
	default:
		http.Error(w, "Unable to delete the report", http.StatusInternalServerError)
	}
}
