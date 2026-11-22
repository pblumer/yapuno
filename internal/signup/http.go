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
	"github.com/pblumer/yapuno/internal/publicpage"
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

// Connections makes two children contacts when both guardians agreed: the
// inviting guardian by naming a child in the invitation, the invited one in
// the last step of the registration.
type Connections interface {
	// Inviting returns the first name of the inviting child, if this guardian
	// may approve contacts for it right now.
	Inviting(guardianID, tenantID, childID string) (string, bool)
	// Connect must check the current authority of both guardians itself.
	Connect(inviterGuardian, inviterTenant, inviterChild, guardianID, tenantID, childID string) error
}

type Config struct {
	Origin    string
	Store     *Store
	Directory Directory
	Families  Families
	Mailer    Mailer
	Sessions  Sessions
	Now       func() time.Time
	// Connections is optional. Without it an invitation only registers.
	Connections Connections
	// ParentsChat says that guardians of connected children can write to
	// each other on this installation, so the pages say so before asking.
	ParentsChat bool
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
	css, err := publicpage.Styles()
	if err != nil {
		return nil, err
	}
	return &Handler{cfg: cfg, css: css, pending: make(map[[32]byte]registration), recent: make(map[string]time.Time)}, nil
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /join", h.showForm)
	mux.HandleFunc("POST /join/request", h.requestLink)
	mux.HandleFunc("GET /join/verify", h.showConfirm)
	mux.HandleFunc("POST /join/verify", h.finish)
	return mux
}

const (
	shellOpen  = publicpage.Open
	shellClose = publicpage.Close
)

var joinPage = template.Must(template.New("join").Parse(shellOpen + `
<h1>Einladung zu Yapuno</h1>
<p class="yp-muted">Eine Familie, die Yapuno schon nutzt, hat dich eingeladen. Hier legst du deine eigene Familie an.</p>
{{if .Inviting}}<p class="yp-status">Die Einladung kommt von der Familie von {{.Inviting}}. Im letzten Schritt entscheidest du, ob dein Kind und {{.Inviting}} miteinander schreiben dürfen.</p>{{end}}
<section aria-labelledby="yp-join"><h2 id="yp-join">Deine Familie anlegen</h2>
<form method="post" action="/join/request"><input type="hidden" name="code" value="{{.Code}}">
<label class="yp-field">Deine E-Mail-Adresse<input type="email" name="email" autocomplete="email" required maxlength="254"></label>
<label class="yp-field">Vorname oder Spitzname deines Kindes<input type="text" name="child_name" autocomplete="off" required maxlength="40"></label>
<label class="yp-check"><input type="checkbox" name="confirm" value="yes" required> <span>Ich bin erziehungsberechtigt für dieses Kind.</span></label>
<button type="submit">Bestätigungslink senden</button></form></section>
<section aria-labelledby="yp-how"><h2 id="yp-how">Gut zu wissen</h2><ul>
<li>Du erhältst eine E-Mail mit einem Link. Er gilt 10 Minuten. Erst damit wird deine Familie angelegt.</li>
{{if .Inviting}}<li>Die Familie, die dich eingeladen hat, sieht deine Adresse nicht. Den Vornamen deines Kindes sieht sie nur, wenn du die Kinder verbindest.</li>
<li>Verbindest du die Kinder, dürfen sie einander schreiben.{{if .ParentsChat}} Auch die Eltern beider Familien können sich dann schreiben.{{end}} Jede Familie kann die Verbindung jederzeit wieder beenden.</li>
{{else}}<li>Die Familie, die dich eingeladen hat, sieht weder deine Adresse noch den Namen deines Kindes.</li>
<li>Eure Kinder sind danach nicht automatisch Kontakte. Dafür braucht es weiterhin die Zustimmung beider Familien.</li>
{{end}}<li>Weitere Kinder kannst du nach der Anmeldung selbst anlegen.</li>
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
{{if .Inviting}}<p>Die Einladung kommt von der Familie von {{.Inviting}}. Dürfen {{.Child}} und {{.Inviting}} miteinander schreiben?</p>
<p class="yp-muted">Die andere Familie hat dem schon zugestimmt. Sagst du Ja, sieht sie den Vornamen deines Kindes{{if .ParentsChat}}, und die Eltern beider Familien können sich schreiben{{end}}. Jede Familie kann die Verbindung jederzeit wieder beenden.</p>
<form method="post" action="/join/verify"><input type="hidden" name="token" value="{{.Token}}"><button type="submit" name="connect" value="yes">Familie anlegen und Kinder verbinden</button><button class="yp-quiet" type="submit" name="connect" value="no">Nur Familie anlegen</button></form>
{{else}}<form method="post" action="/join/verify"><input type="hidden" name="token" value="{{.Token}}"><button type="submit">Jetzt Familie anlegen</button></form>{{end}}</section>
` + shellClose))

