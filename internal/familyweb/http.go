// Package familyweb serves guardian-scoped family management behind an
// externally authenticated session. It never authenticates from client IDs.
package familyweb

import (
	"crypto/subtle"
	"errors"
	"mime"
	"net/http"
	"strconv"
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
	return NewWithOptions(auth, store, Options{DevicePages: len(enableDevicePages) != 0 && enableDevicePages[0]})
}

// NewWithAppearance additionally applies the guardian's saved look and links
// the profile figure to the "Mein Look" page served by NewLookPages.
func NewWithAppearance(auth Authenticator, store FamilyStore, looks AppearanceStore, enableDevicePages bool) (http.Handler, error) {
	if looks == nil {
		return nil, errors.New("appearance store is required")
	}
	return NewWithOptions(auth, store, Options{Looks: looks, DevicePages: enableDevicePages})
}

// Options selects which optional pages the family overview links to. A link
// is navigation only; each linked handler authorizes its own requests.
type Options struct {
	Looks        AppearanceStore // nil: classic skin, no "Mein Look" link
	DevicePages  bool
	ContactPages bool
	// SecurityPages links to sign-out and the emergency lock (NewSecurityPages).
	SecurityPages bool
	// Reports shows next to the contact link how many reports are open.
	Reports ReportCounter
	// Children lets guardians add child profiles to their own family. It is
	// part of self-registration and nil unless that is switched on.
	Children ChildCreator
	// InvitePages links to "invite a family" (NewInvitePages).
	InvitePages bool
	// Chat links to the guardian's chats (NewChatPages); nil means none.
	Chat GuardianChat
	// Inactive names, in German, the functions this installation does not
	// offer. The overview shows them so a guardian knows why a step of the
	// guide is missing; it is information for the operator, not a secret.
	Inactive []string
	// Version is shown on the overview when set.
	Version string
}

// NewWithOptions builds the fail-closed family overview handler.
func NewWithOptions(auth Authenticator, store FamilyStore, opt Options) (http.Handler, error) {
	if auth == nil || store == nil {
		return nil, errors.New("authenticator and store are required")
	}
	h := &handler{auth: auth, store: store, looks: opt.Looks, devicesEnabled: opt.DevicePages, contactsEnabled: opt.ContactPages, securityEnabled: opt.SecurityPages, reports: opt.Reports, children: opt.Children, inviteEnabled: opt.InvitePages, chat: opt.Chat, inactive: opt.Inactive, version: opt.Version}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /family", h.view)
	mux.HandleFunc("POST /family/rename", h.rename)
	if opt.Children != nil {
		mux.HandleFunc("GET /family/children", h.childForm)
		mux.HandleFunc("POST /family/children", h.addChild)
	}
	return mux, nil
}

type handler struct {
	auth  Authenticator
	store FamilyStore
	looks AppearanceStore

	devicesEnabled  bool
	contactsEnabled bool
	securityEnabled bool
	reports         ReportCounter
	children        ChildCreator
	inviteEnabled   bool
	chat            GuardianChat
	inactive        []string
	version         string
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
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; font-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
}

// openReports returns a short count per child for guardians who may see that
// child's reports. It never contains report content.
func (h *handler) openReports(p Session, cards []familydashboard.ChildCard) map[string]string {
	out := map[string]string{}
	if h.reports == nil {
		return out
	}
	for _, c := range cards {
		switch n := h.reports.Count(p.GuardianID, p.TenantID, c.ID); {
		case n == 1:
			out[c.ID] = "1 offene Meldung"
		case n > 1:
			out[c.ID] = strconv.Itoa(n) + " offene Meldungen"
		}
	}
	return out
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
