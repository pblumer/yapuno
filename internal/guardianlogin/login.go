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

	"github.com/pblumer/yapuno/internal/publicpage"
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

// RememberedIssuer issues a session that lasts for days on the device that
// confirms the link. It must make the session durable before it sets a cookie.
type RememberedIssuer interface {
	IssueRemembered(w http.ResponseWriter, guardianID, tenantID string) error
}

type Config struct {
	Origin    string
	Directory Directory
	Sender    Sender
	Sessions  SessionIssuer
	Now       func() time.Time
	// Remembered is optional. With it the last step offers to stay signed in
	// on this device for RememberDays; SessionMinutes is how long an
	// ordinary session lasts, so the page can say what the alternative is.
	Remembered     RememberedIssuer
	RememberDays   int
	SessionMinutes int
	// PairingPage says that /pair exists, so the sign-in page can lead a
	// child's device there.
	PairingPage bool
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
	css     template.CSS
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
	if (cfg.Remembered == nil) != (cfg.RememberDays == 0) || cfg.RememberDays < 0 || cfg.RememberDays > 31 || (cfg.Remembered != nil && cfg.SessionMinutes < 1) {
		return nil, errors.New("invalid policy for remembered guardian sessions")
	}
	css, err := publicpage.Styles()
	if err != nil {
		return nil, err
	}
	return &Handler{cfg: cfg, css: css, pending: make(map[[32]byte]challenge), recent: make(map[string]time.Time)}, nil
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", h.showForm)
	mux.HandleFunc("POST /login/request", h.requestLink)
	mux.HandleFunc("GET /login/verify", h.showConfirm)
	mux.HandleFunc("POST /login/verify", h.consumeLink)
	return mux
}

func secureHeaders(w http.ResponseWriter) { publicpage.Headers(w) }

// loginNotices are the only texts the login page can be asked to show.
var loginNotices = map[string]string{
	"signed-out":            "Du bist abgemeldet.",
	"signed-out-everywhere": "Du bist überall abgemeldet. Noch nicht benutzte Anmeldelinks sind ungültig.",
	// Shown after a guardian deleted their own family.
	"family-deleted":            "Eure Familie ist gelöscht. Du bist abgemeldet.",
	"family-deleted-incomplete": "Eure Familie ist gelöscht und du bist abgemeldet. Einzelne Angaben konnten nicht entfernt werden; wende dich an den Betreiber dieser Yapuno-Installation.",
}

var loginForm = template.Must(template.New("login").Parse(publicpage.Open + `
<h1>Bei Yapuno anmelden</h1>
{{if .Notice}}<p class="yp-status" role="status">{{.Notice}}</p>{{end}}
<p class="yp-muted">Für Eltern, die bei Yapuno schon registriert sind. Du erhältst einen Anmeldelink per E-Mail; ein Passwort gibt es nicht.</p>
<section aria-labelledby="yp-login"><h2 id="yp-login">Anmeldelink anfordern</h2>
<form method="post" action="/login/request"><label class="yp-field">E-Mail-Adresse<input type="email" name="email" autocomplete="email" required maxlength="254"></label><button type="submit">Anmeldelink senden</button></form></section>
{{if .Pairing}}<section aria-labelledby="yp-child"><h2 id="yp-child">Handy oder Tablet eines Kindes?</h2>
<p class="yp-muted">Kinder melden sich nicht mit einer E-Mail-Adresse an. Ihr Gerät wird einmal mit einem Code verbunden.</p>
<p><a class="yp-link" href="/pair">Gerät eines Kindes verbinden</a></p></section>{{end}}
` + publicpage.Close))

var messagePage = template.Must(template.New("message").Parse(publicpage.Open + `
<h1>{{.Title}}</h1>
<p class="yp-status" role="{{.Role}}">{{.Text}}</p>
{{if .Detail}}<p class="yp-muted">{{.Detail}}</p>{{end}}
<p><a class="yp-link" href="/login">Zur Anmeldung</a></p>
` + publicpage.Close))

// message answers with a fixed German text. role is "status" or "alert".
func (h *Handler) message(w http.ResponseWriter, status int, role, title, text, detail string) {
	w.WriteHeader(status)
	_ = messagePage.Execute(w, struct {
		Title, Role, Text, Detail string
		CSS                       template.CSS
	}{title, role, text, detail, h.css})
}

func (h *Handler) invalidLink(w http.ResponseWriter) {
	h.message(w, http.StatusForbidden, "alert", "Link ungültig", "Dieser Link ist ungültig oder abgelaufen.",
		"Anmeldelinks gelten 10 Minuten und nur einmal. Fordere einen neuen an.")
}

