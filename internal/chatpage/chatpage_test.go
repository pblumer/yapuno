package chatpage

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func render(t *testing.T, p Page) (*httptest.ResponseRecorder, string) {
	t.Helper()
	w := httptest.NewRecorder()
	if err := Render(w, http.StatusOK, p); err != nil {
		t.Fatal(err)
	}
	return w, w.Body.String()
}

func TestThePageTellsTheScriptWhereItLives(t *testing.T) {
	at := time.Date(2026, 11, 22, 10, 30, 0, 0, time.UTC)
	w, body := render(t, Page{Skin: "night", Peer: "Alex", Subtitle: "Nur du und dein Kind", Home: "/family/chat", HomeLabel: "Zurück zu den Chats",
		Path: "/family/chat/abc", Assets: "/family", Conversation: "abc", CSRF: "token", ClientID: "client", LastSeq: 2,
		Messages: []Message{NewMessage(1, false, "Hoi", at), NewMessage(2, true, "Sali", at)}})
	if w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("content type %q", w.Header().Get("Content-Type"))
	}
	for _, want := range []string{`<html lang="de">`, `data-yp-skin="night"`, `data-conversation="abc"`, `data-csrf="token"`, `data-last-seq="2"`,
		`data-base="/family/chat/abc/messages"`, `data-home="/family/chat"`, `href="/family/chat" aria-label="Zurück zu den Chats"`,
		`action="/family/chat/abc/send"`, `<script src="/family/chat-utils.js" defer></script>`, `<script src="/family/chat.js" defer></script>`,
		"<h1>Alex</h1>", "<p>Nur du und dein Kind</p>", `name="client_id" value="client"`, `datetime="2026-11-22T10:30:00Z"`, "yp-mine", "yp-theirs"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// No safety link unless one is given.
	if strings.Contains(body, `class="yp-help"`) {
		t.Error("safety link without a target")
	}
	_, with := render(t, Page{Peer: "Bea", SafetyHref: "/child/chat/x/safety", SafetyLabel: "Melden oder blockieren", SafetyText: "Melden"})
	if !strings.Contains(with, `<a class="yp-help" href="/child/chat/x/safety" aria-label="Melden oder blockieren">Melden</a>`) {
		t.Error("safety link missing")
	}
	style := body[strings.Index(body, "<style>")+7 : strings.Index(body, "</style>")]
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).MatchString(style[strings.Index(style, "body{margin:0}"):]) {
		t.Error("component styles must use skin tokens only")
	}
}

func TestEverythingShownIsEscapedAndUnknownSkinsFallBack(t *testing.T) {
	const hostile = `"><script>alert(1)</script>`
	_, body := render(t, Page{Skin: hostile, Peer: hostile, Subtitle: hostile, Home: "/child", HomeLabel: hostile, Path: "/child/chat/x", Assets: "/child",
		Conversation: hostile, CSRF: hostile, ClientID: hostile, Alert: hostile, Draft: hostile, Messages: []Message{NewMessage(1, false, hostile, time.Now())}})
	if strings.Contains(body, "<script>alert") || !strings.Contains(body, `data-yp-skin="classic"`) || !strings.Contains(body, `role="alert"`) {
		t.Fatalf("hostile input: %s", body)
	}
	_, empty := render(t, Page{Peer: "Alex"})
	if !strings.Contains(empty, "Noch keine Nachrichten") || !strings.Contains(empty, `id="yp-banner" role="status" hidden`) {
		t.Error("empty conversation or hidden banner missing")
	}
}