func secureHeaders(w http.ResponseWriter) { publicpage.Headers(w) }

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
	inv, ok := h.invitation(r.Context(), codes[0])
	if !ok {
		h.invalidInvitation(w)
		return
	}
	_ = joinPage.Execute(w, struct {
		Title, Code, Inviting string
		ParentsChat           bool
		CSS                   template.CSS
	}{"Einladung", codes[0], h.inviting(inv), h.cfg.ParentsChat, h.css})
}

// inviting names the child the invitation offers a contact with. It is empty
// when the invitation names none, contacts are off, or the inviting guardian
// may no longer approve contacts for that child.
func (h *Handler) inviting(inv Invitation) string {
	if inv.Child == "" || h.cfg.Connections == nil {
		return ""
	}
	name, ok := h.cfg.Connections.Inviting(inv.Guardian, inv.Tenant, inv.Child)
	if !ok {
		return ""
	}
	return name
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
	// Showing the page consumes nothing; only the POST registers. A link
	// that is still good also shows which children it could connect.
	var child, inviting string
	h.mu.Lock()
	reg, exists := h.pending[sha256.Sum256([]byte(tokens[0]))]
	h.mu.Unlock()
	if exists && h.cfg.Now().Before(reg.expires) {
		if inv, err := h.cfg.Store.Usable(reg.invitation); err == nil && h.cfg.Directory.ActiveGuardian(r.Context(), inv.Guardian, inv.Tenant) {
			if inviting = h.inviting(inv); inviting != "" {
				child = reg.child
			}
		}
	}
	_ = confirmPage.Execute(w, struct {
		Title, Token, Child, Inviting string
		ParentsChat                   bool
		CSS                           template.CSS
	}{"Familie anlegen", tokens[0], child, inviting, h.cfg.ParentsChat, h.css})
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
	// The choice is one button; two answers are no answer.
	if len(form["connect"]) > 1 {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	connect := form.Get("connect") == "yes"
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
	guardianID, tenantID, connected, status := h.register(r.Context(), reg, connect)
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
	http.Redirect(w, r, "/family"+connected, http.StatusSeeOther)
}

// What the family page is told about the connection the guardian asked for.
const (
	notAsked    = ""
	connected   = "?done=connected"
	unconnected = "?done=unconnected"
)

// register creates the family for a confirmed registration. The invitation
// is spent first and durably; the family is written before the identity, so
// an interruption can leave a family nobody can sign in to, but never an
// identity without a family. Later failures undo the earlier steps.
//
// With connect the new guardian agreed to the contact the invitation offers.
// The children are connected last: a failure there leaves a complete family,
// which can still connect with a contact code.
func (h *Handler) register(ctx context.Context, reg registration, connect bool) (guardianID, tenantID, outcome string, status int) {
	h.complete.Lock()
	defer h.complete.Unlock()
	inv, err := h.cfg.Store.Usable(reg.invitation)
	if err != nil || !h.cfg.Directory.ActiveGuardian(ctx, inv.Guardian, inv.Tenant) || h.cfg.Directory.Has(reg.email) {
		return "", "", notAsked, http.StatusForbidden
	}
	tenantID, err = newID("f")
	if err != nil {
		return "", "", notAsked, http.StatusServiceUnavailable
	}
	guardianID, err = newID("g")
	if err != nil {
		return "", "", notAsked, http.StatusServiceUnavailable
	}
	childID, err := newID("c")
	if err != nil {
		return "", "", notAsked, http.StatusServiceUnavailable
	}
	if err := h.cfg.Store.Consume(inv.ID, tenantID); err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrFull) {
			return "", "", notAsked, http.StatusForbidden
		}
		return "", "", notAsked, http.StatusServiceUnavailable
	}
	if err := h.cfg.Families.CreateFamily(tenantID, guardianID, childID, reg.child); err != nil {
		h.reopen(inv.ID, tenantID)
		return "", "", notAsked, http.StatusServiceUnavailable
	}
	if err := h.cfg.Directory.Register(reg.email, guardianID, tenantID); err != nil {
		if err := h.cfg.Families.DiscardFamily(tenantID, guardianID, childID); err != nil {
			log.Printf("registration: a family without an identity could not be discarded")
		}
		h.reopen(inv.ID, tenantID)
		return "", "", notAsked, http.StatusServiceUnavailable
	}
	if !connect || inv.Child == "" || h.cfg.Connections == nil {
		return guardianID, tenantID, notAsked, http.StatusOK
	}
	if err := h.cfg.Connections.Connect(inv.Guardian, inv.Tenant, inv.Child, guardianID, tenantID, childID); err != nil {
		log.Printf("registration: the children could not be connected; the family is registered")
		return guardianID, tenantID, unconnected, http.StatusOK
	}
	return guardianID, tenantID, connected, http.StatusOK
}

func (h *Handler) reopen(invitationID, tenantID string) {
	if err := h.cfg.Store.Restore(invitationID, tenantID); err != nil {
		log.Printf("registration: an invitation could not be reopened after a failed registration")
	}
}
