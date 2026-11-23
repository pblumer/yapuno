package familyweb

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/fontassets"
	"github.com/pblumer/yapuno/internal/guardianchat"
	"github.com/pblumer/yapuno/internal/skin"
)

const homeCSS = `.yp-home-top{display:flex;align-items:center;justify-content:space-between;gap:1rem}
.yp-logo{font-family:var(--yp-font-heading);font-weight:850;font-size:1.4rem;letter-spacing:-.05em}
.yp-logo span{color:var(--yp-accent-text)}
.yp-me{display:grid;place-items:center;min-width:52px;min-height:52px;border-radius:50%;color:var(--yp-text)}
.yp-page ul.yp-rows{display:flex;flex-direction:column;gap:.5rem}
.yp-rows a{display:flex;align-items:center;gap:.6rem;min-height:52px;padding:.6rem .9rem;border:1px solid var(--yp-line);border-radius:var(--yp-radius);background:var(--yp-bg);color:var(--yp-text);font-weight:700;text-decoration:none;overflow-wrap:anywhere}
.yp-rows a small{display:block;font-weight:400;color:var(--yp-text-muted)}
.yp-rows a span:first-child{flex:1;min-width:0}
.yp-rows a .yp-go{flex:none;color:var(--yp-accent-text);font-size:1.4rem}
.yp-page details{margin-top:.8rem}
.yp-page summary{display:flex;align-items:center;min-height:44px;color:var(--yp-accent-text);font-weight:700;cursor:pointer}
.yp-page ol.yp-steps li{margin:.5rem 0}
.yp-rows a:focus-visible,.yp-me:focus-visible,.yp-page summary:focus-visible{outline:3px solid var(--yp-focus);outline-offset:2px}
`

