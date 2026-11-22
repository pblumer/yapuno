package familyweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/guardianchat"
)

const chatOrigin = "https://app.example.org"

type everyoneActive struct{}

func (everyoneActive) ActiveGuardian(context.Context, string, string) bool { return true }

type noDevices struct{}

func (noDevices) ActiveDevice(string, string, string) (devicepairing.Device, bool) {
	return devicepairing.Device{}, false
}

type chatWorld struct {
	*contactWorld
	chat *guardianchat.Store
	link string
}

// newChatWorld connects Alex (family a) and Bea (family b); family c stays apart.
func newChatWorld(t *testing.T) *chatWorld {
	t.Helper()
	cw := newContactWorld(t)
	code, err := cw.store.Invite("guardian-a", "tenant-a", "child-a")
	if err != nil {
		t.Fatal(err)
	}
	link, err := cw.store.Redeem("guardian-b", "tenant-b", "child-b", code)
	if err != nil {
		t.Fatal(err)
	}
	if err := cw.store.Confirm("guardian-a", "tenant-a", "child-a", link); err != nil {
		t.Fatal(err)
	}
	chat, err := guardianchat.Open(filepath.Join(t.TempDir(), "guardian-chat.log"), cw.families, cw.store, everyoneActive{}, noDevices{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = chat.Close() })
	return &chatWorld{contactWorld: cw, chat: chat, link: link}
}

func (cw *chatWorld) pages(t *testing.T, auth testAuth) http.Handler {
	t.Helper()
	h, err := NewChatPages(auth, cw.chat, cw.families, cw.looks)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (cw *chatWorld) thread(t *testing.T, s Session, kind guardianchat.Kind) string {
	t.Helper()
	for _, th := range cw.chat.Threads(guardianchat.Guardian(s.TenantID, s.GuardianID)) {
		if th.Kind == kind {
			return th.ID
		}
	}
	t.Fatalf("%s has no %v thread", s.GuardianID, kind)
	return ""
}

func chatGet(h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, chatOrigin+"/family/chat"+path, nil))
	return w
}

