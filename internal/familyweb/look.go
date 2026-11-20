package familyweb

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"mime"
	"net/http"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/lookpage"
)

// AppearanceStore persists per-subject looks. Implementations must validate
// preferences; the handler additionally derives the subject from the session.
type AppearanceStore interface {
	Get(appearance.Subject) appearance.Preference
	Set(appearance.Subject, appearance.Preference) error
}

// NewLookPages serves the "Mein Look" page for the authenticated guardian.
// The subject is always taken from the session, never from the form.
func NewLookPages(auth Authenticator, looks AppearanceStore) (http.Handler, error) {
	if auth == nil || looks == nil {
		return nil, errors.New("authenticator and appearance store are required")
	}
	h := &lookHandler{auth: auth, looks: looks}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /family/look", h.view)
	mux.HandleFunc("POST /family/look", h.save)
	return mux, nil
}

type lookHandler struct {
	auth  Authenticator
	looks AppearanceStore
}

func guardianSubject(p Session) appearance.Subject {
	return appearance.Subject{Kind: appearance.KindGuardian, TenantID: p.TenantID, ID: p.GuardianID}
}

func (h *lookHandler) session(w http.ResponseWriter, r *http.Request) (Session, bool) {
	p, err := h.auth.Authenticate(r)
	if err != nil || p.GuardianID == "" || p.TenantID == "" || p.CSRFToken == "" {
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return Session{}, false
	}
	return p, true
}

func (h *lookHandler) view(w http.ResponseWriter, r *http.Request) {
	headers(w)
	p, ok := h.session(w, r)
	if !ok {
		return
	}
	var page bytes.Buffer
	err := lookpage.Render(&page, lookpage.Page{
		Action: "/family/look", BackHref: "/family", BackLabel: "← Zurück zur Familie",
		CSRF: p.CSRFToken, Saved: r.URL.Query().Get("saved") == "1", Look: h.looks.Get(guardianSubject(p)),
	})
	if err != nil {
		http.Error(w, "Unable to display look", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page.Bytes())
}

func (h *lookHandler) save(w http.ResponseWriter, r *http.Request) {
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
	pref, ok := lookpage.Parse(r.PostForm)
	if !ok || len(r.PostForm["csrf_token"]) != 1 {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	token := r.PostForm.Get("csrf_token")
	if len(token) != len(p.CSRFToken) || subtle.ConstantTimeCompare([]byte(token), []byte(p.CSRFToken)) != 1 {
		http.Error(w, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	if err := h.looks.Set(guardianSubject(p), pref); err != nil {
		if errors.Is(err, appearance.ErrInvalid) {
			http.Error(w, "Invalid look", http.StatusBadRequest)
			return
		}
		http.Error(w, "Unable to save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/family/look?saved=1", http.StatusSeeOther)
}
