package childweb

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"mime"
	"net/http"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/childsession"
	"github.com/pblumer/yapuno/internal/lookpage"
)

// ownLook is the only subject a child session may read or store.
func ownLook(s childsession.Session) appearance.Subject {
	return appearance.Subject{Kind: appearance.KindChild, TenantID: s.TenantID, ID: s.ChildID}
}

func (h *handler) lookView(w http.ResponseWriter, r *http.Request) {
	headers(w, false)
	s, err := h.sessions.Authenticate(r)
	if err != nil {
		http.Redirect(w, r, "/child", http.StatusSeeOther)
		return
	}
	var page bytes.Buffer
	err = lookpage.Render(&page, lookpage.Page{
		Action: "/child/look", BackHref: "/child", BackLabel: "← Zurück",
		CSRF: s.CSRFToken, Saved: r.URL.Query().Get("saved") == "1", Look: h.opt.Looks.Get(ownLook(s)),
	})
	if err != nil {
		http.Error(w, "Unable to display look", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page.Bytes())
}

func (h *handler) lookSave(w http.ResponseWriter, r *http.Request) {
	headers(w, false)
	s, err := h.sessions.Authenticate(r)
	if err != nil {
		http.Error(w, "Authentication required", http.StatusUnauthorized)
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
	look, ok := lookpage.Parse(r.PostForm)
	if !ok || len(r.PostForm["csrf_token"]) != 1 {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	token := r.PostForm.Get("csrf_token")
	if len(token) != len(s.CSRFToken) || subtle.ConstantTimeCompare([]byte(token), []byte(s.CSRFToken)) != 1 {
		http.Error(w, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	if err := h.opt.Looks.Set(ownLook(s), look); err != nil {
		if errors.Is(err, appearance.ErrInvalid) {
			http.Error(w, "Invalid look", http.StatusBadRequest)
			return
		}
		http.Error(w, "Unable to save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/child/look?saved=1", http.StatusSeeOther)
}
