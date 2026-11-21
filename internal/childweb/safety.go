package childweb

import (
	"crypto/subtle"
	"errors"
	"html/template"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/childsession"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/fontassets"
	"github.com/pblumer/yapuno/internal/messages"
	"github.com/pblumer/yapuno/internal/reports"
	"github.com/pblumer/yapuno/internal/skin"
)

// Reports stores a child's report about one message. It must authorize the
// report from the child's current right to read that message.
type Reports interface {
	File(p messages.Principal, conversationID string, seq uint64) (bool, error)
	Reported(p contacts.Party, conversationID string) map[uint64]bool
}

// reportChoices bounds how many of the other child's messages are offered.
const reportChoices = 20

// homeNotices and safetyNotices are the only texts a redirect can select.
var (
	homeNotices = map[string]string{
		"blocked":            "Blockiert. Ihr könnt euch nicht mehr schreiben.",
		"reported-blocked":   "Gemeldet und blockiert. Deine Eltern sehen die gemeldete Nachricht, wenn sie Yapuno öffnen. Sag es ihnen am besten auch selbst.",
		"blocked-unreported": "Blockiert. Die Meldung wurde aber nicht gespeichert. Sag es deinen Eltern bitte direkt.",
	}
	safetyNotices = map[string]string{
		"reported": "Gemeldet. Deine Eltern sehen diese Nachricht, wenn sie Yapuno öffnen. Sag es ihnen am besten auch selbst.",
	}
)

// fixedNotice returns the text for a single known key; the query itself is
// never rendered.
func fixedNotice(r *http.Request, texts map[string]string) string {
	if values := r.URL.Query()["done"]; len(values) == 1 {
		return texts[values[0]]
	}
	return ""
}

func (h *handler) safetyTitle() string {
	if h.opt.Reports != nil {
		return "Melden oder blockieren"
	}
	return "Blockieren"
}

func (h *handler) safetyShort() string {
	if h.opt.Reports != nil {
		return "Melden"
	}
	return "Blockieren"
}

const safetyCSS = `.yp-backlink{display:inline-flex;align-items:center;min-height:44px;color:var(--yp-accent-text);font-weight:700;text-decoration:none}
.yp-child section{margin:1.25rem 0;padding:1rem;border:1px solid var(--yp-line);border-radius:var(--yp-radius);background:var(--yp-surface)}
.yp-child section h2{margin:0 0 .6rem}
.yp-child section p{margin:.5rem 0}
.yp-child section form{margin-top:.9rem;display:flex;flex-direction:column;gap:.7rem}
.yp-child fieldset{margin:0;padding:0;border:0;min-width:0;display:flex;flex-direction:column;gap:.5rem}
.yp-child legend{padding:0;margin-bottom:.5rem;font-weight:700}
.yp-pick{display:flex;gap:.7rem;align-items:flex-start;min-height:48px;padding:.65rem .75rem;border:2px solid var(--yp-line);border-radius:var(--yp-radius);background:var(--yp-bg);color:var(--yp-text)}
.yp-pick input{flex:none;width:1.4rem;height:1.4rem;margin:.1rem 0 0;accent-color:var(--yp-accent)}
.yp-pick span{min-width:0}
.yp-pick:has(input:checked){border-color:var(--yp-accent-text)}
.yp-quote{display:block;white-space:pre-wrap;overflow-wrap:anywhere}
.yp-pick small{display:block;margin-top:.15rem;color:var(--yp-text-muted);font-weight:600}
.yp-done{flex-direction:column;gap:0;color:var(--yp-text-muted)}
.yp-check{display:flex;gap:.6rem;align-items:flex-start;min-height:44px;padding:.5rem 0}
.yp-check input{flex:none;width:1.4rem;height:1.4rem;margin:0;accent-color:var(--yp-accent)}
.yp-child button.yp-primary{border:0;background:var(--yp-accent);color:var(--yp-on-accent);font-weight:800}
.yp-child button.yp-danger{border:2px solid var(--yp-danger-text);background:var(--yp-bg);color:var(--yp-danger-text);font-weight:800}
.yp-facts{margin:0;padding-left:1.3rem;color:var(--yp-text-muted)}
.yp-facts li{margin:.3rem 0}
.yp-backlink:focus-visible,.yp-pick input:focus-visible,.yp-check input:focus-visible{outline:3px solid var(--yp-focus);outline-offset:2px}
`

