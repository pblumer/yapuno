package familyweb

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/pblumer/yapuno/internal/devicepairing"
)

// DeviceStore only exposes guardian-facing capabilities. It must re-check
// the guardian's current device-management authority for the named child on
// every call. What a device does to be paired is served elsewhere.
type DeviceStore interface {
	ListDevices(guardian, tenant, child string) ([]devicepairing.Device, error)
	// Approve binds the device that shows this code to the child.
	Approve(guardian, tenant, child, code string) (string, error)
	Revoke(guardian, tenant, child, deviceID string) error
}

type deviceHandler struct {
	auth     Authenticator
	devices  DeviceStore
	families FamilyStore
	address  string // where a device goes to be paired, without scheme
	looks    AppearanceStore
}

// NewDevicePages serves "Geräte von …": a guardian enters the code a device
// shows and thereby binds that device to one of their children, and locks
// devices. Guardian and tenant always come from the session; the child is
// named by the request and authorized by the store. looks may be nil.
func NewDevicePages(auth Authenticator, devices DeviceStore, families FamilyStore, origin string, looks AppearanceStore) (http.Handler, error) {
	u, err := url.Parse(origin)
	if auth == nil || devices == nil || families == nil || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return nil, errors.New("device pages require verified sessions, storage, families and an HTTPS origin")
	}
	h := &deviceHandler{auth: auth, devices: devices, families: families, address: u.Host + "/pair", looks: looks}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /family/devices", h.list)
	mux.HandleFunc("POST /family/devices/connect", h.connect)
	mux.HandleFunc("POST /family/devices/revoke", h.revoke)
	return mux, nil
}

func (h *deviceHandler) session(w http.ResponseWriter, r *http.Request) (Session, bool) {
	p, err := h.auth.Authenticate(r)
	if err != nil || p.GuardianID == "" || p.TenantID == "" || p.CSRFToken == "" {
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return Session{}, false
	}
	return p, true
}

func validChildID(id string) bool {
	return id != "" && len(id) <= 64 && !strings.ContainsAny(id, "\r\n\x00<>&")
}

func (h *deviceHandler) posted(w http.ResponseWriter, r *http.Request, p Session, fields ...string) (url.Values, bool) {
	return postedForm(w, r, p, fields...)
}

func respondDeviceError(w http.ResponseWriter, err error) {
	if errors.Is(err, devicepairing.ErrForbidden) {
		http.Error(w, "Not authorized", http.StatusForbidden)
		return
	}
	if errors.Is(err, devicepairing.ErrInvalid) {
		http.Error(w, "Invalid device request", http.StatusBadRequest)
		return
	}
	http.Error(w, "Unable to manage device", http.StatusInternalServerError)
}

// deviceNotices are the only texts a redirect can select.
var deviceNotices = map[string]string{
	"connected": "Der Code stimmt. Das Gerät verbindet sich jetzt von selbst und erscheint gleich in der Liste unten.",
	"locked":    "Das Gerät ist gesperrt.",
}

