package signup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"html/template"
	"log"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/fontassets"
	"github.com/pblumer/yapuno/internal/skin"
)

// Directory is the guardian identity directory. Has must answer true when it
// cannot tell, so nothing is registered on top of an unreadable directory.
type Directory interface {
	Has(email string) bool
	Register(email, guardianID, tenantID string) error
	ActiveGuardian(ctx context.Context, guardianID, tenantID string) bool
}

// Families creates the new tenant and can undo exactly that.
type Families interface {
	CreateFamily(tenantID, guardianID, childID, name string) error
	DiscardFamily(tenantID, guardianID, childID string) error
}

// Mailer sends the link that proves control of the address.
type Mailer interface {
	SendRegistrationLink(ctx context.Context, email, link string) error
}

// Sessions signs the new guardian in after registration.
type Sessions interface {
	IssueVerified(w http.ResponseWriter, guardianID, tenantID string) error
}

type Config struct {
	Origin    string
	Store     *Store
	Directory Directory
	Families  Families
	Mailer    Mailer
	Sessions  Sessions
	Now       func() time.Time
}

// registration is what a confirmation link stands for. It lives in memory
// only: a restart discards unconfirmed registrations, never a family.
type registration struct {
	email      string
	child      string
	invitation string
	expires    time.Time
}

// Handler serves the public registration pages. Pending links are kept in
// this process, like sign-in links.
type Handler struct {
	cfg Config
	css template.CSS

	mu      sync.Mutex
	pending map[[32]byte]registration
	recent  map[string]time.Time

	// complete serializes the final step, so two confirmations can neither
	// use one invitation twice nor register one address twice.
	complete sync.Mutex
}

const (
	linkTTL       = 10 * time.Minute
	emailCooldown = 60 * time.Second
	maxPending    = 1000
)

