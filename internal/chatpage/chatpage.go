// Package chatpage is the chat page shared by the child and the guardian
// area: markup, styles and the progressive-enhancement script. It knows no
// session, store or authorization; callers authorize and pass what to show.
package chatpage

import (
	"bytes"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/yapuno/internal/fontassets"
	"github.com/pblumer/yapuno/internal/skin"
)

// JS and UtilsJS are served by each area under its own path.
//
//go:embed chat.js
var JS []byte

//go:embed chat-utils.js
var UtilsJS []byte

const pageCSS = `body{margin:0}
.yp-chat{--yp-chat-side:calc(50% - 20rem);box-sizing:border-box;min-height:100vh;display:flex;flex-direction:column;background-color:var(--yp-bg);background-image:var(--yp-wallpaper);background-size:var(--yp-wallpaper-size);color:var(--yp-text);font-family:var(--yp-font-body);font-size:var(--yp-font-size);line-height:1.4}
.yp-chat *{box-sizing:border-box}
.yp-sr{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap}
.yp-head{display:flex;align-items:center;gap:.5rem;padding:.6rem .6rem;padding-top:max(.6rem,env(safe-area-inset-top));padding-inline:max(.6rem,var(--yp-chat-side));background:var(--yp-surface);border-bottom:1px solid var(--yp-line)}
.yp-back{flex:none;display:grid;place-items:center;min-width:44px;min-height:44px;border-radius:var(--yp-radius);color:var(--yp-accent-text);font-weight:800;text-decoration:none}
.yp-head svg{flex:none}
.yp-head h1{font-family:var(--yp-font-heading);font-size:1.15rem;margin:0;overflow-wrap:anywhere}
.yp-head p{margin:0;font-size:.8rem;color:var(--yp-text-muted)}
.yp-head div{flex:1;min-width:0}
.yp-help{flex:none;display:grid;place-items:center;min-width:44px;min-height:44px;padding:0 .5rem;border:2px solid var(--yp-line);border-radius:var(--yp-radius);background:var(--yp-bg);color:var(--yp-text);font-size:.85rem;font-weight:700;text-decoration:none}
.yp-banner{margin:0;padding:.6rem .9rem;padding-inline:max(.9rem,var(--yp-chat-side));background:var(--yp-surface);border-bottom:2px solid var(--yp-danger-text);font-weight:700}
.yp-list{flex:1;list-style:none;margin:0;padding:1rem .75rem;padding-inline:max(.75rem,var(--yp-chat-side));display:flex;flex-direction:column;gap:.5rem}
.yp-msg{max-width:82%;display:flex;flex-direction:column;gap:.15rem}
.yp-mine{align-self:flex-end;align-items:flex-end}
.yp-theirs{align-self:flex-start;align-items:flex-start}
.yp-text{margin:0;padding:.6rem .85rem;border-radius:var(--yp-bubble-radius);border:var(--yp-bubble-border);box-shadow:var(--yp-bubble-shadow);white-space:pre-wrap;overflow-wrap:anywhere}
.yp-mine .yp-text{background:var(--yp-own-bubble);color:var(--yp-on-own-bubble)}
.yp-theirs .yp-text{background:var(--yp-other-bubble);color:var(--yp-on-other-bubble)}
.yp-meta{margin:0;font-size:.75rem;font-weight:600;color:var(--yp-text-muted)}
.yp-empty{align-self:center;margin:auto 0;color:var(--yp-text-muted);text-align:center}
.yp-retry{min-height:44px;padding:.4rem .9rem;border:2px solid var(--yp-text-muted);border-radius:var(--yp-radius);background:var(--yp-bg);color:var(--yp-text);font:inherit;font-weight:700;cursor:pointer}
.yp-compose{position:sticky;bottom:0;display:flex;gap:.5rem;align-items:flex-end;padding:.6rem .75rem;padding-bottom:max(.6rem,env(safe-area-inset-bottom));padding-inline:max(.75rem,var(--yp-chat-side));background:var(--yp-bg);border-top:1px solid var(--yp-line)}
.yp-compose textarea{flex:1;min-width:0;min-height:48px;max-height:9rem;padding:.7rem .9rem;border:2px solid var(--yp-text-muted);border-radius:var(--yp-radius);background:var(--yp-bg);color:var(--yp-text);font:inherit;resize:none}
.yp-send{flex:none;min-width:48px;min-height:48px;padding:0 1rem;border:0;border-radius:var(--yp-radius);background:var(--yp-accent);color:var(--yp-on-accent);font:inherit;font-weight:800;cursor:pointer}
.yp-chat :disabled{opacity:.55;cursor:not-allowed}
@media (max-width:400px){.yp-head{flex-wrap:wrap;row-gap:.15rem}.yp-head div{display:contents}.yp-head h1{flex:1;min-width:0;order:1}.yp-help{order:2}.yp-head p{order:3;flex-basis:100%}}
.yp-help:focus-visible,.yp-back:focus-visible,.yp-send:focus-visible,.yp-retry:focus-visible,.yp-compose textarea:focus-visible{outline:3px solid var(--yp-focus);outline-offset:2px}
`

