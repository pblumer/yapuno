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