func New(cfg Config) (*Handler, error) {
	u, err := url.Parse(cfg.Origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" ||
		cfg.Store == nil || cfg.Directory == nil || cfg.Families == nil || cfg.Mailer == nil || cfg.Sessions == nil || cfg.Now == nil {
		return nil, errors.New("invalid registration configuration")
	}
	// Nobody is signed in here, so these pages always use the classic skin.
	def := skin.Catalog()["classic"]
	decls, err := skin.Declarations(def)
	if err != nil {
		return nil, err
	}
	fonts := fontassets.FaceCSS(def.Typography.Heading, def.Typography.Body)
	return &Handler{cfg: cfg, css: template.CSS(fonts + ".yp-page{" + decls + "}\n" + pageCSS),
		pending: make(map[[32]byte]registration), recent: make(map[string]time.Time)}, nil
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /join", h.showForm)
	mux.HandleFunc("POST /join/request", h.requestLink)
	mux.HandleFunc("GET /join/verify", h.showConfirm)
	mux.HandleFunc("POST /join/verify", h.finish)
	return mux
}

const pageCSS = `body{margin:0}
.yp-page{box-sizing:border-box;min-height:100vh;background-color:var(--yp-bg);background-image:var(--yp-wallpaper);background-size:var(--yp-wallpaper-size);color:var(--yp-text);font-family:var(--yp-font-body);font-size:var(--yp-font-size);line-height:1.45}
.yp-page *{box-sizing:border-box}
.yp-shell{max-width:40rem;margin:0 auto;padding:1rem 1rem 2.5rem}
.yp-page h1{font-family:var(--yp-font-heading);font-size:1.9rem;line-height:1.15;letter-spacing:-.03em;margin:.75rem 0 .5rem;overflow-wrap:anywhere}
.yp-page h2{font-family:var(--yp-font-heading);font-size:1.2rem;margin:0 0 .6rem}
.yp-muted{color:var(--yp-text-muted);margin:.3rem 0}
.yp-page section{margin:1.25rem 0;padding:1rem;border:1px solid var(--yp-line);border-radius:var(--yp-radius);background:var(--yp-surface)}
.yp-page ul{margin:0;padding-left:1.3rem;color:var(--yp-text-muted)}
.yp-page li{margin:.4rem 0}
.yp-page form{display:flex;flex-direction:column;gap:.6rem}
.yp-page label.yp-field{display:flex;flex-direction:column;gap:.3rem;font-weight:700}
.yp-page input[type=email],.yp-page input[type=text]{width:100%;min-height:48px;padding:.6rem .8rem;border:2px solid var(--yp-text-muted);border-radius:var(--yp-radius);background:var(--yp-bg);color:var(--yp-text);font:inherit}
.yp-check{display:flex;gap:.6rem;align-items:flex-start;min-height:44px;padding:.5rem 0}
.yp-check input{width:1.4rem;height:1.4rem;flex:none;margin:0;accent-color:var(--yp-accent)}
.yp-page button{min-height:48px;padding:.7rem 1rem;border:0;border-radius:var(--yp-radius);background:var(--yp-accent);color:var(--yp-on-accent);font:inherit;font-weight:800;cursor:pointer}
.yp-link{display:inline-flex;align-items:center;min-height:44px;color:var(--yp-accent-text);font-weight:700}
.yp-page button:focus-visible,.yp-page input:focus-visible,.yp-link:focus-visible{outline:3px solid var(--yp-focus);outline-offset:2px}
.yp-status{padding:.8rem 1rem;border:2px solid var(--yp-line);border-radius:var(--yp-radius);background:var(--yp-surface);font-weight:700}
`

const shellOpen = `<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Yapuno – {{.Title}}</title><style>{{.CSS}}</style></head><body><main class="yp-page" data-yp-skin="classic"><div class="yp-shell">`

const shellClose = `<p class="yp-muted">Vorschau mit synthetischen Profilen.</p></div></main></body></html>`

var joinPage = template.Must(template.New("join").Parse(shellOpen + `
<h1>Einladung zu Yapuno</h1>
<p class="yp-muted">Eine Familie, die Yapuno schon nutzt, hat dich eingeladen. Hier legst du deine eigene Familie an.</p>
<section aria-labelledby="yp-join"><h2 id="yp-join">Deine Familie anlegen</h2>
<form method="post" action="/join/request"><input type="hidden" name="code" value="{{.Code}}">
<label class="yp-field">Deine E-Mail-Adresse<input type="email" name="email" autocomplete="email" required maxlength="254"></label>
<label class="yp-field">Vorname oder Spitzname deines Kindes<input type="text" name="child_name" autocomplete="off" required maxlength="40"></label>
<label class="yp-check"><input type="checkbox" name="confirm" value="yes" required> <span>Ich bin erziehungsberechtigt für dieses Kind.</span></label>
<button type="submit">Bestätigungslink senden</button></form></section>
<section aria-labelledby="yp-how"><h2 id="yp-how">Gut zu wissen</h2><ul>
<li>Du erhältst eine E-Mail mit einem Link. Er gilt 10 Minuten. Erst damit wird deine Familie angelegt.</li>
<li>Die Familie, die dich eingeladen hat, sieht weder deine Adresse noch den Namen deines Kindes.</li>
<li>Eure Kinder sind danach nicht automatisch Kontakte. Dafür braucht es weiterhin die Zustimmung beider Familien.</li>
<li>Weitere Kinder kannst du nach der Anmeldung selbst anlegen.</li>
<li>Yapuno prüft nicht, ob du wirklich erziehungsberechtigt bist. Du bestätigst das selbst.</li>
</ul></section>
<p><a class="yp-link" href="/login">Schon registriert? Zur Anmeldung</a></p>
` + shellClose))

var messagePage = template.Must(template.New("message").Parse(shellOpen + `
<h1>{{.Title}}</h1>
<p class="yp-status" role="status">{{.Text}}</p>
{{if .Detail}}<p class="yp-muted">{{.Detail}}</p>{{end}}
<p><a class="yp-link" href="/login">Zur Anmeldung</a></p>
` + shellClose))

var confirmPage = template.Must(template.New("confirm").Parse(shellOpen + `
<h1>Familie anlegen</h1>
<p class="yp-muted">Deine E-Mail-Adresse ist bestätigt. Mit dem nächsten Schritt wird deine Familie angelegt und du bist angemeldet.</p>
<section aria-labelledby="yp-finish"><h2 id="yp-finish">Letzter Schritt</h2>
<form method="post" action="/join/verify"><input type="hidden" name="token" value="{{.Token}}"><button type="submit">Jetzt Familie anlegen</button></form></section>
` + shellClose))

func secureHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; font-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
}

func (h *Handler) message(w http.ResponseWriter, status int, title, text, detail string) {
	w.WriteHeader(status)
	_ = messagePage.Execute(w, struct {
		Title, Text, Detail string
		CSS                 template.CSS
	}{title, text, detail, h.css})
}