var page = template.Must(template.New("chat").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><meta name="referrer" content="no-referrer"><title>Yapuno – {{.Peer}}</title><style>{{.CSS}}</style></head><body>
<main class="yp-chat" id="yp-chat" data-yp-skin="{{.Skin}}" data-conversation="{{.Conversation}}" data-csrf="{{.CSRF}}" data-last-seq="{{.LastSeq}}" data-base="{{.Path}}/messages" data-home="{{.Home}}">
<header class="yp-head"><a class="yp-back" href="{{.Home}}" aria-label="{{.HomeLabel}}">←</a>{{.PeerFigure}}<div><h1>{{.Peer}}</h1><p>{{.Subtitle}}</p></div>{{if .SafetyHref}}<a class="yp-help" href="{{.SafetyHref}}" aria-label="{{.SafetyLabel}}">{{.SafetyText}}</a>{{end}}</header>
<p class="yp-banner" id="yp-banner" role="{{if .Alert}}alert{{else}}status{{end}}"{{if not .Alert}} hidden{{end}}>{{.Alert}}</p>
<ol class="yp-list" id="yp-messages" aria-label="Nachrichten">{{range .Messages}}<li class="yp-msg {{if .Mine}}yp-mine{{else}}yp-theirs{{end}}" data-seq="{{.Seq}}"><p class="yp-text">{{.Body}}</p><p class="yp-meta">{{if .Mine}}Gesendet · {{end}}<time datetime="{{.ISO}}">{{.Clock}}</time></p></li>{{else}}<li class="yp-empty">Noch keine Nachrichten. Schreib die erste!</li>{{end}}</ol>
<form class="yp-compose" id="yp-compose" method="post" action="{{.Path}}/send"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="client_id" value="{{.ClientID}}"><label class="yp-sr" for="yp-body">Nachricht an {{.Peer}}</label><textarea id="yp-body" name="body" rows="1" required maxlength="1000" autocomplete="off" enterkeyhint="send" placeholder="Nachricht schreiben">{{.Draft}}</textarea><button class="yp-send" type="submit">Senden</button></form>
</main>
<script src="{{.Assets}}/chat-utils.js" defer></script>
<script src="{{.Assets}}/chat.js" defer></script>
</body></html>`))

// Message is one message as the reader sees it.
type Message struct {
	Seq   uint64
	Mine  bool
	Body  string
	ISO   string
	Clock string
}

// NewMessage formats one message for the page.
func NewMessage(seq uint64, mine bool, body string, at time.Time) Message {
	return Message{Seq: seq, Mine: mine, Body: body, ISO: at.UTC().Format(time.RFC3339), Clock: at.Local().Format("15:04")}
}

// Page is everything one conversation page shows. All text is escaped by
// the template; PeerFigure MUST be trusted markup from appearance.FigureSVG.
type Page struct {
	// Skin is the reader's skin id; unknown ids fall back to classic.
	Skin string
	// Peer names the other participant; Subtitle says why they may talk.
	Peer, Subtitle string
	PeerFigure     template.HTML
	// Home is where the back link leads and where the script sends a reader
	// who is signed out; HomeLabel is the back link's accessible name.
	Home, HomeLabel string
	// Path is the conversation's own path, for example "/child/chat/<id>".
	// The form posts to Path+"/send"; the script uses Path+"/messages".
	Path string
	// Assets is the path prefix under which JS and UtilsJS are served.
	Assets                                     string
	Conversation, CSRF, ClientID, Alert, Draft string
	// SafetyHref adds a link to block or report; empty means none.
	SafetyHref, SafetyLabel, SafetyText string
	LastSeq                             uint64
	Messages                            []Message
}

// Render writes the page. It writes nothing when it returns an error.
func Render(w http.ResponseWriter, status int, p Page) error {
	catalog := skin.Catalog()
	def := skin.Resolve(p.Skin, catalog, catalog["classic"])
	tokens, err := skin.CSSVariables(def)
	if err != nil {
		return err
	}
	decls := strings.TrimSuffix(strings.TrimPrefix(tokens, ":root{"), "}")
	var out bytes.Buffer
	if err := page.Execute(&out, struct {
		Page
		Skin string
		// Validated first-party skin tokens and fixed component rules only.
		CSS template.CSS
	}{Page: p, Skin: def.ID, CSS: template.CSS(fontassets.FaceCSS(def.Typography.Heading, def.Typography.Body) + ".yp-chat{" + decls + "}\n" + pageCSS)}); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(out.Bytes())
	return nil
}

// NewClientID returns a fresh idempotency key for the no-script form.
func NewClientID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// WireMessage is one message of the scripted history response. It says only
// whether a message is the reader's own; it carries no identifiers.
type WireMessage struct {
	Seq  uint64 `json:"seq"`
	Mine bool   `json:"mine"`
	Body string `json:"body"`
	At   string `json:"at"`
}

// NewWireMessage formats one message for the scripted history response.
func NewWireMessage(seq uint64, mine bool, body string, at time.Time) WireMessage {
	return WireMessage{Seq: seq, Mine: mine, Body: body, At: at.UTC().Format(time.RFC3339Nano)}
}

// WriteJSON writes a complete JSON response or an error, never half of one.
func WriteJSON(w http.ResponseWriter, status int, value any) {
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(value); err != nil {
		http.Error(w, "Unable to encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b.Bytes())
}

// JSONError writes the error shape the script understands.
func JSONError(w http.ResponseWriter, status int, code string) {
	WriteJSON(w, status, map[string]string{"error": code})
}
