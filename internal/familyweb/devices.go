package familyweb

import (
	"crypto/subtle"
	"errors"
	"html/template"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/pblumer/yapuno/internal/devicepairing"
)

// DeviceStore only exposes guardian-facing capabilities. Child-side claiming
// and proof-of-possession must be handled by separate, explicitly bounded APIs.
type DeviceStore interface {
	Start(guardian, tenant, child string) (string, error)
	ListDevices(guardian, tenant, child string) ([]devicepairing.Device, error)
	Status(guardian, tenant, child, token string) (devicepairing.Pending, error)
	Approve(guardian, tenant, child, token, expectedFingerprint string) error
	Revoke(guardian, tenant, child, deviceID string) error
}

type deviceHandler struct {
	auth    Authenticator
	devices DeviceStore
	origin  string
}

// NewDevicePages mounts guardian-only device pages. A verified authenticator
// must derive guardian/tenant server-side on every request, including POSTs.
func NewDevicePages(auth Authenticator, devices DeviceStore, origin ...string) (http.Handler, error) {
	if auth == nil || devices == nil || len(origin) > 1 {
		return nil, errors.New("device pages require verified sessions and storage")
	}
	h := &deviceHandler{auth: auth, devices: devices}
	if len(origin) == 1 && origin[0] != "" {
		u, err := url.Parse(origin[0])
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, errors.New("device invitation requires a canonical HTTPS origin")
		}
		h.origin = strings.TrimRight(origin[0], "/")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /family/devices", h.list)
	mux.HandleFunc("POST /family/devices/start", h.start)
	mux.HandleFunc("POST /family/devices/status", h.status)
	mux.HandleFunc("POST /family/devices/approve", h.approve)
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
	if !validChildID(r.PostForm.Get("child_id")) {
		http.Error(w, "Invalid child", http.StatusBadRequest)
		return nil, false
	}
	return r.PostForm, true
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
	if errors.Is(err, devicepairing.ErrExpired) {
		http.Error(w, "Pairing expired", http.StatusGone)
		return
	}
	if errors.Is(err, devicepairing.ErrConsumed) || errors.Is(err, devicepairing.ErrNotReady) {
		http.Error(w, "Pairing not available", http.StatusConflict)
		return
	}
	http.Error(w, "Unable to manage device", http.StatusInternalServerError)
}

var devicePage = template.Must(template.New("devices").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Yapuno – Geräte</title><style>
body{margin:0;font:16px system-ui,sans-serif;color:#213148;background:#f7f8fd}main{max-width:700px;margin:0 auto;padding:1.2rem}a{color:#3159c9}section{border:1px solid #d8dfea;border-radius:18px;background:white;padding:1rem;margin:1rem 0}button{min-height:48px;border:0;border-radius:12px;background:#314cbe;color:white;padding:.75rem 1.2rem;font-weight:700;cursor:pointer}label{display:inline-flex;gap:.5rem;align-items:center}li{padding:1rem 0;border-bottom:1px solid #e2e6f2;overflow-wrap:anywhere}li:last-child{border:0}.muted{color:#5b6577}
</style></head><body><main><p><a href="/family">← Familie</a></p><h1>Geräte verwalten</h1><p class="muted">Geräte für das ausgewählte Kinderprofil. Freigaben sind an das Kind gebunden.</p><section><h2>Verknüpfte Geräte</h2>{{if .Devices}}<ul>{{range .Devices}}<li><strong>{{.Name}}</strong> {{if .Revoked}}<span>(gesperrt)</span>{{else}}<span>(aktiv)</span><form method="post" action="/family/devices/revoke"><input type="hidden" name="csrf_token" value="{{$.CSRF}}"><input type="hidden" name="child_id" value="{{$.Child}}"><input type="hidden" name="device_id" value="{{.ID}}"><label><input type="checkbox" required name="confirm" value="yes"> Dieses Gerät sperren</label> <button type="submit">Sperren</button></form>{{end}}</li>{{end}}</ul>{{else}}<p>Noch keine Geräte verbunden.</p>{{end}}</section><section><h2>Weiteres Gerät</h2><form method="post" action="/family/devices/start"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="child_id" value="{{.Child}}"><button type="submit">Pairing-Code erzeugen</button></form><p class="muted">Technische Vorschau: Das Scannen und Bestätigen auf dem Kindergerät folgt als nächster Schritt.</p></section></main></body></html>`))

var invitationPage = template.Must(template.New("invitation").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Yapuno – Geräte-Einladung</title><meta name="referrer" content="no-referrer"></head><body><main><h1>Kindergerät verbinden</h1><p>Einmal-Code für fünf Minuten. Gib den Link nur auf dem Gerät deines Kindes frei. Der Code allein erlaubt keine Anmeldung.</p><p><a href="{{.Link}}">Kopplungslink auf dem Kindergerät öffnen</a></p><p><small>Alternativ diesen Link direkt auf das Kindergerät übertragen:</small></p><p><code style="overflow-wrap:anywhere">{{.Link}}</code></p><p><output style="overflow-wrap:anywhere;font-family:monospace">{{.Token}}</output></p><h2>Elternfreigabe</h2><p>Nachdem das Kind den Link geöffnet hat, die Geräteanfrage prüfen. Den Fingerabdruck auf beiden Bildschirmen vergleichen.</p><form method="post" action="/family/devices/status"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="child_id" value="{{.Child}}"><input type="hidden" name="token" value="{{.Token}}"><button type="submit">Geräteanfrage prüfen</button></form><p><a href="/family/devices?child_id={{.Child}}">Zurück zur Geräteübersicht</a></p></main></body></html>`))

