package familyweb

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/fontassets"
	"github.com/pblumer/yapuno/internal/guardianlock"
	"github.com/pblumer/yapuno/internal/skin"
)

// GuardianSessions ends guardian sessions. Count and RevokeAll must be
// scoped to exactly the guardian and tenant they are given.
type GuardianSessions interface {
	Count(guardianID, tenantID string) int
	Revoke(r *http.Request)
	RevokeAll(guardianID, tenantID string) int
	Clear(w http.ResponseWriter)
}

// SignInLinks invalidates sign-in links that were sent but not used yet.
type SignInLinks interface {
	Cancel(guardianID, tenantID string) int
}

// AccessLocker durably locks a guardian's own access and tells the other
// guardians of the same children.
type AccessLocker interface {
	Lock(ctx context.Context, guardianID, tenantID string) (guardianlock.Report, error)
}

// ChildDeviceLocker locks every device of one child. It must re-check the
// guardian's current device-management authority.
type ChildDeviceLocker interface {
	RevokeAll(guardian, tenant, child string) (int, error)
}

// SecurityOptions selects the optional parts of the security page.
type SecurityOptions struct {
	Locker  AccessLocker      // nil: no emergency lock
	Devices ChildDeviceLocker // nil: the lock cannot also lock child devices
	Looks   AppearanceStore   // nil: classic skin
}

// NewSecurityPages serves sign-out, sign-out everywhere and the emergency
// lock for the authenticated guardian. Every action applies to the guardian
// of the session only; no request field can name another guardian.
func NewSecurityPages(auth Authenticator, sessions GuardianSessions, links SignInLinks, families FamilyStore, opt SecurityOptions) (http.Handler, error) {
	if auth == nil || sessions == nil || links == nil || families == nil {
		return nil, errors.New("authenticator, sessions, sign-in links and family store are required")
	}
	h := &securityHandler{auth: auth, sessions: sessions, links: links, families: families, opt: opt}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /family/security", h.view)
	mux.HandleFunc("POST /family/security/logout", h.logout)
	mux.HandleFunc("POST /family/security/logout-all", h.logoutAll)
	if opt.Locker != nil {
		mux.HandleFunc("POST /family/security/lock", h.lock)
	}
	return mux, nil
}

type securityHandler struct {
	auth     Authenticator
	sessions GuardianSessions
	links    SignInLinks
	families FamilyStore
	opt      SecurityOptions
}

const securityHead = `<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Yapuno – Sicherheit</title><style>{{.CSS}}</style></head><body><main class="yp-page" data-yp-skin="{{.Skin}}"><div class="yp-shell">`