func (h *Handler) invalidInvitation(w http.ResponseWriter) {
	h.message(w, http.StatusNotFound, "Einladung ungültig", "Diese Einladung ist nicht mehr gültig.",
		"Einladungen gelten sieben Tage und nur einmal. Bitte die Familie, die dich eingeladen hat, um einen neuen Link.")
}

func (h *Handler) invalidLink(w http.ResponseWriter) {
	h.message(w, http.StatusForbidden, "Link ungültig", "Dieser Link ist ungültig oder abgelaufen.",
		"Bestätigungslinks gelten 10 Minuten und nur einmal. Öffne die Einladung noch einmal und fordere einen neuen Link an.")
}

// invitation resolves a code to an invitation whose inviter still has access.
// A guardian who locked their access or was deactivated invites nobody.
func (h *Handler) invitation(ctx context.Context, code string) (Invitation, bool) {
	inv, err := h.cfg.Store.Lookup(code)
	if err != nil || !h.cfg.Directory.ActiveGuardian(ctx, inv.Guardian, inv.Tenant) {
		return Invitation{}, false
	}
	return inv, true
}

func (h *Handler) showForm(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	codes := r.URL.Query()["code"]
	if len(codes) != 1 {
		h.invalidInvitation(w)
		return
	}
	if _, ok := h.invitation(r.Context(), codes[0]); !ok {
		h.invalidInvitation(w)
		return
	}
	_ = joinPage.Execute(w, struct {
		Title, Code string
		CSS         template.CSS
	}{"Einladung", codes[0], h.css})
}

func normalizeEmail(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if len(email) > 254 || email == "" || strings.ContainsAny(email, "\r\n\x00") {
		return "", false
	}
	parsed, err := mail.ParseAddress(email)
	return email, err == nil && parsed.Address == email && parsed.Name == "" && strings.Count(email, "@") == 1
}

func posted(w http.ResponseWriter, r *http.Request, fields ...string) (url.Values, bool) {
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
	for _, field := range fields {
		if len(r.PostForm[field]) != 1 {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return nil, false
		}
	}
	return r.PostForm, true
}

// admit applies the in-memory limits and notes the request for the cooldown.
func (h *Handler) admit(email string, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if last, exists := h.recent[email]; exists && now.Sub(last) < emailCooldown {
		return false
	}
	if len(h.recent) >= maxPending {
		for address, last := range h.recent {
			if now.Sub(last) >= emailCooldown {
				delete(h.recent, address)
			}
		}
		if len(h.recent) >= maxPending {
			return false
		}
	}
	if len(h.pending) >= maxPending {
		for key, item := range h.pending {
			if !now.Before(item.expires) {
				delete(h.pending, key)
			}
		}
		if len(h.pending) >= maxPending {
			return false
		}
	}
	h.recent[email] = now
	return true
}

func (h *Handler) requestLink(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	form, ok := posted(w, r, "code", "email", "child_name", "confirm")
	if !ok {
		return
	}
	email, validEmail := normalizeEmail(form.Get("email"))
	child := strings.TrimSpace(form.Get("child_name"))
	if form.Get("confirm") != "yes" || !validEmail || !familystore.ValidName(child) {
		http.Error(w, "Invalid registration", http.StatusBadRequest)
		return
	}
	inv, ok := h.invitation(r.Context(), form.Get("code"))
	if !ok {
		h.invalidInvitation(w)
		return
	}
	// From here on the answer is the same whatever happens, so it reveals
	// neither whether an address is registered nor whether a limit was hit.
	defer h.message(w, http.StatusOK, "Schau in dein Postfach", "Falls diese Adresse noch nicht bei Yapuno registriert ist, haben wir dir einen Bestätigungslink geschickt.",
		"Der Link gilt 10 Minuten. Keine E-Mail erhalten? Prüfe den Spam-Ordner oder öffne die Einladung in einer Minute noch einmal.")
	now := h.cfg.Now()
	if !h.admit(email, now) {
		return
	}
	// Every admitted request is charged, also for a registered address.
	if err := h.cfg.Store.CountMail(inv.ID); err != nil {
		return
	}
	if h.cfg.Directory.Has(email) {
		return
	}
	token, err := randomText(32)
	if err != nil {
		return
	}
	key := sha256.Sum256([]byte(token))
	h.mu.Lock()
	h.pending[key] = registration{email: email, child: child, invitation: inv.ID, expires: now.Add(linkTTL)}
	h.mu.Unlock()
	link := strings.TrimRight(h.cfg.Origin, "/") + "/join/verify?token=" + url.QueryEscape(token)
	if err := h.cfg.Mailer.SendRegistrationLink(r.Context(), email, link); err != nil {
		h.mu.Lock()
		delete(h.pending, key)
		h.mu.Unlock()
	}
}