var homePage = template.Must(template.New("home").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Yapuno – Familie</title><style>{{.CSS}}</style></head><body><main class="yp-page" data-yp-skin="{{.Skin}}"><div class="yp-shell">
<header class="yp-home-top"><div class="yp-logo">ya<span>puno</span></div>{{if .Figure}}<a class="yp-me" href="/family/look" aria-label="Mein Look">{{.Figure}}</a>{{end}}</header>
<h1>Deine Familie</h1>
<p class="yp-muted">Hier richtest du ein, womit und mit wem deine Kinder schreiben.</p>
{{if .Alert}}<p class="yp-alert" role="alert">{{.Alert}}</p>{{end}}
{{if .Notice}}<p class="yp-status" role="status">{{.Notice}}</p>{{end}}
{{range .Children}}<section aria-labelledby="yp-child-{{.N}}"><h2 id="yp-child-{{.N}}">{{.Name}}</h2>
{{if or .Devices .Contacts .Chat}}<ul class="yp-rows">
{{if .Devices}}<li><a href="/family/devices?child_id={{.ID}}"><span>Geräte von {{.Name}}<small>Handy oder Tablet verbinden und verwalten</small></span><span class="yp-go" aria-hidden="true">›</span></a></li>{{end}}
{{if .Contacts}}<li><a href="/family/contacts?child_id={{.ID}}"><span>Kontakte von {{.Name}}<small>{{if .Reports}}{{.Reports}}{{else}}Mit Kindern anderer Familien verbinden{{end}}</small></span><span class="yp-go" aria-hidden="true">›</span></a></li>{{end}}
{{if .Chat}}<li><a href="/family/chat/{{.Chat}}"><span>Mit {{.Name}} schreiben</span><span class="yp-go" aria-hidden="true">›</span></a></li>{{end}}
{{if .Devices}}<li><a href="/family/devices/lost?child_id={{.ID}}"><span>Gerät von {{.Name}} verloren?</span><span class="yp-go" aria-hidden="true">›</span></a></li>{{end}}
</ul>{{end}}{{if not (or .Devices .Contacts)}}<p class="yp-muted">{{if .Full}}Geräte und Kontakte sind auf dieser Installation nicht eingeschaltet.{{else}}Für dieses Profil hast du keine Verwaltungsrechte.{{end}}</p>{{end}}
{{if .Full}}<details><summary>Namen ändern</summary><form method="post" action="/family/rename"><input type="hidden" name="csrf_token" value="{{$.CSRF}}"><input type="hidden" name="child_id" value="{{.ID}}"><label class="yp-field" for="yp-name-{{.N}}">Neuer Name für {{.Name}}</label><input type="text" id="yp-name-{{.N}}" name="name" value="{{.Name}}" required maxlength="40" autocomplete="off"><button class="yp-secondary" type="submit">Speichern</button></form></details>{{end}}
{{if and .Full $.DeleteEnabled}}<p><a class="yp-back" href="/family/children/delete?child_id={{.ID}}">Profil von {{.Name}} löschen</a></p>{{end}}
</section>{{else}}<section aria-labelledby="yp-none"><h2 id="yp-none">Noch kein Kind</h2><p class="yp-muted">Für dich ist kein Kinderprofil freigegeben.</p></section>{{end}}
<section aria-labelledby="yp-family"><h2 id="yp-family">Familie</h2><ul class="yp-rows">
{{if .ChatEnabled}}<li><a href="/family/chat"><span>Chats<small>Mit deinen Kindern und mit anderen Eltern schreiben</small></span><span class="yp-go" aria-hidden="true">›</span></a></li>{{end}}
{{if .CanAddChild}}<li><a href="/family/children"><span>Weiteres Kind anlegen</span><span class="yp-go" aria-hidden="true">›</span></a></li>{{end}}
{{if .CanInvite}}<li><a href="/family/invite"><span>Eine Familie einladen<small>Damit sie sich selbst registrieren kann</small></span><span class="yp-go" aria-hidden="true">›</span></a></li>{{end}}
{{if .LookEnabled}}<li><a href="/family/look"><span>Mein Look ändern</span><span class="yp-go" aria-hidden="true">›</span></a></li>{{end}}
{{if .SecurityEnabled}}<li><a href="/family/security"><span>Sicherheit und Abmelden</span><span class="yp-go" aria-hidden="true">›</span></a></li>{{end}}
</ul></section>
<section aria-labelledby="yp-guide"><h2 id="yp-guide">So kommt ihr zum ersten Chat</h2><ol class="yp-steps">
<li><strong>Gerät verbinden.</strong> {{if .DevicesEnabled}}Öffne auf dem Handy oder Tablet deines Kindes die Adresse, die unter «Geräte» steht. Das Gerät zeigt einen Code mit sechs Ziffern; tippe ihn unter «Geräte» ein.{{else}}Das Verbinden von Geräten ist auf dieser Installation nicht eingeschaltet.{{end}}</li>
<li><strong>Zweite Familie.</strong> {{if .InviteConnects}}Öffne «Eine Familie einladen», wähle das Kind, das verbunden werden soll, und gib den Link einer erwachsenen Person, die du kennst. Sie registriert sich damit selbst, stimmt der Verbindung zu und verbindet ebenfalls ein Gerät.{{else if .CanInvite}}Öffne «Eine Familie einladen» und gib den Link einer erwachsenen Person, die du kennst. Sie registriert sich damit selbst und verbindet ebenfalls ein Gerät.{{else}}Weitere Familien legt der Betreiber dieser Installation an.{{end}}</li>
<li><strong>Kinder verbinden.</strong> {{if .InviteConnects}}Mit der Einladung sind die Kinder schon verbunden. Ist die andere Familie bereits registriert, öffne «Kontakte», erstelle einen Code und gib ihn ihr. Sie löst ihn ein, danach gibst du die Anfrage frei.{{else if .ContactsEnabled}}Öffne «Kontakte», erstelle einen Code und gib ihn der anderen Familie. Sie löst ihn ein, danach gibst du die Anfrage frei.{{else}}Kontakte zwischen Familien sind auf dieser Installation nicht eingeschaltet.{{end}}</li>
<li><strong>Schreiben.</strong> Die Kinder schreiben auf ihren Geräten miteinander. {{if .ChatEnabled}}Du schreibst unter «Chats» mit deinem Kind und, sobald die Kinder verbunden sind, mit den anderen Eltern.{{else}}Der Chat für Eltern ist auf dieser Installation nicht eingeschaltet.{{end}}</li>
</ol></section>
{{if .Inactive}}<p class="yp-muted">Auf dieser Installation nicht eingeschaltet: {{.Inactive}}. Der Betreiber schaltet Funktionen in der Konfiguration ein.</p>{{end}}
<p class="yp-muted">Vorschau mit synthetischen Profilen.{{if .Version}} Version {{.Version}}{{end}}</p>
</div></main></body></html>`))

// homeNotices and homeAlerts are the only texts a redirect can select. They
// tell a guardian who has just registered or signed in what became of
// something they asked for.
var (
	homeNotices = map[string]string{
		"connected":     "Deine Familie ist angelegt und die Kinder sind verbunden. Verbinde als Nächstes das Gerät deines Kindes.",
		"child-deleted": "Das Profil ist gelöscht.",
	}
	homeAlerts = map[string]string{
		"unconnected": "Deine Familie ist angelegt, aber die Kinder wurden nicht verbunden. Verbindet sie unter «Kontakte» mit einem Code.",
		// The guardian asked to stay signed in, and the session could not be stored.
		// The profile is gone; a message log could not be rewritten yet.
		"child-deleted-leftovers": "Das Profil ist gelöscht. Einzelne Chat-Nachrichten werden erst beim nächsten Aufräumen des Servers entfernt; lesen kann sie niemand mehr.",
		"not-remembered":          "Du bist angemeldet, aber nur für kurze Zeit: Die Anmeldung konnte auf diesem Gerät nicht gespeichert werden.",
	}
)

// shortVersion keeps a build stamp readable on a phone: a commit id is cut
// to its first twelve characters.
func shortVersion(v string) string {
	if len(v) > 12 {
		return v[:12]
	}
	return v
}

// homeChild is one child of the guardian with exactly the actions the
// guardian's grant and this installation allow.
type homeChild struct {
	N        int
	ID, Name string
	Devices  bool
	Contacts bool
	Full     bool
	Chat     string // id of the guardian's conversation with this child
	Reports  string
}

func (h *handler) view(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := h.session(w, r)
	if !ok {
		return
	}
	cards := h.store.Cards(p.GuardianID, p.TenantID)
	full := fullGuardian(h.store, p)
	// ?skin= previews a skin for this request only; it never changes storage.
	skinID := r.URL.Query().Get("skin")
	var figure template.HTML
	if h.looks != nil {
		pref := h.looks.Get(guardianSubject(p))
		if skinID == "" {
			skinID = pref.Skin
		}
		if svg, err := appearance.FigureSVG(pref.Figure, 44); err == nil {
			figure = svg
		}
	}
	catalog := skin.Catalog()
	def := skin.Resolve(skinID, catalog, catalog["classic"])
	decls, err := skin.Declarations(def)
	if err != nil {
		http.Error(w, "Unable to display family", http.StatusInternalServerError)
		return
	}
	chats := map[string]string{}
	if h.chat != nil {
		for _, t := range h.chat.Threads(actorOf(p)) {
			if t.Kind == guardianchat.Family {
				chats[t.Peer.ID] = t.ID
			}
		}
	}
	reports := h.openReports(p, cards)
	children := make([]homeChild, 0, len(cards))
	for i, c := range cards {
		children = append(children, homeChild{N: i + 1, ID: c.ID, Name: c.DisplayName,
			Devices:  h.devicesEnabled && c.Permissions.ManageDevices,
			Contacts: h.contactsEnabled && c.Permissions.ApproveContacts,
			Full:     c.Permissions.ManageDevices && c.Permissions.ApproveContacts,
			Chat:     chats[c.ID], Reports: reports[c.ID]})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Only validated first-party skin tokens and fixed component rules.
	fonts := fontassets.FaceCSS(def.Typography.Heading, def.Typography.Body)
	_ = homePage.Execute(w, struct {
		Skin, CSRF, Inactive, Version string
		Alert, Notice                 string
		CSS                           template.CSS
		Figure                        template.HTML
		Children                      []homeChild

		DevicesEnabled, ContactsEnabled, ChatEnabled bool
		LookEnabled, SecurityEnabled                 bool
		CanAddChild, CanInvite, InviteConnects       bool
		DeleteEnabled                                bool
	}{DeleteEnabled: h.deleteEnabled, Alert: homeAlerts[r.URL.Query().Get("done")], Notice: homeNotices[r.URL.Query().Get("done")], Skin: def.ID, CSRF: p.CSRFToken, Inactive: strings.Join(h.inactive, ", "), Version: shortVersion(h.version),
		CSS: template.CSS(fonts + ".yp-page{" + decls + "}\n" + contactCSS + lostCSS + homeCSS), Figure: figure, Children: children,
		DevicesEnabled: h.devicesEnabled, ContactsEnabled: h.contactsEnabled, ChatEnabled: h.chat != nil,
		LookEnabled: h.looks != nil, SecurityEnabled: h.securityEnabled,
		CanAddChild: h.children != nil && full, CanInvite: h.inviteEnabled && full,
		InviteConnects: h.inviteEnabled && full && h.contactsEnabled})
}