var securityPage = template.Must(template.New("security").Parse(securityHead + `
<a class="yp-back" href="/family">← Zurück zur Familie</a>
<h1>Sicherheit</h1>
<p class="yp-muted">Du hast zurzeit {{if eq .Count 1}}1 aktive Anmeldung.{{else}}{{.Count}} aktive Anmeldungen.{{end}}</p>
<section aria-labelledby="yp-out"><h2 id="yp-out">Abmelden</h2>
<p class="yp-muted">Beendet die Anmeldung in diesem Browser.</p>
<form method="post" action="/family/security/logout"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><button class="yp-primary" type="submit">Abmelden</button></form></section>
<section aria-labelledby="yp-everywhere"><h2 id="yp-everywhere">Überall abmelden</h2>
<p class="yp-muted">Beendet alle deine Anmeldungen auf allen Geräten und macht noch nicht benutzte Anmeldelinks ungültig. Du kannst dich danach wieder anmelden. Das kann auch, wer Zugriff auf dein E-Mail-Postfach hat.</p>
<form method="post" action="/family/security/logout-all"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><label class="yp-check"><input type="checkbox" name="confirm" value="yes" required> <span>Alle meine Anmeldungen beenden.</span></label><button class="yp-secondary" type="submit">Überall abmelden</button></form></section>
<section aria-labelledby="yp-lock"><h2 id="yp-lock">Gerät oder Postfach in fremden Händen?</h2>
{{if .LockEnabled}}<p class="yp-muted">Sperre deinen Zugang, wenn dein Gerät verloren ging und jemand an dein E-Mail-Postfach kommen könnte.</p>
<ul class="yp-facts">
<li>Alle deine Anmeldungen enden sofort, und es werden keine Anmeldelinks mehr an deine Adresse verschickt.</li>
<li>Die anderen Elternteile deiner Kinder erhalten eine E-Mail, damit sie Geräte und Kontakte prüfen können.</li>
<li>Deine Kinder können weiter schreiben. Ihre Geräte bleiben verbunden, ausser du sperrst sie unten mit.</li>
<li>Nur der Betreiber dieser Yapuno-Installation kann die Sperre wieder aufheben. Du selbst kannst das nicht.</li>
</ul>
<form method="post" action="/family/security/lock"><input type="hidden" name="csrf_token" value="{{.CSRF}}">{{if .DevicesEnabled}}<label class="yp-check"><input type="checkbox" name="devices" value="yes"> <span>Zusätzlich alle Geräte meiner Kinder sperren. Sinnvoll, wenn jemand Fremdes ein Gerät verbunden haben könnte. Neu verbinden kann sie danach nur ein Elternteil mit Zugang.</span></label>{{end}}<label class="yp-check"><input type="checkbox" name="confirm" value="yes" required> <span>Meinen Zugang sperren. Ich weiss, dass nur der Betreiber ihn wieder freigeben kann.</span></label><button class="yp-danger" type="submit">Meinen Zugang sperren</button></form>
{{else}}<p class="yp-muted">Die Notfallsperre ist auf dieser Installation nicht eingerichtet. Melde dich überall ab, ändere das Passwort deines E-Mail-Postfachs und wende dich an den Betreiber.</p>{{end}}</section>
<p class="yp-muted">Vorschau mit synthetischen Profilen.</p>
</div></main></body></html>`))

var lockedPage = template.Must(template.New("locked").Parse(securityHead + `
<h1>Dein Zugang ist gesperrt</h1>
<p class="yp-status" role="status">Alle deine Anmeldungen sind beendet. Es werden keine Anmeldelinks mehr an deine Adresse verschickt.</p>
<section aria-labelledby="yp-done"><h2 id="yp-done">Was geschehen ist</h2><ul class="yp-facts">
{{if .Told}}<li>{{.Told}}</li>{{end}}
{{if .Devices}}<li>{{.Devices}}</li>{{end}}
<li>Deine Kinder können weiter mit ihren freigegebenen Kontakten schreiben.</li>
</ul></section>
<section aria-labelledby="yp-next"><h2 id="yp-next">So geht es weiter</h2><ul class="yp-facts">
<li>Ändere das Passwort deines Postfachs und sichere oder ersetze das Gerät.</li>
<li>Wende dich danach an den Betreiber dieser Yapuno-Installation. Nur er kann die Sperre aufheben.</li>
<li>Prüft nach der Freigabe die verbundenen Geräte und die Kontakte eurer Kinder.</li>
</ul></section>
<p class="yp-muted">Du kannst dieses Fenster schliessen.</p>
</div></main></body></html>`))