func TestHelpersProduceWhatTheScriptExpects(t *testing.T) {
	a, _ := NewClientID()
	b, _ := NewClientID()
	if a == b || !regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`).MatchString(a) {
		t.Fatalf("client ids %q %q", a, b)
	}
	at := time.Date(2026, 11, 22, 10, 30, 0, 123000000, time.FixedZone("x", 3600))
	if m := NewWireMessage(7, true, "Hoi", at); m.Seq != 7 || !m.Mine || m.Body != "Hoi" || m.At != "2026-11-22T09:30:00.123Z" {
		t.Fatalf("wire message %+v", m)
	}
	w := httptest.NewRecorder()
	JSONError(w, http.StatusForbidden, "not_allowed")
	if w.Code != http.StatusForbidden || w.Header().Get("Content-Type") != "application/json" || strings.TrimSpace(w.Body.String()) != `{"error":"not_allowed"}` {
		t.Fatalf("json error: %d %q", w.Code, w.Body)
	}
	if len(JS) == 0 || len(UtilsJS) == 0 || !strings.Contains(string(JS), "root.dataset.base") || !strings.Contains(string(JS), "root.dataset.home") {
		t.Fatal("script missing or not path-agnostic")
	}
}

// On a wide screen the bars still span the window, but everything in them
// and the messages share one centred column; on a phone the column is wider
// than the screen and the plain paddings apply.
func TestWideScreensKeepTheConversationInAReadableColumn(t *testing.T) {
	_, body := render(t, Page{Peer: "Alex"})
	style := body[strings.Index(body, "<style>")+7 : strings.Index(body, "</style>")]
	if !strings.Contains(style, "--yp-chat-side:calc(50% - 20rem)") {
		t.Fatal("no column width")
	}
	inColumn := regexp.MustCompile(`padding-inline:max\(\.\d+rem,var\(--yp-chat-side\)\)`)
	for _, selector := range []string{".yp-head", ".yp-banner", ".yp-list", ".yp-compose"} {
		rule := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(selector) + `\{[^}]*\}`).FindString(style)
		if !inColumn.MatchString(rule) {
			t.Errorf("%s is not held in the column: %q", selector, rule)
		}
	}
}

// A wide screen lists the reader's conversations beside the open one. A
// phone keeps the single page and its back link: the list is not displayed.
func TestTheReadersConversationsAreListedBesideTheOpenOneOnWideScreensOnly(t *testing.T) {
	const hostile = `"><script>alert(1)</script>`
	_, body := render(t, Page{Peer: "Alex", Threads: []ThreadGroup{
		{Title: "Deine Kinder", Threads: []Thread{{Name: "Alex", Href: "/family/chat/a", Figure: `<svg width="44"></svg>`, Current: true}}},
		{Title: "Leer"},
		{Title: "Andere Eltern", Threads: []Thread{{Name: "Sam", Detail: "Elternteil von Bea", Href: "/family/chat/b"}, {Name: hostile, Detail: hostile, Href: "/family/chat/c"}}},
	}})
	for _, want := range []string{
		`<div class="yp-chat yp-split" id="yp-chat"`, `<nav class="yp-side" aria-label="Chats">`,
		`<p class="yp-group" id="yp-group-0">Deine Kinder</p><ul aria-labelledby="yp-group-0">`,
		`<li><a href="/family/chat/a" aria-current="page"><svg width="44"></svg><span>Alex</span></a></li>`,
		`<p class="yp-group" id="yp-group-1">Andere Eltern</p><ul aria-labelledby="yp-group-1">`,
		`<li><a href="/family/chat/b"><span>Sam<small>Elternteil von Bea</small></span></a></li>`, `<main class="yp-pane">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Count(body, `aria-current="page"`) != 1 || strings.Contains(body, "<script>alert") || strings.Contains(body, "Leer") {
		t.Error("more than one open conversation, unescaped text or an empty group")
	}
	if list, pane := strings.Index(body, `<nav class="yp-side"`), strings.Index(body, `<main class="yp-pane">`); list < 0 || pane < list {
		t.Fatal("the list must come before the conversation")
	}
	style := body[strings.Index(body, "<style>")+7 : strings.Index(body, "</style>")]
	wide := strings.Index(style, "@media (min-width:60rem){")
	if wide < 0 {
		t.Fatal("no wide layout")
	}
	if !strings.Contains(style[:wide], "\n.yp-side{display:none}\n") || strings.Contains(style[:wide], "display:grid;grid-template-columns") {
		t.Error("the narrow baseline must not show the list")
	}
	for _, want := range []string{".yp-split{display:grid;grid-template-columns:18rem minmax(0,1fr)}", ".yp-split .yp-side{display:block;", ".yp-side a{display:flex;align-items:center;gap:.6rem;min-height:48px;", ".yp-side a:focus-visible{outline:3px solid var(--yp-focus)"} {
		if !strings.Contains(style[wide:], want) {
			t.Errorf("wide layout lacks %q", want)
		}
	}
	// Without conversations to list there is neither a list nor a second column.
	for _, p := range []Page{{Peer: "Alex"}, {Peer: "Alex", Threads: []ThreadGroup{{Title: "Leer"}}}} {
		if _, alone := render(t, p); strings.Contains(alone, "<nav") || strings.Contains(alone, `class="yp-chat yp-split"`) || !strings.Contains(alone, `<div class="yp-chat" id="yp-chat"`) || !strings.Contains(alone, `<main class="yp-pane">`) {
			t.Error("a page without conversations to list shows a list")
		}
	}
}