var devicePage = template.Must(template.New("devices").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Yapuno – Geräte</title>{{if .Reload}}<meta http-equiv="refresh" content="4;url=/family/devices?child_id={{.Child}}">{{end}}<style>{{.CSS}}</style></head><body><main class="yp-page" data-yp-skin="{{.Skin}}"><div class="yp-shell">
<a class="yp-back" href="/family">← Zurück zur Familie</a>
<h1>Geräte von {{.Name}}</h1>
<p class="yp-muted">Verbinde das Handy oder Tablet von {{.Name}}. Es braucht kein Passwort.</p>
{{if .Alert}}<p class="yp-alert" role="alert">{{.Alert}}</p>{{end}}
{{if .Notice}}<p class="yp-status" role="status">{{.Notice}}</p>{{end}}
<section aria-labelledby="yp-new"><h2 id="yp-new">Neues Gerät verbinden</h2>
<ol class="yp-steps">
<li>Öffne auf dem Gerät von {{.Name}} im Browser: <strong class="yp-address">{{.Address}}</strong></li>
<li>Das Gerät zeigt einen Code mit sechs Ziffern.</li>
<li>Tippe den Code hier ein.</li>
</ol>
<form method="post" action="/family/devices/connect"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="child_id" value="{{.Child}}"><label class="yp-field" for="yp-code">Code vom Gerät</label><input type="text" id="yp-code" class="yp-digits" name="code" required inputmode="numeric" autocomplete="off" autocorrect="off" spellcheck="false" maxlength="9" placeholder="123 456"><button class="yp-primary" type="submit">Gerät verbinden</button></form>
<p class="yp-muted">Tippe nur einen Code ein, den du selbst auf dem Gerät von {{.Name}} abliest. Nennt dir jemand einen Code am Telefon oder in einer Nachricht, will diese Person ihr eigenes Gerät als {{.Name}} anmelden.</p></section>
<section aria-labelledby="yp-devices"><h2 id="yp-devices">Verbundene Geräte</h2>
{{if .Devices}}<ul>{{range .Devices}}<li class="yp-item"><strong>{{.Name}}</strong>{{if .Revoked}} <span class="yp-muted">(gesperrt)</span>{{else}}
<form method="post" action="/family/devices/revoke"><input type="hidden" name="csrf_token" value="{{$.CSRF}}"><input type="hidden" name="child_id" value="{{$.Child}}"><input type="hidden" name="device_id" value="{{.ID}}"><label class="yp-check"><input type="checkbox" name="confirm" value="yes" required> <span>«{{.Name}}» sperren. Dieses Gerät kann sich danach nicht mehr anmelden.</span></label><button class="yp-danger" type="submit">Gerät sperren</button></form>{{end}}</li>{{end}}</ul>
{{else}}<p class="yp-muted">Noch kein Gerät verbunden.</p>{{end}}
<p><a class="yp-back" href="/family/devices/lost?child_id={{.Child}}">Gerät verloren?</a></p></section>
<p class="yp-muted">Vorschau mit synthetischen Profilen.</p>
</div></main></body></html>`))

const deviceCSS = `.yp-page ol.yp-steps li{margin:.5rem 0}
.yp-address{color:var(--yp-text);overflow-wrap:anywhere}
.yp-page input.yp-digits{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:1.5rem;font-weight:700;letter-spacing:.12em;text-align:center}
`

func (h *deviceHandler) childName(p Session, child string) (string, bool) {
	for _, c := range h.families.Cards(p.GuardianID, p.TenantID) {
		if c.ID == child && c.Permissions.ManageDevices {
			return c.DisplayName, true
		}
	}
	return "", false
}

func (h *deviceHandler) render(w http.ResponseWriter, p Session, child string, status int, alert, notice string) {
	name, ok := h.childName(p, child)
	if !ok {
		respondDeviceError(w, devicepairing.ErrForbidden)
		return
	}
	devices, err := h.devices.ListDevices(p.GuardianID, p.TenantID, child)
	if err != nil {
		respondDeviceError(w, err)
		return
	}
	skinID, css, err := skinnedCSS(h.looks, p, deviceCSS)
	if err != nil {
		http.Error(w, "Unable to display devices", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = devicePage.Execute(w, struct {
		Skin, Name, Child, CSRF, Address, Alert, Notice string
		CSS                                             template.CSS
		Devices                                         []devicepairing.Device
		// Reload shows the new device once it has finished on its own.
		Reload bool
	}{Reload: notice == deviceNotices["connected"], Skin: skinID, Name: name, Child: child, CSRF: p.CSRFToken, Address: h.address, Alert: alert, Notice: notice, CSS: css, Devices: devices})
}

func (h *deviceHandler) list(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := h.session(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	if len(query["child_id"]) != 1 || !validChildID(query.Get("child_id")) {
		http.Error(w, "Invalid child", http.StatusBadRequest)
		return
	}
	// Only fixed, known notices are shown; the query text is never rendered.
	h.render(w, p, query.Get("child_id"), http.StatusOK, "", deviceNotices[query.Get("done")])
}

// connect binds the device that shows the entered code to the child. The
// guardian decides which device that is by reading the code off it.
func (h *deviceHandler) connect(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := h.session(w, r)
	if !ok {
		return
	}
	form, ok := h.posted(w, r, p, "child_id", "code")
	if !ok {
		return
	}
	child := form.Get("child_id")
	_, err := h.devices.Approve(p.GuardianID, p.TenantID, child, form.Get("code"))
	switch {
	case err == nil:
		http.Redirect(w, r, "/family/devices?child_id="+url.QueryEscape(child)+"&done=connected", http.StatusSeeOther)
	case errors.Is(err, devicepairing.ErrInvalid):
		h.render(w, p, child, http.StatusBadRequest, "Das ist kein gültiger Code. Er hat sechs Ziffern; vielleicht hast du dich vertippt.", "")
	case errors.Is(err, devicepairing.ErrNotFound):
		h.render(w, p, child, http.StatusNotFound, "Kein Gerät wartet mit diesem Code. Codes gelten fünf Minuten; lass dir auf dem Gerät einen neuen anzeigen.", "")
	case errors.Is(err, devicepairing.ErrRateLimited):
		h.render(w, p, child, http.StatusTooManyRequests, "Das waren zu viele Codes, die es nicht gibt. Warte zehn Minuten und lies den Code dann direkt vom Gerät ab.", "")
	default:
		respondDeviceError(w, err)
	}
}

func (h *deviceHandler) revoke(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := h.session(w, r)
	if !ok {
		return
	}
	form, ok := h.posted(w, r, p, "child_id", "device_id", "confirm")
	if !ok {
		return
	}
	if form.Get("confirm") != "yes" || form.Get("device_id") == "" || len(form.Get("device_id")) > 128 {
		http.Error(w, "Explicit confirmation required", http.StatusBadRequest)
		return
	}
	if err := h.devices.Revoke(p.GuardianID, p.TenantID, form.Get("child_id"), form.Get("device_id")); err != nil {
		respondDeviceError(w, err)
		return
	}
	http.Redirect(w, r, "/family/devices?child_id="+url.QueryEscape(form.Get("child_id"))+"&done=locked", http.StatusSeeOther)
}