func (h *securityHandler) css(p Session) (string, template.CSS, error) {
	catalog := skin.Catalog()
	id := "classic"
	if h.opt.Looks != nil {
		id = h.opt.Looks.Get(appearance.Subject{Kind: appearance.KindGuardian, TenantID: p.TenantID, ID: p.GuardianID}).Skin
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

func (h *securityHandler) view(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	skinID, css, err := h.css(p)
	if err != nil {
		http.Error(w, "Unable to display security settings", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = securityPage.Execute(w, struct {
		Skin, CSRF                  string
		CSS                         template.CSS
		Count                       int
		LockEnabled, DevicesEnabled bool
	}{Skin: skinID, CSRF: p.CSRFToken, CSS: css, Count: h.sessions.Count(p.GuardianID, p.TenantID),
		LockEnabled: h.opt.Locker != nil, DevicesEnabled: h.opt.Locker != nil && h.opt.Devices != nil})
}

func (h *securityHandler) logout(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	if _, ok := postedFields(w, r, p); !ok {
		return
	}
	h.sessions.Revoke(r)
	h.sessions.Clear(w)
	http.Redirect(w, r, "/login?notice=signed-out", http.StatusSeeOther)
}

// confirmedFields is postedFields plus the explicit confirmation every
// far-reaching action on this page needs.
func confirmedFields(w http.ResponseWriter, r *http.Request, p Session) (url.Values, bool) {
	form, ok := postedFields(w, r, p, "confirm")
	if !ok {
		return nil, false
	}
	if form.Get("confirm") != "yes" {
		http.Error(w, "Explicit confirmation required", http.StatusBadRequest)
		return nil, false
	}
	return form, true
}

func (h *securityHandler) logoutAll(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	if _, ok := confirmedFields(w, r, p); !ok {
		return
	}
	h.endEverything(w, p)
	http.Redirect(w, r, "/login?notice=signed-out-everywhere", http.StatusSeeOther)
}

// endEverything ends all sessions of the session's guardian, invalidates
// unused sign-in links and drops this browser's cookie.
func (h *securityHandler) endEverything(w http.ResponseWriter, p Session) {
	h.sessions.RevokeAll(p.GuardianID, p.TenantID)
	h.links.Cancel(p.GuardianID, p.TenantID)
	h.sessions.Clear(w)
}

func (h *securityHandler) lock(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	form, ok := confirmedFields(w, r, p)
	if !ok {
		return
	}
	withDevices := false
	switch choice := form["devices"]; {
	case len(choice) == 0:
	case len(choice) == 1 && choice[0] == "yes" && h.opt.Devices != nil:
		withDevices = true
	default:
		http.Error(w, "Invalid device option", http.StatusBadRequest)
		return
	}
	// Child devices first: if that fails the guardian still has a session
	// and can try again. After the access lock nobody could retry.
	devices := 0
	if withDevices {
		for _, card := range h.families.Cards(p.GuardianID, p.TenantID) {
			if !card.Permissions.ManageDevices {
				continue
			}
			n, err := h.opt.Devices.RevokeAll(p.GuardianID, p.TenantID, card.ID)
			if err != nil {
				http.Error(w, "Unable to lock the children's devices; your access was not locked", http.StatusInternalServerError)
				return
			}
			devices += n
		}
	}
	// The skin is resolved while the guardian is still known to be active.
	skinID, css, err := h.css(p)
	if err != nil {
		http.Error(w, "Unable to lock access", http.StatusInternalServerError)
		return
	}
	report, err := h.opt.Locker.Lock(r.Context(), p.GuardianID, p.TenantID)
	if err != nil {
		http.Error(w, "Unable to lock access", http.StatusInternalServerError)
		return
	}
	h.endEverything(w, p)
	told := ""
	switch {
	case report.AlreadyLocked:
	case report.CoGuardians == 0:
		told = "Für deine Kinder ist kein anderer Elternteil eingetragen. Bis die Sperre aufgehoben ist, kann niemand ihre Geräte und Kontakte verwalten."
	case report.Notified == report.CoGuardians && report.CoGuardians == 1:
		told = "Dem anderen Elternteil deiner Kinder wurde eine E-Mail gesendet."
	case report.Notified == report.CoGuardians:
		told = "Den anderen Elternteilen deiner Kinder wurde eine E-Mail gesendet."
	case report.CoGuardians == 1:
		told = "Der andere Elternteil deiner Kinder konnte nicht per E-Mail benachrichtigt werden. Informiere ihn bitte selbst."
	default:
		told = "Nicht alle anderen Elternteile deiner Kinder konnten per E-Mail benachrichtigt werden. Informiere sie bitte selbst."
	}
	deviceLine := ""
	if withDevices {
		switch devices {
		case 0:
			deviceLine = "Für deine Kinder war kein Kindergerät verbunden."
		case 1:
			deviceLine = "1 Kindergerät wurde gesperrt."
		default:
			deviceLine = strconv.Itoa(devices) + " Kindergeräte wurden gesperrt."
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = lockedPage.Execute(w, struct {
		Skin, Told, Devices string
		CSS                 template.CSS
	}{Skin: skinID, Told: told, Devices: deviceLine, CSS: css})
}
