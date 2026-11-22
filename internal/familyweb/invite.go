package familyweb

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/fontassets"
	"github.com/pblumer/yapuno/internal/signup"
	"github.com/pblumer/yapuno/internal/skin"
)

// InvitationStore holds invitations to register a family. Every call is
// scoped to the guardian and tenant it is given.
type InvitationStore interface {
	Invite(guardian, tenant, child string) (string, signup.Invitation, error)
	Pending(guardian, tenant string) []signup.Invitation
	Withdraw(guardian, tenant, id string) error
}

// ChildCreator adds a child to the guardian's own family. It must check that
// the guardian holds full authority in that tenant.
type ChildCreator interface {
	AddChild(guardianID, tenantID, childID, name string) error
}

// Invitation ids are 12 random bytes in unpadded base64url.
var invitationIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16}$`)

// InviteOptions adapts the invitation pages to the installation.
type InviteOptions struct {
	Looks AppearanceStore // may be nil
	// Connect lets the guardian name one of their children in an invitation.
	// That is the guardian's approval for a contact between this child and
	// the invited family's child, given before knowing who registers.
	Connect bool
	// ParentsChat says that guardians of connected children can write to
	// each other, so the page says so before the guardian approves.
	ParentsChat bool
}

// NewInvitePages serves "invite a family": a guardian with full authority
// for a child creates one-time links with which another adult registers
// their own family. Unless the guardian names a child, an invitation shares
// nothing about the inviting family and creates no contact between children.
func NewInvitePages(auth Authenticator, invitations InvitationStore, families FamilyStore, origin string, opt InviteOptions) (http.Handler, error) {
	u, err := url.Parse(origin)
	if auth == nil || invitations == nil || families == nil || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return nil, errors.New("authenticator, invitation store, family store and an HTTPS origin are required")
	}
	h := &inviteHandler{auth: auth, invitations: invitations, families: families, origin: strings.TrimRight(origin, "/"), looks: opt.Looks, connect: opt.Connect, parentsChat: opt.ParentsChat}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /family/invite", h.view)
	mux.HandleFunc("POST /family/invite", h.create)
	mux.HandleFunc("POST /family/invite/withdraw", h.withdraw)
	return mux, nil
}

type inviteHandler struct {
	auth        Authenticator
	invitations InvitationStore
	families    FamilyStore
	origin      string
	looks       AppearanceStore
	connect     bool
	parentsChat bool
}

// inviteNotices are the only texts a redirect can select.
var inviteNotices = map[string]string{
	"withdrawn": "Die Einladung ist zurückgezogen. Der Link ist ungültig.",
}

const inviteHead = `<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Yapuno – Familie einladen</title><style>{{.CSS}}</style></head><body><main class="yp-page" data-yp-skin="{{.Skin}}"><div class="yp-shell">`

var invitePage = template.Must(template.New("invite").Parse(inviteHead + `
<a class="yp-back" href="/family">← Zurück zur Familie</a>
<h1>Eine Familie einladen</h1>
<p class="yp-muted">Mit einem Einladungslink registriert sich eine andere Familie selbst bei Yapuno und legt ihre Kinder an.</p>
{{if .Alert}}<p class="yp-alert" role="alert">{{.Alert}}</p>{{end}}
{{if .Notice}}<p class="yp-status" role="status">{{.Notice}}</p>{{end}}
<section aria-labelledby="yp-new"><h2 id="yp-new">Neue Einladung</h2>
{{if .CanCreate}}<form method="post" action="/family/invite"><input type="hidden" name="csrf_token" value="{{.CSRF}}">{{if .Connect}}<fieldset class="yp-choice"><legend>Sollen eure Kinder gleich verbunden werden?</legend>
{{range .Children}}<label class="yp-check"><input type="radio" name="child_id" value="{{.ID}}" required> <span>Ja, mit {{.Name}}</span></label>
{{end}}<label class="yp-check"><input type="radio" name="child_id" value="" required> <span>Nein, nur einladen</span></label></fieldset>
<p class="yp-muted">Mit «Ja» stimmst du dem Kontakt jetzt schon zu. Die eingeladene Person entscheidet bei der Registrierung, ob auch sie zustimmt.</p>{{end}}<button class="yp-primary" type="submit">Einladungslink erstellen</button></form>
{{else}}<p class="yp-muted">Du hast bereits {{.Max}} offene Einladungen. Ziehe eine zurück oder warte, bis eine abläuft.</p>{{end}}</section>
{{if .Pending}}<section aria-labelledby="yp-open"><h2 id="yp-open">Offene Einladungen</h2><ul>{{range .Pending}}<li class="yp-item">Erstellt am {{.Created}}, gültig bis {{.Expires}}{{if $.Connect}}, {{if .Child}}verbindet mit {{.Child}}{{else}}ohne Verbindung{{end}}{{end}}
<form method="post" action="/family/invite/withdraw"><input type="hidden" name="csrf_token" value="{{$.CSRF}}"><input type="hidden" name="invitation_id" value="{{.ID}}"><button class="yp-secondary" type="submit">Einladung zurückziehen</button></form></li>{{end}}</ul>
<p class="yp-muted">Den Link selbst zeigt Yapuno nur beim Erstellen. Ist er verloren, ziehe die Einladung zurück und erstelle eine neue.</p></section>{{end}}
<section aria-labelledby="yp-know"><h2 id="yp-know">Was eine Einladung bewirkt</h2><ul class="yp-facts">
<li>Gib den Link nur Erwachsenen weiter, die du persönlich kennst. Wer den Link hat, kann eine eigene Familie anlegen.</li>
<li>Der Link gilt 7 Tage und nur einmal.</li>
{{if .Connect}}<li>Wählst du ein Kind, sieht die eingeladene Person dessen Vornamen. Stimmt auch sie zu, sind die beiden Kinder sofort Kontakte, ohne dass du noch einmal gefragt wirst.</li>
<li>Du siehst das andere Kind danach unter «Kontakte» und kannst die Verbindung jederzeit beenden.{{if .ParentsChat}} Solange sie besteht, können sich auch die Eltern beider Familien schreiben.{{end}}</li>
<li>Ohne Kind bleibt es bei der Einladung: Du siehst nicht, wer sich registriert, und die andere Familie sieht nichts von euch.</li>
{{else}}<li>Du siehst nicht, wer sich damit registriert. Die andere Familie sieht nichts von euch.</li>
<li>Eure Kinder werden dadurch keine Kontakte. Dafür braucht es weiterhin die Zustimmung beider Familien.</li>
{{end}}<li>Yapuno prüft nicht, ob die eingeladene Person erziehungsberechtigt ist. Sie bestätigt das selbst.</li>
<li>Sperrst du deinen Zugang, werden deine offenen Einladungen ungültig.</li>
</ul></section>
<p class="yp-muted">Vorschau mit synthetischen Profilen.</p>
</div></main></body></html>`))

var inviteLinkPage = template.Must(template.New("invite-link").Parse(inviteHead + `
<a class="yp-back" href="/family/invite">← Zurück zu den Einladungen</a>
<h1>Dein Einladungslink</h1>
<a class="yp-code yp-link-code" href="{{.Link}}" rel="noreferrer">{{.Link}}</a>
<p>Tippe lange auf den Link, um ihn zu kopieren oder zu teilen. Gib ihn nur einer erwachsenen Person weiter, die du persönlich kennst.</p>
{{if .Child}}<p class="yp-status">Wer sich mit diesem Link registriert, kann sein Kind mit {{.Child}} verbinden. Gib ihn deshalb nur der Familie, die du meinst.</p>{{end}}
<p class="yp-muted">Der Link gilt 7 Tage und nur einmal. Er wird hier nur jetzt angezeigt.</p>
<p class="yp-muted">Vorschau mit synthetischen Profilen.</p>
</div></main></body></html>`))

const inviteCSS = `.yp-page fieldset.yp-choice{margin:0;padding:0;border:0;min-width:0}
.yp-choice legend{padding:0;font-weight:700}
.yp-page a.yp-link-code{font-size:1rem;letter-spacing:0;text-align:left;color:var(--yp-accent-text)}
.yp-page a.yp-link-code:focus-visible{outline:3px solid var(--yp-focus);outline-offset:2px}
`

// skinnedCSS returns the guardian's skin id and the page styles: validated
// first-party skin tokens plus fixed component rules. looks may be nil.
func skinnedCSS(looks AppearanceStore, p Session, extra string) (string, template.CSS, error) {
	catalog := skin.Catalog()
	id := "classic"
	if looks != nil {
		id = looks.Get(appearance.Subject{Kind: appearance.KindGuardian, TenantID: p.TenantID, ID: p.GuardianID}).Skin
	}
	def := skin.Resolve(id, catalog, catalog["classic"])
	decls, err := skin.Declarations(def)
	if err != nil {
		return "", "", err
	}
	fonts := fontassets.FaceCSS(def.Typography.Heading, def.Typography.Body)
	return def.ID, template.CSS(fonts + ".yp-page{" + decls + "}\n" + contactCSS + lostCSS + extra), nil
}

func (h *inviteHandler) css(p Session) (string, template.CSS, error) {
	return skinnedCSS(h.looks, p, inviteCSS)
}

// fullGuardian reports whether the session holds full authority for at
// least one child of its tenant. Only such a guardian invites or adds.
func fullGuardian(families FamilyStore, p Session) bool {
	for _, c := range families.Cards(p.GuardianID, p.TenantID) {
		if c.Permissions.ManageDevices && c.Permissions.ApproveContacts {
			return true
		}
	}
	return false
}

func (h *inviteHandler) render(w http.ResponseWriter, p Session, status int, alert, notice string) {
	skinID, css, err := h.css(p)
	if err != nil {
		http.Error(w, "Unable to display invitations", http.StatusInternalServerError)
		return
	}
	type row struct{ ID, Created, Expires, Child string }
	children := h.connectable(p)
	names := map[string]string{}
	for _, c := range children {
		names[c.ID] = c.Name
	}
	pending := h.invitations.Pending(p.GuardianID, p.TenantID)
	rows := make([]row, 0, len(pending))
	for _, inv := range pending {
		rows = append(rows, row{ID: inv.ID, Created: inv.CreatedAt.Local().Format("02.01.2006"), Expires: inv.ExpiresAt.Local().Format("02.01.2006 15:04"), Child: names[inv.Child]})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = invitePage.Execute(w, struct {
		Skin, CSRF, Alert, Notice string
		CSS                       template.CSS
		Pending                   []row
		Children                  []inviteChild
		CanCreate                 bool
		Connect, ParentsChat      bool
		Max                       int
	}{Skin: skinID, CSRF: p.CSRFToken, Alert: alert, Notice: notice, CSS: css, Pending: rows, Children: children,
		CanCreate: len(rows) < signup.MaxOpenPerGuardian, Connect: h.connect, ParentsChat: h.parentsChat, Max: signup.MaxOpenPerGuardian})
}

type inviteChild struct{ ID, Name string }

// connectable lists the children for whom this guardian may approve
// contacts right now. Only these can be named in an invitation.
func (h *inviteHandler) connectable(p Session) []inviteChild {
	if !h.connect {
		return nil
	}
	var out []inviteChild
	for _, c := range h.families.Cards(p.GuardianID, p.TenantID) {
		if c.Permissions.ApproveContacts {
			out = append(out, inviteChild{ID: c.ID, Name: c.DisplayName})
		}
	}
	return out
}

func (h *inviteHandler) view(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	if !fullGuardian(h.families, p) {
		http.Error(w, "Not authorized", http.StatusForbidden)
		return
	}
	// Only fixed, known notices are shown; the query text is never rendered.
	h.render(w, p, http.StatusOK, "", inviteNotices[r.URL.Query().Get("done")])
}

func (h *inviteHandler) create(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	// Where children can be connected the guardian must choose, also "no".
	fields := []string{}
	if h.connect {
		fields = append(fields, "child_id")
	}
	form, ok := postedFields(w, r, p, fields...)
	if !ok {
		return
	}
	if !h.connect && len(form["child_id"]) != 0 {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	if !fullGuardian(h.families, p) {
		http.Error(w, "Not authorized", http.StatusForbidden)
		return
	}
	child, childName := form.Get("child_id"), ""
	if child != "" {
		allowed := false
		for _, c := range h.connectable(p) {
			if c.ID == child {
				allowed, childName = true, c.Name
			}
		}
		if !allowed {
			http.Error(w, "Not authorized", http.StatusForbidden)
			return
		}
	}
	code, _, err := h.invitations.Invite(p.GuardianID, p.TenantID, child)
	switch {
	case errors.Is(err, signup.ErrLimit):
		h.render(w, p, http.StatusTooManyRequests, "Mehr offene Einladungen sind nicht möglich. Ziehe eine zurück oder warte, bis eine abläuft.", "")
		return
	case errors.Is(err, signup.ErrFull):
		h.render(w, p, http.StatusConflict, "Auf dieser Yapuno-Installation können sich zurzeit keine weiteren Familien registrieren.", "")
		return
	case err != nil:
		http.Error(w, "Unable to create invitation", http.StatusInternalServerError)
		return
	}
	skinID, css, err := h.css(p)
	if err != nil {
		http.Error(w, "Unable to display invitation", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = inviteLinkPage.Execute(w, struct {
		Skin, Link, Child string
		CSS               template.CSS
	}{Skin: skinID, Link: h.origin + "/join?code=" + url.QueryEscape(code), Child: childName, CSS: css})
}

func (h *inviteHandler) withdraw(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	form, ok := postedFields(w, r, p, "invitation_id")
	if !ok {
		return
	}
	id := form.Get("invitation_id")
	if !invitationIDPattern.MatchString(id) {
		http.Error(w, "Invalid invitation", http.StatusBadRequest)
		return
	}
	switch err := h.invitations.Withdraw(p.GuardianID, p.TenantID, id); {
	case errors.Is(err, signup.ErrNotFound):
		http.Error(w, "Invitation not found", http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, "Unable to withdraw invitation", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/family/invite?done=withdrawn", http.StatusSeeOther)
}
