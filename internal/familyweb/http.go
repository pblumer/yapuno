// Package familyweb serves guardian-scoped family management behind an
// externally authenticated session. It never authenticates from client IDs.
package familyweb

import (
	"crypto/subtle"
	"errors"
	"html/template"
	"mime"
	"net/http"
	"strings"

	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
)

// Session must come from a trusted, server-side authenticator; neither identity
// nor CSRFToken may be taken from a request header, query parameter or form.
// CSRFToken must be cryptographically random and bound to the current session.
type Session struct {
	GuardianID string
	TenantID   string
	CSRFToken  string
}

type Authenticator interface {
	Authenticate(*http.Request) (Session, error)
}

type FamilyStore interface {
	Cards(guardianID, tenantID string) []familydashboard.ChildCard
	RenameChild(guardianID, tenantID, childID, name string) error
}

// New builds a fail-closed handler. It is not a complete application: a real
// authenticator, secure cookies, HTTPS and account provisioning are required.
func New(auth Authenticator, store FamilyStore, enableDevicePages ...bool) (http.Handler, error) {
	if auth == nil || store == nil {
		return nil, errors.New("authenticator and store are required")
	}
	h := &handler{auth: auth, store: store}
	if len(enableDevicePages) != 0 {
		h.devicesEnabled = enableDevicePages[0]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /family", h.view)
	mux.HandleFunc("POST /family/rename", h.rename)
	return mux, nil
}

type handler struct {
	auth  Authenticator
	store FamilyStore

	devicesEnabled bool
}

func (h *handler) session(w http.ResponseWriter, r *http.Request) (Session, bool) {
	p, err := h.auth.Authenticate(r)
	if err != nil || p.GuardianID == "" || p.TenantID == "" || p.CSRFToken == "" {
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return Session{}, false
	}
	return p, true
}

func headers(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
}

func (h *handler) view(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := h.session(w, r)
	if !ok {
		return
	}
	cards := h.store.Cards(p.GuardianID, p.TenantID)
	skinID := r.URL.Query().Get("skin")
	fragment, err := familydashboard.RenderHTML(cards, skinID)
	if err != nil {
		http.Error(w, "Unable to display family", http.StatusInternalServerError)
		return
	}
	const page = `<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Yapuno – Familie</title></head><body>{{.Fragment}}<section aria-label="Kinderprofile bearbeiten"><h2>Profile bearbeiten</h2>{{range .Cards}}{{if and .Permissions.ManageDevices .Permissions.ApproveContacts}}<form method="post" action="/family/rename"><label>Neuer Name für {{.DisplayName}}<input name="name" value="{{.DisplayName}}" required maxlength="40"></label><input type="hidden" name="child_id" value="{{.ID}}"><input type="hidden" name="csrf_token" value="{{$.Token}}"><button type="submit">Speichern</button></form>{{end}}{{if and $.DevicesEnabled .Permissions.ManageDevices}}<p><a href="/family/devices?child_id={{.ID}}">Geräte verwalten</a></p>{{end}}{{end}}</section></body></html>`
	t, err := template.New("page").Parse(page)
	if err != nil {
		http.Error(w, "Unable to display family", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, struct {
		Token    string
		Cards    []familydashboard.ChildCard
		Fragment template.HTML

		DevicesEnabled bool
	}{Token: p.CSRFToken, Cards: cards, Fragment: template.HTML(fragment), DevicesEnabled: h.devicesEnabled}); err != nil {
		return
	}
}

func (h *handler) rename(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := h.session(w, r)
	if !ok {
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		http.Error(w, "Expected form data", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	for _, field := range []string{"csrf_token", "child_id", "name"} {
		if len(r.PostForm[field]) != 1 {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}
	}
	token := r.PostForm.Get("csrf_token")
	if len(token) != len(p.CSRFToken) || subtle.ConstantTimeCompare([]byte(token), []byte(p.CSRFToken)) != 1 {
		http.Error(w, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	childID := strings.TrimSpace(r.PostForm.Get("child_id"))
	name := strings.TrimSpace(r.PostForm.Get("name"))
	if err := h.store.RenameChild(p.GuardianID, p.TenantID, childID, name); err != nil {
		switch {
		case errors.Is(err, familystore.ErrForbidden):
			http.Error(w, "Not authorized", http.StatusForbidden)
		case errors.Is(err, familystore.ErrInvalid):
			http.Error(w, "Invalid profile", http.StatusBadRequest)
		default:
			http.Error(w, "Unable to save", http.StatusInternalServerError)
		}
		return
	}
	http.Redirect(w, r, "/family", http.StatusSeeOther)
}