var safetyPage = template.Must(template.New("child-safety").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><meta name="referrer" content="no-referrer"><title>Yapuno – {{.Title}}</title><style>{{.CSS}}</style></head><body>
<main class="yp-child" data-yp-skin="{{.Skin}}"><div class="yp-shell">
<a class="yp-backlink" href="/child/chat/{{.Conversation}}">← Zurück zum Chat</a>
<h1>{{.Title}}</h1>
<p class="yp-muted">Stört dich etwas oder macht dir etwas Angst? Das ist nicht deine Schuld. Deine Eltern können dir helfen.</p>
{{if .Alert}}<p class="yp-alert" role="alert">{{.Alert}}</p>{{end}}{{if .Notice}}<p class="yp-status" role="status">{{.Notice}}</p>{{end}}
{{if .Reports}}<section aria-labelledby="yp-report"><h2 id="yp-report">Eine Nachricht melden</h2>
<p>Deine Eltern sehen genau diese eine Nachricht und dass du sie gemeldet hast. Sonst sehen sie nichts aus dem Chat. {{.Peer}} erfährt nichts davon.</p>
{{if .Messages}}<form method="post" action="/child/chat/{{.Conversation}}/report"><input type="hidden" name="csrf_token" value="{{.CSRF}}">
<fieldset><legend>Welche Nachricht von {{.Peer}}?</legend>
{{range .Messages}}{{if .Reported}}<div class="yp-pick yp-done"><span class="yp-quote">{{.Body}}</span><small><time datetime="{{.ISO}}">{{.Clock}}</time> · Schon gemeldet</small></div>
{{else}}<label class="yp-pick"><input type="radio" name="seq" value="{{.Seq}}" required><span><span class="yp-quote">{{.Body}}</span><small><time datetime="{{.ISO}}">{{.Clock}}</time></small></span></label>
{{end}}{{end}}</fieldset>
{{if .Selectable}}<label class="yp-check"><input type="checkbox" name="block" value="yes"> <span>Ausserdem {{.Peer}} blockieren.</span></label>
<button class="yp-primary" type="submit">Diese Nachricht melden</button>{{end}}</form>
<p class="yp-muted">Deine Eltern sehen die Meldung, wenn sie Yapuno öffnen.{{if .Notices}} Yapuno schreibt deinen Eltern eine E-Mail, dass du etwas gemeldet hast, und erinnert sie, falls sie die Meldung nicht ansehen. Die Nachricht selbst steht nicht in der E-Mail.{{end}} Sag es ihnen am besten auch selbst.</p>
{{else}}<p class="yp-muted">{{.Peer}} hat dir hier noch nichts geschrieben.</p>{{end}}</section>{{end}}
<section aria-labelledby="yp-block"><h2 id="yp-block">{{.Peer}} blockieren</h2>
<ul class="yp-facts"><li>Ihr könnt euch nicht mehr schreiben.</li><li>Der Chat verschwindet.</li><li>Nur deine Eltern können das wieder aufheben.</li><li>Deine Eltern sehen, dass du blockiert hast, aber keine Nachrichten.</li></ul>
<form method="post" action="/child/chat/{{.Conversation}}/block"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><label class="yp-check"><input type="checkbox" name="confirm" value="yes" required> <span>Ja, {{.Peer}} blockieren.</span></label><button class="yp-danger" type="submit">Blockieren</button></form></section>
</div></main></body></html>`))

type reportChoice struct {
	Seq      uint64
	Body     string
	ISO      string
	Clock    string
	Reported bool
}

// renderSafety writes the page for one of the child's own conversations.
// Anybody who cannot read the conversation is sent to the start page.
func (h *handler) renderSafety(w http.ResponseWriter, r *http.Request, s childsession.Session, conversationID string, status int, alert, notice string) {
	peer, ok := h.peerName(s, conversationID)
	if !ok {
		http.Redirect(w, r, "/child", http.StatusSeeOther)
		return
	}
	recent, err := h.opt.Messages.Recent(principal(s), conversationID, chatPageSize)
	if err != nil {
		http.Redirect(w, r, "/child", http.StatusSeeOther)
		return
	}
	self := contacts.Party{TenantID: s.TenantID, ChildID: s.ChildID}
	var choices []reportChoice
	selectable := false
	if h.opt.Reports != nil {
		reported := h.opt.Reports.Reported(self, conversationID)
		// Newest first; only messages of the other child can be reported.
		for i := len(recent) - 1; i >= 0 && len(choices) < reportChoices; i-- {
			m := recent[i]
			if m.From == self {
				continue
			}
			choices = append(choices, reportChoice{Seq: m.Seq, Body: m.Body, Reported: reported[m.Seq],
				ISO: m.At.UTC().Format(time.RFC3339), Clock: m.At.Local().Format("02.01. 15:04")})
			selectable = selectable || !reported[m.Seq]
		}
	}
	pref := appearance.Default()
	if h.opt.Looks != nil {
		pref = h.opt.Looks.Get(appearance.Subject{Kind: appearance.KindChild, TenantID: s.TenantID, ID: s.ChildID})
	}
	catalog := skin.Catalog()
	def := skin.Resolve(pref.Skin, catalog, catalog["classic"])
	tokens, err := skin.CSSVariables(def)
	if err != nil {
		http.Error(w, "Unable to display page", http.StatusInternalServerError)
		return
	}
	decls := strings.TrimSuffix(strings.TrimPrefix(tokens, ":root{"), "}")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = safetyPage.Execute(w, struct {
		Skin, Title, Peer, Conversation, CSRF, Alert, Notice string
		Reports, Selectable, Notices                         bool
		CSS                                                  template.CSS
		Messages                                             []reportChoice
	}{
		Skin: def.ID, Title: h.safetyTitle(), Peer: peer, Conversation: conversationID, CSRF: s.CSRFToken, Alert: alert, Notice: notice,
		Reports: h.opt.Reports != nil, Selectable: selectable, Notices: h.opt.ReportNotices, Messages: choices,
		// Validated first-party skin tokens and fixed component rules only.
		CSS: template.CSS(fontassets.FaceCSS(def.Typography.Heading, def.Typography.Body) + ".yp-child{" + decls + "}\n" + homeCSS + safetyCSS),
	})
}

func (h *handler) safety(w http.ResponseWriter, r *http.Request) {
	headers(w, false)
	id := r.PathValue("id")
	if !conversationPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	s, err := h.sessions.Authenticate(r)
	if err != nil {
		http.Redirect(w, r, "/child", http.StatusSeeOther)
		return
	}
	h.renderSafety(w, r, s, id, http.StatusOK, "", fixedNotice(r, safetyNotices))
}

// safetyForm authenticates the child and enforces form encoding, a small
// body, exactly one value for each required field and the session's CSRF
// token. It never reads identity from the form.
func (h *handler) safetyForm(w http.ResponseWriter, r *http.Request, fields ...string) (childsession.Session, string, url.Values, bool) {
	headers(w, false)
	id := r.PathValue("id")
	if !conversationPattern.MatchString(id) {
		http.NotFound(w, r)
		return childsession.Session{}, "", nil, false
	}
	s, err := h.sessions.Authenticate(r)
	if err != nil {
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return childsession.Session{}, "", nil, false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		http.Error(w, "Expected form data", http.StatusUnsupportedMediaType)
		return childsession.Session{}, "", nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return childsession.Session{}, "", nil, false
	}
	for _, field := range append([]string{"csrf_token"}, fields...) {
		if len(r.PostForm[field]) != 1 {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return childsession.Session{}, "", nil, false
		}
	}
	token := r.PostForm.Get("csrf_token")
	if len(token) != len(s.CSRFToken) || subtle.ConstantTimeCompare([]byte(token), []byte(s.CSRFToken)) != 1 {
		http.Error(w, "Invalid CSRF token", http.StatusForbidden)
		return childsession.Session{}, "", nil, false
	}
	return s, id, r.PostForm, true
}

// block suspends the contact for both children. Only the child's own
// guardians can lift it.
func (h *handler) block(w http.ResponseWriter, r *http.Request) {
	s, id, form, ok := h.safetyForm(w, r, "confirm")
	if !ok {
		return
	}
	if form.Get("confirm") != "yes" {
		http.Error(w, "Explicit confirmation required", http.StatusBadRequest)
		return
	}
	if err := h.opt.Contacts.Block(contacts.Party{TenantID: s.TenantID, ChildID: s.ChildID}, id); err != nil {
		if errors.Is(err, contacts.ErrNotFound) {
			http.Error(w, "Not allowed", http.StatusForbidden)
			return
		}
		http.Error(w, "Unable to block", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/child?done=blocked", http.StatusSeeOther)
}

// report files one message with the child's own guardians and, if asked,
// blocks the contact. A failed report never prevents the block.
func (h *handler) report(w http.ResponseWriter, r *http.Request) {
	s, id, form, ok := h.safetyForm(w, r, "seq")
	if !ok {
		return
	}
	seq, err := strconv.ParseUint(form.Get("seq"), 10, 64)
	if err != nil || seq == 0 {
		http.Error(w, "Invalid message", http.StatusBadRequest)
		return
	}
	alsoBlock := false
	switch choice := form["block"]; {
	case len(choice) == 0:
	case len(choice) == 1 && choice[0] == "yes":
		alsoBlock = true
	default:
		http.Error(w, "Invalid block option", http.StatusBadRequest)
		return
	}
	_, err = h.opt.Reports.File(principal(s), id, seq)
	stored := err == nil
	switch {
	case stored:
	case errors.Is(err, reports.ErrForbidden):
		http.Error(w, "Not allowed", http.StatusForbidden)
		return
	case errors.Is(err, reports.ErrNotFound), errors.Is(err, reports.ErrInvalid):
		http.Error(w, "Invalid message", http.StatusBadRequest)
		return
	case alsoBlock:
		// Fall through: the child asked for protection as well.
	case errors.Is(err, reports.ErrLimit):
		h.renderSafety(w, r, s, id, http.StatusTooManyRequests, "Du hast schon viele Nachrichten gemeldet. Sprich jetzt direkt mit deinen Eltern.", "")
		return
	default:
		h.renderSafety(w, r, s, id, http.StatusServiceUnavailable, "Die Meldung konnte nicht gespeichert werden. Sag es deinen Eltern bitte direkt.", "")
		return
	}
	if !alsoBlock {
		http.Redirect(w, r, "/child/chat/"+id+"/safety?done=reported", http.StatusSeeOther)
		return
	}
	if err := h.opt.Contacts.Block(contacts.Party{TenantID: s.TenantID, ChildID: s.ChildID}, id); err != nil {
		http.Error(w, "Unable to block", http.StatusInternalServerError)
		return
	}
	if stored {
		http.Redirect(w, r, "/child?done=reported-blocked", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/child?done=blocked-unreported", http.StatusSeeOther)
}