func (h *Handler) showConfirm(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	tokens := r.URL.Query()["token"]
	if len(tokens) != 1 {
		http.Error(w, "Invalid link", http.StatusBadRequest)
		return
	}
	// Showing the page consumes nothing; only the POST registers.
	_ = confirmPage.Execute(w, struct {
		Title, Token string
		CSS          template.CSS
	}{"Familie anlegen", tokens[0], h.css})
}

func newID(prefix string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(raw), nil
}

func (h *Handler) finish(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	form, ok := posted(w, r, "token")
	if !ok {
		return
	}
	token := form.Get("token")
	if decoded, err := base64.RawURLEncoding.DecodeString(token); err != nil || len(decoded) != 32 || len(token) != 43 {
		h.invalidLink(w)
		return
	}
	key := sha256.Sum256([]byte(token))
	h.mu.Lock()
	reg, exists := h.pending[key]
	// Taking the entry under the lock makes the link single use.
	delete(h.pending, key)
	h.mu.Unlock()
	if !exists || !h.cfg.Now().Before(reg.expires) {
		h.invalidLink(w)
		return
	}
	guardianID, tenantID, status := h.register(r.Context(), reg)
	switch status {
	case http.StatusOK:
	case http.StatusForbidden:
		h.invalidLink(w)
		return
	default:
		h.message(w, http.StatusServiceUnavailable, "Nicht möglich", "Die Familie konnte gerade nicht angelegt werden.",
			"Es wurde nichts gespeichert. Öffne die Einladung noch einmal und versuche es erneut.")
		return
	}
	if err := h.cfg.Sessions.IssueVerified(w, guardianID, tenantID); err != nil {
		// The family exists; the guardian signs in the normal way.
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/family", http.StatusSeeOther)
}

// register creates the family for a confirmed registration. The invitation
// is spent first and durably; the family is written before the identity, so
// an interruption can leave a family nobody can sign in to, but never an
// identity without a family. Later failures undo the earlier steps.
func (h *Handler) register(ctx context.Context, reg registration) (guardianID, tenantID string, status int) {
	h.complete.Lock()
	defer h.complete.Unlock()
	inv, err := h.cfg.Store.Usable(reg.invitation)
	if err != nil || !h.cfg.Directory.ActiveGuardian(ctx, inv.Guardian, inv.Tenant) || h.cfg.Directory.Has(reg.email) {
		return "", "", http.StatusForbidden
	}
	tenantID, err = newID("f")
	if err != nil {
		return "", "", http.StatusServiceUnavailable
	}
	guardianID, err = newID("g")
	if err != nil {
		return "", "", http.StatusServiceUnavailable
	}
	childID, err := newID("c")
	if err != nil {
		return "", "", http.StatusServiceUnavailable
	}
	if err := h.cfg.Store.Consume(inv.ID, tenantID); err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrFull) {
			return "", "", http.StatusForbidden
		}
		return "", "", http.StatusServiceUnavailable
	}
	if err := h.cfg.Families.CreateFamily(tenantID, guardianID, childID, reg.child); err != nil {
		h.reopen(inv.ID, tenantID)
		return "", "", http.StatusServiceUnavailable
	}
	if err := h.cfg.Directory.Register(reg.email, guardianID, tenantID); err != nil {
		if err := h.cfg.Families.DiscardFamily(tenantID, guardianID, childID); err != nil {
			log.Printf("registration: a family without an identity could not be discarded")
		}
		h.reopen(inv.ID, tenantID)
		return "", "", http.StatusServiceUnavailable
	}
	return guardianID, tenantID, http.StatusOK
}

func (h *Handler) reopen(invitationID, tenantID string) {
	if err := h.cfg.Store.Restore(invitationID, tenantID); err != nil {
		log.Printf("registration: an invitation could not be reopened after a failed registration")
	}
}
