// Package guardianlogin implements invitation-only passwordless email sign-in.
// It requires trusted, pre-provisioned guardian identities; email possession
// alone is not evidence of guardianship.
package guardianlogin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Principal is resolved from authoritative, independently provisioned records.
// It must never be derived from an untrusted login form or email message.
type Principal struct {
	GuardianID string
	TenantID   string
	Email      string
	Active     bool
}

type Directory interface {
	LookupGuardian(ctx context.Context, normalizedEmail string) (Principal, error)
}

type Sender interface {
	SendSignInLink(ctx context.Context, email, link string) error
}

type SessionIssuer interface {
	IssueVerified(w http.ResponseWriter, guardianID, tenantID string) error
}

type Config struct {
	Origin    string
	Directory Directory
	Sender    Sender
	Sessions  SessionIssuer
	Now       func() time.Time
}

type challenge struct {
	email      string
	guardianID string
	tenantID   string
	expires    time.Time
}

// Handler stores pending links in memory and is intended for a single-process
// prototype. Use a shared atomic challenge store before scaling horizontally.
type Handler struct {
	cfg     Config
	mu      sync.Mutex
	pending map[[32]byte]challenge
	recent  map[string]time.Time
}

const (
	linkTTL       = 10 * time.Minute
	emailCooldown = 60 * time.Second
	maxPending    = 10000
)

func New(cfg Config) (*Handler, error) {
	u, err := url.Parse(cfg.Origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" || cfg.Directory == nil || cfg.Sender == nil || cfg.Sessions == nil || cfg.Now == nil {
		return nil, errors.New("invalid guardian login configuration")
	}
	return &Handler{cfg: cfg, pending: make(map[[32]byte]challenge), recent: make(map[string]time.Time)}, nil
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", h.showForm)
	mux.HandleFunc("POST /login/request", h.requestLink)
	mux.HandleFunc("GET /login/verify", h.showConfirm)
	mux.HandleFunc("POST /login/verify", h.consumeLink)
	return mux
}

func secureHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
}

func (h *Handler) showForm(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	_, _ = w.Write([]byte(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Yapuno – Anmeldung</title></head><body><main><h1>Bei Yapuno anmelden</h1><p>Für bereits bestätigte Erziehungsberechtigte. Du erhältst einen einmaligen Anmeldelink.</p><form method="post" action="/login/request"><label>E-Mail-Adresse<input type="email" name="email" autocomplete="email" required maxlength="254"></label><button type="submit">Anmeldelink senden</button></form></main></body></html>`))
}

func normalizeEmail(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if len(email) > 254 || email == "" || strings.ContainsAny(email, "\r\n\x00") {
		return "", false
	}
	parsed, err := mail.ParseAddress(email)
	return email, err == nil && parsed.Address == email && parsed.Name == "" && strings.Count(email, "@") == 1
}

func randomToken() (string, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(token[:]), nil
}

func (h *Handler) requestLink(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if err := r.ParseForm(); err != nil || len(r.PostForm["email"]) != 1 {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	email, ok := normalizeEmail(r.PostForm.Get("email"))
	if !ok {
		http.Error(w, "Invalid email", http.StatusBadRequest)
		return
	}
	// Same generic response for provisioned and unknown email accounts.
	defer func() { _, _ = w.Write([]byte("Falls diese Adresse zugelassen ist, erhältst du einen Anmeldelink.")) }()
	now := h.cfg.Now()
	h.mu.Lock()
	if last, exists := h.recent[email]; exists && now.Sub(last) < emailCooldown {
		h.mu.Unlock()
		return
	}
	if len(h.recent) >= maxPending {
		for address, last := range h.recent {
			if now.Sub(last) >= emailCooldown {
				delete(h.recent, address)
			}
		}
		if len(h.recent) >= maxPending {
			h.mu.Unlock()
			return
		}
	}
	if len(h.pending) >= maxPending {
		for digest, item := range h.pending {
			if !now.Before(item.expires) {
				delete(h.pending, digest)
			}
		}
		if len(h.pending) >= maxPending {
			h.mu.Unlock()
			return
		}
	}
	h.recent[email] = now
	h.mu.Unlock()
	principal, err := h.cfg.Directory.LookupGuardian(r.Context(), email)
	if err != nil || !principal.Active || principal.GuardianID == "" || principal.TenantID == "" || !strings.EqualFold(principal.Email, email) {
		return
	}
	token, err := randomToken()
	if err != nil {
		return
	}
	digest := sha256.Sum256([]byte(token))
	h.mu.Lock()
	h.pending[digest] = challenge{email: email, guardianID: principal.GuardianID, tenantID: principal.TenantID, expires: now.Add(linkTTL)}
	h.mu.Unlock()
	link := strings.TrimRight(h.cfg.Origin, "/") + "/login/verify?token=" + url.QueryEscape(token)
	if err := h.cfg.Sender.SendSignInLink(r.Context(), email, link); err != nil {
		h.mu.Lock()
		delete(h.pending, digest)
		h.mu.Unlock()
	}
}

var confirm = template.Must(template.New("confirm").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Yapuno – Anmeldung bestätigen</title></head><body><main><h1>Anmeldung bestätigen</h1><p>Mit dem nächsten Schritt meldest du dich als Erziehungsberechtigte oder Erziehungsberechtigter an.</p><form method="post" action="/login/verify"><input type="hidden" name="token" value="{{.}}"><button type="submit">Jetzt anmelden</button></form></main></body></html>`))

func (h *Handler) showConfirm(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	if len(r.URL.Query()["token"]) != 1 {
		http.Error(w, "Invalid link", http.StatusBadRequest)
		return
	}
	_ = confirm.Execute(w, r.URL.Query().Get("token"))
}

func (h *Handler) consumeLink(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if err := r.ParseForm(); err != nil || len(r.PostForm["token"]) != 1 {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	token := r.PostForm.Get("token")
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 || len(token) != 43 {
		http.Error(w, "Link invalid or expired", http.StatusForbidden)
		return
	}
	digest := sha256.Sum256([]byte(token))
	h.mu.Lock()
	ch, exists := h.pending[digest]
	// Consuming under the lock prevents replay even for concurrent requests.
	if exists {
		delete(h.pending, digest)
	}
	h.mu.Unlock()
	if !exists || !h.cfg.Now().Before(ch.expires) {
		http.Error(w, "Link invalid or expired", http.StatusForbidden)
		return
	}
	// Re-check active identity after issuance; revocation must win.
	principal, err := h.cfg.Directory.LookupGuardian(r.Context(), ch.email)
	if err != nil || !principal.Active || principal.GuardianID != ch.guardianID || principal.TenantID != ch.tenantID || !strings.EqualFold(principal.Email, ch.email) {
		http.Error(w, "Link invalid or expired", http.StatusForbidden)
		return
	}
	if err := h.cfg.Sessions.IssueVerified(w, principal.GuardianID, principal.TenantID); err != nil {
		http.Error(w, "Login unavailable", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, "/family", http.StatusSeeOther)
}