func (h *Handler) showForm(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	notice := ""
	// Only a single, known key selects a text; the query is never rendered.
	if values := r.URL.Query()["notice"]; len(values) == 1 {
		notice = loginNotices[values[0]]
	}
	_ = loginForm.Execute(w, struct {
		Title, Notice string
		Pairing       bool
		CSS           template.CSS
	}{"Anmeldung", notice, h.cfg.PairingPage, h.css})
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
		h.message(w, http.StatusBadRequest, "alert", "Adresse prüfen", "Das ist keine gültige E-Mail-Adresse.", "Gib die Adresse ohne Namen und ohne Leerzeichen ein.")
		return
	}
	// Same generic response for provisioned and unknown email accounts.
	defer h.message(w, http.StatusOK, "status", "Schau in dein Postfach", "Falls diese Adresse zugelassen ist, erhältst du einen Anmeldelink.",
		"Der Link gilt 10 Minuten und nur einmal. Keine E-Mail erhalten? Prüfe den Spam-Ordner oder fordere in einer Minute einen neuen Link an.")
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

var confirm = template.Must(template.New("confirm").Parse(publicpage.Open + `
<h1>Anmeldung bestätigen</h1>
<p class="yp-muted">Mit dem nächsten Schritt bist du als Elternteil angemeldet.</p>
<section aria-labelledby="yp-finish"><h2 id="yp-finish">Letzter Schritt</h2>
<form method="post" action="/login/verify"><input type="hidden" name="token" value="{{.Token}}">{{if .Days}}<label class="yp-check"><input type="checkbox" name="remember" value="yes"> <span>Auf diesem Gerät {{.Days}} Tage angemeldet bleiben</span></label>
<p class="yp-muted">Setze den Haken nur auf einem Gerät, das nur du benutzt. Ohne Haken wirst du nach {{.Minutes}} Minuten abgemeldet. Unter «Sicherheit und Abmelden» meldest du dich jederzeit überall ab.</p>{{end}}<button type="submit">Jetzt anmelden</button></form></section>
` + publicpage.Close))

func (h *Handler) showConfirm(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	if len(r.URL.Query()["token"]) != 1 {
		http.Error(w, "Invalid link", http.StatusBadRequest)
		return
	}
	_ = confirm.Execute(w, struct {
		Title, Token  string
		Days, Minutes int
		CSS           template.CSS
	}{"Anmeldung bestätigen", r.URL.Query().Get("token"), h.cfg.RememberDays, h.cfg.SessionMinutes, h.css})
}

func (h *Handler) consumeLink(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	// The choice is one checkbox; two answers are no answer. This is checked
	// before the link is spent.
	if err := r.ParseForm(); err != nil || len(r.PostForm["token"]) != 1 || len(r.PostForm["remember"]) > 1 {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	remember := r.PostForm.Get("remember") == "yes" && h.cfg.Remembered != nil
	token := r.PostForm.Get("token")
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 || len(token) != 43 {
		h.invalidLink(w)
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
		h.invalidLink(w)
		return
	}
	// Re-check active identity after issuance; revocation must win.
	principal, err := h.cfg.Directory.LookupGuardian(r.Context(), ch.email)
	if err != nil || !principal.Active || principal.GuardianID != ch.guardianID || principal.TenantID != ch.tenantID || !strings.EqualFold(principal.Email, ch.email) {
		h.invalidLink(w)
		return
	}
	target := "/family"
	if remember {
		if err := h.cfg.Remembered.IssueRemembered(w, principal.GuardianID, principal.TenantID); err == nil {
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		// The link is spent. Sign the guardian in for the ordinary time and
		// say that staying signed in did not work.
		target = "/family?done=not-remembered"
	}
	if err := h.cfg.Sessions.IssueVerified(w, principal.GuardianID, principal.TenantID); err != nil {
		h.message(w, http.StatusServiceUnavailable, "alert", "Nicht möglich", "Die Anmeldung ist gerade nicht möglich.", "Fordere in einer Minute einen neuen Anmeldelink an.")
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// Cancel invalidates every unused sign-in link of one guardian and returns
// how many were pending. It belongs to "sign out everywhere": a link already
// sitting in a mailbox must not outlive that decision. It does not prevent a
// new link from being requested.
func (h *Handler) Cancel(guardianID, tenantID string) int {
	if guardianID == "" || tenantID == "" {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	cancelled := 0
	for digest, item := range h.pending {
		if item.guardianID == guardianID && item.tenantID == tenantID {
			delete(h.pending, digest)
			cancelled++
		}
	}
	return cancelled
}