func chatForm(h http.Handler, id string, form url.Values, contentType string) *httptest.ResponseRecorder {
	if contentType == "" {
		contentType = "application/x-www-form-urlencoded"
	}
	r := httptest.NewRequest(http.MethodPost, chatOrigin+"/family/chat/"+id+"/send", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func chatAPI(h http.Handler, id, csrf string, payload any, mutate func(*http.Request)) *httptest.ResponseRecorder {
	body, _ := json.Marshal(payload)
	r := httptest.NewRequest(http.MethodPost, chatOrigin+"/family/chat/"+id+"/messages", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", chatOrigin)
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	if mutate != nil {
		mutate(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func say(body string, n int) url.Values {
	return url.Values{"csrf_token": {sessionA.CSRFToken}, "client_id": {"web-client-id-00000" + string(rune('a'+n))}, "body": {body}}
}

func TestGuardianChatRequiresAuthentication(t *testing.T) {
	cw := newChatWorld(t)
	id := cw.thread(t, sessionA, guardianchat.Family)
	h := cw.pages(t, testAuth{err: errors.New("no session")})
	for _, path := range []string{"", "/" + id, "/" + id + "/messages"} {
		if w := chatGet(h, path); w.Code != http.StatusUnauthorized {
			t.Errorf("GET %q: %d", path, w.Code)
		}
	}
	if w := chatForm(h, id, say("hoi", 0), ""); w.Code != http.StatusUnauthorized {
		t.Errorf("form: %d", w.Code)
	}
	if w := chatAPI(h, id, sessionA.CSRFToken, map[string]string{"client_id": "web-client-id-000001", "body": "hoi"}, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("api: %d", w.Code)
	}
	if got, _ := cw.chat.Recent(guardianchat.Guardian("tenant-a", "guardian-a"), id, 10); len(got) != 0 {
		t.Fatal("a message was stored without a session")
	}
}

func TestTheInboxListsOwnChildrenAndGuardiansOfConnectedChildren(t *testing.T) {
	cw := newChatWorld(t)
	w := chatGet(cw.pages(t, testAuth{session: sessionA}), "")
	body := w.Body.String()
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("inbox: %d %v", w.Code, w.Header())
	}
	// The overview runs no script, so its policy allows none.
	if strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src") {
		t.Error("the overview's policy allows scripts")
	}
	family, parents := cw.thread(t, sessionA, guardianchat.Family), cw.thread(t, sessionA, guardianchat.Parents)
	for _, want := range []string{`<html lang="de">`, "<h1>Chats</h1>", `href="/family"`, "Deine Kinder", `href="/family/chat/` + family + `"`, "Alex",
		"Andere Eltern", `href="/family/chat/` + parents + `"`, "Elternteil von Bea", "nicht lesen", "synthetischen Profilen"} {
		if !strings.Contains(body, want) {
			t.Errorf("inbox lacks %q", want)
		}
	}
	// No identifier of the other family, and no script on the overview.
	for _, absent := range []string{"guardian-b", "tenant-b", "child-b", "<script", "Cem"} {
		if strings.Contains(body, absent) {
			t.Errorf("inbox contains %q", absent)
		}
	}
	style := body[strings.Index(body, "<style>")+7 : strings.Index(body, "</style>")]
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).MatchString(style[strings.Index(style, "body{margin:0}"):]) {
		t.Error("component styles must use skin tokens only")
	}
	// A family without connections sees only its own child and is told why.
	alone := chatGet(cw.pages(t, testAuth{session: sessionC}), "").Body.String()
	if !strings.Contains(alone, "Cem") || strings.Contains(alone, "Elternteil von") || !strings.Contains(alone, "sobald eure Kinder verbunden sind") {
		t.Errorf("inbox without connections: %s", alone)
	}
	// Ending the children's contact removes the other parent.
	if err := cw.store.Remove("guardian-a", "tenant-a", "child-a", cw.link); err != nil {
		t.Fatal(err)
	}
	if after := chatGet(cw.pages(t, testAuth{session: sessionA}), "").Body.String(); strings.Contains(after, "Elternteil von Bea") || !strings.Contains(after, "Alex") {
		t.Error("inbox does not follow the contact")
	}
}

func TestAGuardianWritesToTheirOwnChild(t *testing.T) {
	cw := newChatWorld(t)
	if err := cw.looks.Set(appearance.Subject{Kind: appearance.KindGuardian, TenantID: "tenant-a", ID: "guardian-a"}, appearance.Preference{Skin: "night", Figure: appearance.Default().Figure}); err != nil {
		t.Fatal(err)
	}
	h := cw.pages(t, testAuth{session: sessionA})
	id := cw.thread(t, sessionA, guardianchat.Family)
	page := chatGet(h, "/"+id)
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(page.Header().Get("Content-Security-Policy"), "script-src 'self'") || page.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("page: %d %v", page.Code, page.Header())
	}
	for _, want := range []string{`data-yp-skin="night"`, "<h1>Alex</h1>", "Nur du und dein Kind", `action="/family/chat/` + id + `/send"`, `data-base="/family/chat/` + id + `/messages"`,
		`data-home="/family/chat"`, `href="/family/chat"`, `<script src="/family/chat.js" defer></script>`, `name="csrf_token" value="` + sessionA.CSRFToken + `"`, "<svg"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, `class="yp-help"`) {
		t.Error("a block or report link is offered to a guardian")
	}
	const hostile = `<script>alert(1)</script> & "x"`
	w := chatForm(h, id, say(hostile, 1), "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family/chat/"+id {
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	after := chatGet(h, "/"+id).Body.String()
	if strings.Contains(after, "<script>alert") || !strings.Contains(after, "&lt;script&gt;alert(1)&lt;/script&gt;") || !strings.Contains(after, "yp-mine") {
		t.Fatalf("message not shown safely as own: %s", after)
	}
	got, _ := cw.chat.Recent(guardianchat.Guardian("tenant-a", "guardian-a"), id, 10)
	if len(got) != 1 || got[0].Body != hostile {
		t.Fatalf("stored: %+v", got)
	}
}

func TestTwoGuardiansOfConnectedFamiliesWriteToEachOther(t *testing.T) {
	cw := newChatWorld(t)
	a, b := cw.pages(t, testAuth{session: sessionA}), cw.pages(t, testAuth{session: sessionB})
	id := cw.thread(t, sessionA, guardianchat.Parents)
	if id != cw.thread(t, sessionB, guardianchat.Parents) {
		t.Fatal("the two guardians see different threads")
	}
	if w := chatForm(a, id, say("Hoi zäme", 2), ""); w.Code != http.StatusSeeOther {
		t.Fatalf("send: %d", w.Code)
	}
	page := chatGet(b, "/"+id).Body.String()
	for _, want := range []string{"<h1>Elternteil von Alex</h1>", "Eure Kinder sind verbunden", "Hoi zäme", "yp-theirs"} {
		if !strings.Contains(page, want) {
			t.Errorf("other guardian's page lacks %q", want)
		}
	}
	if strings.Contains(page, "guardian-a") || strings.Contains(page, "tenant-a") {
		t.Error("identifiers of the other family on the page")
	}
	reply := url.Values{"csrf_token": {sessionB.CSRFToken}, "client_id": {"web-client-id-00000z"}, "body": {"Hoi Anna"}}
	if w := chatForm(b, id, reply, ""); w.Code != http.StatusSeeOther {
		t.Fatalf("reply: %d", w.Code)
	}
	if back := chatGet(a, "/"+id).Body.String(); !strings.Contains(back, "Hoi Anna") {
		t.Fatal("reply not shown")
	}
}

func TestForeignAndUnknownThreadsShowNothing(t *testing.T) {
	cw := newChatWorld(t)
	family, parents := cw.thread(t, sessionA, guardianchat.Family), cw.thread(t, sessionA, guardianchat.Parents)
	a := cw.pages(t, testAuth{session: sessionA})
	chatForm(a, family, say("privat", 3), "")
	chatForm(a, parents, say("unter Eltern", 4), "")
	c := cw.pages(t, testAuth{session: sessionC})
	b := cw.pages(t, testAuth{session: sessionB})
	stranger := url.Values{"csrf_token": {sessionC.CSRFToken}, "client_id": {"web-client-id-00000y"}, "body": {"hoi"}}
	for name, id := range map[string]string{"family": family, "parents": parents, "unknown": strings.Repeat("0", 32)} {
		if w := chatGet(c, "/"+id); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/family/chat" || strings.Contains(w.Body.String(), "privat") {
			t.Errorf("%s page: %d", name, w.Code)
		}
		if w := chatForm(c, id, stranger, ""); w.Code != http.StatusForbidden {
			t.Errorf("%s form: %d", name, w.Code)
		}
		if w := chatAPI(c, id, sessionC.CSRFToken, map[string]string{"client_id": "web-client-id-000009", "body": "hoi"}, nil); w.Code != http.StatusForbidden {
			t.Errorf("%s api: %d", name, w.Code)
		}
		if w := chatGet(c, "/"+id+"/messages"); w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "privat") {
			t.Errorf("%s history: %d", name, w.Code)
		}
	}
	// The other parent reaches the parents' thread, not the family one.
	if w := chatGet(b, "/"+family); w.Code != http.StatusSeeOther {
		t.Errorf("other parent on the family thread: %d", w.Code)
	}
	for _, bad := range []string{"x", "ABCDEF0123456789abcdef0123456789", family + "0"} {
		if w := chatGet(a, "/"+bad); w.Code != http.StatusNotFound {
			t.Errorf("malformed id %q: %d", bad, w.Code)
		}
	}
	got, _ := cw.chat.Recent(guardianchat.Guardian("tenant-a", "guardian-a"), family, 10)
	if len(got) != 1 {
		t.Fatalf("a foreign message was stored: %+v", got)
	}
}

func TestTheFormRejectsInvalidSubmissionsAndKeepsTheDraft(t *testing.T) {
	cw := newChatWorld(t)
	h := cw.pages(t, testAuth{session: sessionA})
	id := cw.thread(t, sessionA, guardianchat.Family)
	cases := []struct {
		name, contentType string
		form              url.Values
		want              int
	}{
		{"no csrf", "", url.Values{"client_id": {"web-client-id-000001"}, "body": {"x"}}, http.StatusBadRequest},
		{"wrong csrf", "", url.Values{"csrf_token": {"wrong"}, "client_id": {"web-client-id-000001"}, "body": {"x"}}, http.StatusForbidden},
		{"json", "application/json", say("x", 5), http.StatusUnsupportedMediaType},
		{"no body", "", url.Values{"csrf_token": {sessionA.CSRFToken}, "client_id": {"web-client-id-000001"}}, http.StatusBadRequest},
		{"two bodies", "", url.Values{"csrf_token": {sessionA.CSRFToken}, "client_id": {"web-client-id-000001"}, "body": {"a", "b"}}, http.StatusBadRequest},
		{"no client id", "", url.Values{"csrf_token": {sessionA.CSRFToken}, "body": {"x"}}, http.StatusBadRequest},
	}
	for _, c := range cases {
		if w := chatForm(h, id, c.form, c.contentType); w.Code != c.want {
			t.Errorf("%s: %d, want %d", c.name, w.Code, c.want)
		}
	}
	long := strings.Repeat("ä", 1001)
	w := chatForm(h, id, say(long, 6), "")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `role="alert"`) || !strings.Contains(w.Body.String(), "1000 Zeichen") || !strings.Contains(w.Body.String(), long[:40]) {
		t.Fatalf("too long: %d", w.Code)
	}
	if got, _ := cw.chat.Recent(guardianchat.Guardian("tenant-a", "guardian-a"), id, 10); len(got) != 0 {
		t.Fatalf("an invalid submission was stored: %+v", got)
	}
}

func TestTheScriptedPathSendsIdempotentlyAndReadsAfterACursor(t *testing.T) {
	cw := newChatWorld(t)
	a, b := cw.pages(t, testAuth{session: sessionA}), cw.pages(t, testAuth{session: sessionB})
	id := cw.thread(t, sessionA, guardianchat.Parents)
	payload := map[string]string{"client_id": "web-client-id-000001", "body": "Hoi"}
	first := chatAPI(a, id, sessionA.CSRFToken, payload, nil)
	if first.Code != http.StatusCreated || first.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("send: %d %s", first.Code, first.Body)
	}
	var sent struct {
		Seq       uint64 `json:"seq"`
		At        string `json:"at"`
		Duplicate bool   `json:"duplicate"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &sent)
	if sent.Seq != 1 || sent.Duplicate || sent.At == "" {
		t.Fatalf("answer: %+v", sent)
	}
	if again := chatAPI(a, id, sessionA.CSRFToken, payload, nil); again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"duplicate":true`) {
		t.Fatalf("retry: %d %s", again.Code, again.Body)
	}
	if conflict := chatAPI(a, id, sessionA.CSRFToken, map[string]string{"client_id": "web-client-id-000001", "body": "anders"}, nil); conflict.Code != http.StatusConflict {
		t.Fatalf("conflict: %d", conflict.Code)
	}
	chatAPI(b, id, sessionB.CSRFToken, map[string]string{"client_id": "web-client-id-000002", "body": "Hoi zrugg"}, nil)
	history := chatGet(a, "/"+id+"/messages?after=1")
	var out struct {
		Messages []struct {
			Seq  uint64 `json:"seq"`
			Mine bool   `json:"mine"`
			Body string `json:"body"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(history.Body.Bytes(), &out)
	if history.Code != http.StatusOK || len(out.Messages) != 1 || out.Messages[0].Seq != 2 || out.Messages[0].Mine || out.Messages[0].Body != "Hoi zrugg" {
		t.Fatalf("history: %d %s", history.Code, history.Body)
	}
	if strings.Contains(history.Body.String(), "guardian-") || strings.Contains(history.Body.String(), "tenant-") {
		t.Fatal("identifiers in the history response")
	}
	for name, bad := range map[string]func() *httptest.ResponseRecorder{
		"no csrf":    func() *httptest.ResponseRecorder { return chatAPI(a, id, "", payload, nil) },
		"wrong csrf": func() *httptest.ResponseRecorder { return chatAPI(a, id, "wrong", payload, nil) },
		"other origin": func() *httptest.ResponseRecorder {
			return chatAPI(a, id, sessionA.CSRFToken, payload, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") })
		},
		"no origin": func() *httptest.ResponseRecorder {
			return chatAPI(a, id, sessionA.CSRFToken, payload, func(r *http.Request) { r.Header.Del("Origin") })
		},
		"form body": func() *httptest.ResponseRecorder {
			return chatAPI(a, id, sessionA.CSRFToken, payload, func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") })
		},
		"unknown field": func() *httptest.ResponseRecorder {
			return chatAPI(a, id, sessionA.CSRFToken, map[string]string{"client_id": "web-client-id-000003", "body": "x", "sender": "guardian-b"}, nil)
		},
		"empty text": func() *httptest.ResponseRecorder {
			return chatAPI(a, id, sessionA.CSRFToken, map[string]string{"client_id": "web-client-id-000004", "body": "  "}, nil)
		},
	} {
		if w := bad(); w.Code < 400 || w.Code == http.StatusConflict {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	for _, query := range []string{"?after=x", "?after=1&after=2", "?after=-1"} {
		if w := chatGet(a, "/"+id+"/messages"+query); w.Code != http.StatusBadRequest {
			t.Errorf("history %s: %d", query, w.Code)
		}
	}
	if got, _ := cw.chat.Recent(guardianchat.Guardian("tenant-a", "guardian-a"), id, 10); len(got) != 2 {
		t.Fatalf("stored after bad requests: %d", len(got))
	}
}

func TestGuardianChatServesTheSharedScriptsAndNeedsItsDependencies(t *testing.T) {
	cw := newChatWorld(t)
	h := cw.pages(t, testAuth{session: sessionA})
	for _, asset := range []string{"/family/chat.js", "/family/chat-utils.js"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, chatOrigin+asset, nil))
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Body.Len() == 0 {
			t.Errorf("%s: %d %v", asset, w.Code, w.Header())
		}
	}
	auth := testAuth{session: sessionA}
	if _, err := NewChatPages(nil, cw.chat, cw.families, nil); err == nil {
		t.Error("nil authenticator accepted")
	}
	if _, err := NewChatPages(auth, nil, cw.families, nil); err == nil {
		t.Error("nil chat accepted")
	}
	if _, err := NewChatPages(auth, cw.chat, nil, nil); err == nil {
		t.Error("nil names accepted")
	}
	plain, err := NewChatPages(auth, cw.chat, cw.families, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := chatGet(plain, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `data-yp-skin="classic"`) {
		t.Errorf("without looks: %d", w.Code)
	}
}

func TestGuardianChatAcceptsBrowsersThatSendANullOrigin(t *testing.T) {
	cw := newChatWorld(t)
	h := cw.pages(t, testAuth{session: sessionA})
	id := cw.thread(t, sessionA, guardianchat.Family)
	// Safari and every browser on an iPhone: Origin "null", fetch metadata set.
	webkit := func(r *http.Request) {
		r.Header.Set("Origin", "null")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if w := chatAPI(h, id, sessionA.CSRFToken, map[string]string{"client_id": "web-client-id-000101", "body": "Hoi vom iPhone"}, webkit); w.Code != http.StatusCreated {
		t.Fatalf("send from an iPhone refused: %d %s", w.Code, w.Body)
	}
	for name, mutate := range map[string]func(*http.Request){
		"bare null origin":             func(r *http.Request) { r.Header.Set("Origin", "null") },
		"null origin, cross-site":      func(r *http.Request) { r.Header.Set("Origin", "null"); r.Header.Set("Sec-Fetch-Site", "cross-site") },
		"right origin, cross-site":     func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
		"right origin, same-site only": func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") },
	} {
		if w := chatAPI(h, id, sessionA.CSRFToken, map[string]string{"client_id": "web-client-id-000102", "body": "x"}, mutate); w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if w := chatAPI(h, id, "", map[string]string{"client_id": "web-client-id-000103", "body": "x"}, webkit); w.Code != http.StatusForbidden {
		t.Errorf("without CSRF token: %d", w.Code)
	}
	if got, _ := cw.chat.Recent(guardianchat.Guardian("tenant-a", "guardian-a"), id, 10); len(got) != 1 {
		t.Fatalf("stored: %d", len(got))
	}
}
