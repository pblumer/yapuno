package familyweb

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"regexp"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/fontassets"
	"github.com/pblumer/yapuno/internal/skin"
)

// LostDeviceStore locks paired devices. It must re-check the guardian's
// current device-management authority for the named child on every call.
type LostDeviceStore interface {
	ListDevices(guardian, tenant, child string) ([]devicepairing.Device, error)
	Revoke(guardian, tenant, child, deviceID string) error
	RevokeAll(guardian, tenant, child string) (int, error)
}

// Device ids are 16 random bytes in unpadded base64url.
var deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// NewLostDevicePages serves the "device lost" page: a guardian locks one or
// all devices of a child. Guardian and tenant always come from the session;
// the child is named by the request and authorized by the store. looks may
// be nil.
func NewLostDevicePages(auth Authenticator, devices LostDeviceStore, families FamilyStore, looks AppearanceStore) (http.Handler, error) {
	if auth == nil || devices == nil || families == nil {
		return nil, errors.New("authenticator, device store and family store are required")
	}
	h := &lostHandler{auth: auth, devices: devices, families: families, looks: looks}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /family/devices/lost", h.view)
	mux.HandleFunc("POST /family/devices/lost", h.lock)
	return mux, nil
}

type lostHandler struct {
	auth     Authenticator
	devices  LostDeviceStore
	families FamilyStore
	looks    AppearanceStore
}

// lostNotices are the only texts a redirect can select.
var lostNotices = map[string]string{
	"one": "Das Gerät ist gesperrt.",
	"all": "Alle Geräte sind gesperrt. Laufende Kopplungen wurden abgebrochen.",
}

var lostPage = template.Must(template.New("lost").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Yapuno – Gerät verloren</title><style>{{.CSS}}</style></head><body><main class="yp-page" data-yp-skin="{{.Skin}}"><div class="yp-shell">
<a class="yp-back" href="/family">← Zurück zur Familie</a>
<h1>Gerät von {{.Name}} verloren?</h1>
<p class="yp-muted">Ist ein Gerät verloren gegangen oder gestohlen worden, kannst du es hier sperren. Die Sperre gilt sofort auf dem Server.</p>
{{if .Notice}}<p class="yp-status" role="status">{{.Notice}}</p>{{end}}
{{if .Devices}}<section aria-labelledby="yp-devices"><h2 id="yp-devices">Verbundene Geräte</h2><ul>{{range .Devices}}<li class="yp-item"><strong>{{.Name}}</strong>
<form method="post" action="/family/devices/lost"><input type="hidden" name="csrf_token" value="{{$.CSRF}}"><input type="hidden" name="child_id" value="{{$.Child}}"><input type="hidden" name="scope" value="one"><input type="hidden" name="device_id" value="{{.ID}}"><label class="yp-check"><input type="checkbox" name="confirm" value="yes" required> <span>«{{.Name}}» sperren. Dieses Gerät kann sich danach nicht mehr anmelden.</span></label><button class="yp-danger" type="submit">Dieses Gerät sperren</button></form></li>{{end}}</ul></section>
<section aria-labelledby="yp-all"><h2 id="yp-all">Nicht sicher, welches Gerät es ist?</h2>
<p class="yp-muted">Sperre alle Geräte von {{.Name}} und verbinde danach jene neu, die ihr noch habt. Eine gerade laufende Kopplung wird dabei abgebrochen.</p>
<form method="post" action="/family/devices/lost"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="child_id" value="{{.Child}}"><input type="hidden" name="scope" value="all"><label class="yp-check"><input type="checkbox" name="confirm" value="yes" required> <span>Alle Geräte von {{.Name}} sperren.</span></label><button class="yp-danger" type="submit">Alle Geräte sperren</button></form></section>
{{else}}<section aria-labelledby="yp-devices"><h2 id="yp-devices">Verbundene Geräte</h2><p class="yp-muted">Für {{.Name}} ist zurzeit kein Gerät verbunden. Es gibt nichts zu sperren.</p></section>{{end}}
<section aria-labelledby="yp-effect"><h2 id="yp-effect">Was die Sperre bewirkt</h2><ul class="yp-facts">
<li>Das Gerät verliert den Zugang, sobald es das nächste Mal den Server erreicht. Ist es online, schliesst sich ein geöffneter Chat innert Sekunden.</li>
<li>Solange das Gerät offline ist, kann dort bereits Angezeigtes sichtbar bleiben. Neue Nachrichten erreichen es nicht mehr.</li>
<li>Nachrichten und Kontakte von {{.Name}} bleiben erhalten.</li>
<li>Für ein neues oder wiedergefundenes Gerät braucht es einen neuen Kopplungs-Code.</li>
</ul>
<p><a class="yp-back" href="/family/devices?child_id={{.Child}}">Neues Gerät verbinden</a></p></section>
<p class="yp-muted">Vorschau mit synthetischen Profilen.</p>
</div></main></body></html>`))

const lostCSS = `.yp-page ul.yp-facts{list-style:disc;padding-left:1.3rem;color:var(--yp-text-muted)}
.yp-page ul.yp-facts li{margin:.4rem 0}
`

func (h *lostHandler) css(p Session) (string, template.CSS, error) {
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

func (h *lostHandler) childName(p Session, child string) (string, bool) {
	for _, c := range h.families.Cards(p.GuardianID, p.TenantID) {
		if c.ID == child && c.Permissions.ManageDevices {
			return c.DisplayName, true
		}
	}
	return "", false
}

func (h *lostHandler) view(w http.ResponseWriter, r *http.Request) {
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
		respondDeviceError(w, devicepairing.ErrForbidden)
		return
	}
	all, err := h.devices.ListDevices(p.GuardianID, p.TenantID, child)
	if err != nil {
		respondDeviceError(w, err)
		return
	}
	// Locked devices cannot be locked again and are not offered.
	active := make([]devicepairing.Device, 0, len(all))
	for _, d := range all {
		if !d.Revoked {
			active = append(active, d)
		}
	}
	skinID, css, err := h.css(p)
	if err != nil {
		http.Error(w, "Unable to display devices", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Only fixed, known notices are shown; the query text is never rendered.
	_ = lostPage.Execute(w, struct {
		Skin, Name, Child, CSRF, Notice string
		CSS                             template.CSS
		Devices                         []devicepairing.Device
	}{Skin: skinID, Name: name, Child: child, CSRF: p.CSRFToken, Notice: lostNotices[query.Get("done")], CSS: css, Devices: active})
}

func (h *lostHandler) lock(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := authenticated(h.auth, w, r)
	if !ok {
		return
	}
	form, ok := postedForm(w, r, p, "child_id", "scope", "confirm")
	if !ok {
		return
	}
	if form.Get("confirm") != "yes" {
		http.Error(w, "Explicit confirmation required", http.StatusBadRequest)
		return
	}
	child, scope, ids := form.Get("child_id"), form.Get("scope"), form["device_id"]
	var err error
	switch {
	case scope == "one" && len(ids) == 1 && deviceIDPattern.MatchString(ids[0]):
		err = h.devices.Revoke(p.GuardianID, p.TenantID, child, ids[0])
	case scope == "all" && len(ids) == 0:
		_, err = h.devices.RevokeAll(p.GuardianID, p.TenantID, child)
	default:
		// The scope must be unambiguous: one named device, or all of them.
		http.Error(w, "Invalid device request", http.StatusBadRequest)
		return
	}
	if err != nil {
		respondDeviceError(w, err)
		return
	}
	http.Redirect(w, r, "/family/devices/lost?child_id="+url.QueryEscape(child)+"&done="+scope, http.StatusSeeOther)
}