var pendingPage = template.Must(template.New("pending").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Yapuno – Gerätefreigabe</title><meta name="referrer" content="no-referrer"></head><body><main><h1>Geräteanfrage freigeben</h1>{{if .Pending.Fingerprint}}<p>Gerätename: <strong>{{.Pending.Name}}</strong></p><p>Fingerabdruck des beantragenden Geräts:</p><p><code style="overflow-wrap:anywhere;letter-spacing:.06em">{{.Pending.Fingerprint}}</code></p>{{if .Pending.Approved}}<p>Das Gerät wurde freigegeben. Das Kindergerät kann die Verbindung abschliessen.</p>{{else}}<p>Vergleiche den gesamten Fingerabdruck mit dem Kindergerät. Falls sie sich unterscheiden, nicht freigeben!</p><form method="post" action="/family/devices/approve"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="child_id" value="{{.Child}}"><input type="hidden" name="token" value="{{.Token}}"><input type="hidden" name="fingerprint" value="{{.Pending.Fingerprint}}"><label><input type="checkbox" name="confirm" value="yes" required> Ich habe den Fingerabdruck auf beiden Geräten verglichen.</label><button type="submit">Dieses Gerät freigeben</button></form>{{end}}{{else}}<p>Das Kindergerät hat noch keine Kopplungsanfrage gesendet.</p>{{end}}<form method="post" action="/family/devices/status"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="child_id" value="{{.Child}}"><input type="hidden" name="token" value="{{.Token}}"><button type="submit">Status erneut prüfen</button></form><p><a href="/family/devices?child_id={{.Child}}">Zur Geräteübersicht</a></p></main></body></html>`))

func (h *deviceHandler) list(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := h.session(w, r)
	if !ok {
		return
	}
	if len(r.URL.Query()["child_id"]) != 1 || !validChildID(r.URL.Query().Get("child_id")) {
		http.Error(w, "Invalid child", http.StatusBadRequest)
		return
	}
	child := r.URL.Query().Get("child_id")
	devices, err := h.devices.ListDevices(p.GuardianID, p.TenantID, child)
	if err != nil {
		respondDeviceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = devicePage.Execute(w, struct {
		Child   string
		CSRF    string
		Devices []devicepairing.Device
	}{Child: child, CSRF: p.CSRFToken, Devices: devices})
}

func (h *deviceHandler) start(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := h.session(w, r)
	if !ok {
		return
	}
	form, ok := h.posted(w, r, p, "child_id")
	if !ok {
		return
	}
	token, err := h.devices.Start(p.GuardianID, p.TenantID, form.Get("child_id"))
	if err != nil {
		respondDeviceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	link := h.origin + "/pair#" + token
	_ = invitationPage.Execute(w, struct {
		Token string
		Link  string
		Child string
		CSRF  string
	}{Token: token, Link: link, Child: form.Get("child_id"), CSRF: p.CSRFToken})
}

func (h *deviceHandler) status(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := h.session(w, r)
	if !ok {
		return
	}
	form, ok := h.posted(w, r, p, "child_id", "token")
	if !ok {
		return
	}
	pending, err := h.devices.Status(p.GuardianID, p.TenantID, form.Get("child_id"), form.Get("token"))
	if err != nil {
		respondDeviceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pendingPage.Execute(w, struct {
		Child   string
		CSRF    string
		Token   string
		Pending devicepairing.Pending
	}{Child: form.Get("child_id"), CSRF: p.CSRFToken, Token: form.Get("token"), Pending: pending})
}

func (h *deviceHandler) approve(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := h.session(w, r)
	if !ok {
		return
	}
	form, ok := h.posted(w, r, p, "child_id", "token", "fingerprint", "confirm")
	if !ok {
		return
	}
	fingerprint := form.Get("fingerprint")
	if form.Get("confirm") != "yes" || len(fingerprint) != 64 {
		http.Error(w, "Explicit fingerprint confirmation required", http.StatusBadRequest)
		return
	}
	if err := h.devices.Approve(p.GuardianID, p.TenantID, form.Get("child_id"), form.Get("token"), fingerprint); err != nil {
		respondDeviceError(w, err)
		return
	}
	pending, err := h.devices.Status(p.GuardianID, p.TenantID, form.Get("child_id"), form.Get("token"))
	if err != nil {
		respondDeviceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pendingPage.Execute(w, struct {
		Child   string
		CSRF    string
		Token   string
		Pending devicepairing.Pending
	}{Child: form.Get("child_id"), CSRF: p.CSRFToken, Token: form.Get("token"), Pending: pending})
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
	http.Redirect(w, r, "/family/devices?child_id="+url.QueryEscape(form.Get("child_id")), http.StatusSeeOther)
}
