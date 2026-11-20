package childweb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/childsession"
	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
	"github.com/pblumer/yapuno/internal/messages"
	"github.com/pblumer/yapuno/internal/skin"
)

type chatWorld struct {
	*world
	store        *messages.Store
	chat         string // conversation between Alex and Bea
	alex, bea    *http.Cookie
	cem          *http.Cookie
	alexDeviceID string
}

func newChatWorld(t *testing.T) *chatWorld {
	t.Helper()
	w := newWorld(t, true)
	store, err := messages.Open(filepath.Join(t.TempDir(), "messages.log"), w.contacts, w.pairing, w.families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sessions, err := childsession.New(w.pairing, w.families, time.Now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	w.sessions = sessions
	if w.handler, err = New(sessions, w.families, Options{Contacts: w.contacts, Looks: w.looks, Messages: store}); err != nil {
		t.Fatal(err)
	}
	cw := &chatWorld{world: w, store: store}
	code, err := w.contacts.Invite("guardian-a", "tenant-a", "child-a")
	if err != nil {
		t.Fatal(err)
	}
	if cw.chat, err = w.contacts.Redeem("guardian-b", "tenant-b", "child-b", code); err != nil {
		t.Fatal(err)
	}
	if err := w.contacts.Confirm("guardian-a", "tenant-a", "child-a", cw.chat); err != nil {
		t.Fatal(err)
	}
	signIn := func(guardian, tenant, child string) (*http.Cookie, string) {
		id, key := pairingtest.Pair(t, w.pairing, guardian, tenant, child)
		return w.login(t, id, key), id
	}
	cw.alex, cw.alexDeviceID = signIn("guardian-a", "tenant-a", "child-a")
	cw.bea, _ = signIn("guardian-b", "tenant-b", "child-b")
	cw.cem, _ = signIn("guardian-c", "tenant-c", "child-c")
	return cw
}

var (
	csrfInput     = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	clientIDInput = regexp.MustCompile(`name="client_id" value="([^"]+)"`)
)

func (cw *chatWorld) page(cookie *http.Cookie) *httptest.ResponseRecorder {
	return cw.get("/child/chat/"+cw.chat, cookie)
}

func (cw *chatWorld) csrf(t *testing.T, cookie *http.Cookie) string {
	t.Helper()
	m := csrfInput.FindStringSubmatch(cw.page(cookie).Body.String())
	if m == nil {
		t.Fatal("no CSRF token on chat page")
	}
	return m[1]
}

func (cw *chatWorld) form(cookie *http.Cookie, conversation string, values url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, origin+"/child/chat/"+conversation+"/send", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	cw.handler.ServeHTTP(rec, r)
	return rec
}

type apiOptions struct {
	cookie       *http.Cookie
	csrf         string
	conversation string
	mutate       func(*http.Request)
}

func (cw *chatWorld) apiSend(o apiOptions, payload any) *httptest.ResponseRecorder {
	body, _ := json.Marshal(payload)
	if o.conversation == "" {
		o.conversation = cw.chat
	}
	r := httptest.NewRequest(http.MethodPost, origin+"/child/chat/"+o.conversation+"/messages", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	if o.csrf != "" {
		r.Header.Set("X-CSRF-Token", o.csrf)
	}
	if o.cookie != nil {
		r.AddCookie(o.cookie)
	}
	if o.mutate != nil {
		o.mutate(r)
	}
	rec := httptest.NewRecorder()
	cw.handler.ServeHTTP(rec, r)
	return rec
}

type apiMessage struct {
	Seq  uint64 `json:"seq"`
	Mine bool   `json:"mine"`
	Body string `json:"body"`
	At   string `json:"at"`
}

func (cw *chatWorld) apiHistory(cookie *http.Cookie, conversation, query string) (*httptest.ResponseRecorder, []apiMessage) {
	rec := cw.get("/child/chat/"+conversation+"/messages"+query, cookie)
	var out struct {
		Messages []apiMessage `json:"messages"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out.Messages
}

func clientID(i int) string { return fmt.Sprintf("web-client-id-%06d", i) }

func TestChatPageShowsConversationToItsParticipantsOnly(t *testing.T) {
	cw := newChatWorld(t)
	alexCSRF := cw.csrf(t, cw.alex)
	const hostile = `<script>alert(1)</script><img src=x onerror=alert(2)> & "quoted"`
	for i, m := range []struct {
		cookie *http.Cookie
		csrf   string
		body   string
	}{{cw.alex, alexCSRF, "Hoi Bea!"}, {cw.bea, cw.csrf(t, cw.bea), hostile}, {cw.alex, alexCSRF, "Zeile 1\nZeile 2"}} {
		if rec := cw.apiSend(apiOptions{cookie: m.cookie, csrf: m.csrf}, map[string]string{"client_id": clientID(i), "body": m.body}); rec.Code != http.StatusCreated {
			t.Fatalf("send %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	rec := cw.page(cw.alex)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("chat page: %d", rec.Code)
	}
	for _, want := range []string{
		`<html lang="de">`, "<h1>Bea</h1>", "beiden Familien", `href="/child"`, "Hoi Bea!",
		"&lt;script&gt;alert(1)&lt;/script&gt;&lt;img src=x onerror=alert(2)&gt; &amp; &#34;quoted&#34;",
		`action="/child/chat/` + cw.chat + `/send"`, `name="client_id"`, `<label class="yp-sr" for="yp-body">`, `maxlength="1000"`,
		`data-conversation="` + cw.chat + `"`, `data-last-seq="3"`, `<script src="/child/chat.js" defer></script>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("chat page misses %q", want)
		}
	}
	for _, leak := range []string{"<script>alert", "<img src=x", "tenant-b", "child-b", "guardian-", "Cem", cw.alexDeviceID, cw.alex.Value} {
		if strings.Contains(body, leak) {
			t.Errorf("chat page contains %q", leak)
		}
	}
	// Order and ownership follow the server sequence.
	first, second, third := strings.Index(body, "Hoi Bea!"), strings.Index(body, "&lt;script&gt;"), strings.Index(body, "Zeile 1")
	if !(first < second && second < third) {
		t.Fatal("messages not in sequence order")
	}
	if strings.Count(body, `class="yp-msg yp-mine"`) != 2 || strings.Count(body, `class="yp-msg yp-theirs"`) != 1 {
		t.Fatal("ownership classes wrong for Alex")
	}
	if beaPage := cw.page(cw.bea).Body.String(); strings.Count(beaPage, `class="yp-msg yp-mine"`) != 1 || !strings.Contains(beaPage, "<h1>Alex</h1>") {
		t.Fatal("ownership classes or peer name wrong for Bea")
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("chat page headers: %v", rec.Header())
	}
	if strings.Contains(strings.ToLower(body), "onclick=") || strings.Contains(body, "<script>") {
		t.Fatal("inline script on chat page")
	}
	// Appearance comes from skin tokens only (Alex has no saved look: classic).
	style := body[strings.Index(body, "<style>"):strings.Index(body, "</style>")]
	tokens, err := skin.CSSVariables(skin.Catalog()["classic"])
	if err != nil {
		t.Fatal(err)
	}
	rest := strings.Replace(style, strings.TrimSuffix(strings.TrimPrefix(tokens, ":root{"), "}"), "", 1)
	if hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).FindAllString(rest, -1); len(hex) != 0 || !strings.Contains(body, `data-yp-skin="classic"`) {
		t.Fatalf("hard-coded colors on chat page: %v", hex)
	}
	for _, m := range regexp.MustCompile(`var\((--yp-[a-z-]+)\)`).FindAllStringSubmatch(style, -1) {
		if !strings.Contains(style, m[1]+":") {
			t.Errorf("undeclared token %s", m[1])
		}
	}
	// Everyone else is sent back to the start without any content.
	for name, cookie := range map[string]*http.Cookie{"signed out": nil, "unrelated child": cw.cem} {
		other := cw.page(cookie)
		if other.Code != http.StatusSeeOther || other.Header().Get("Location") != "/child" || strings.Contains(other.Body.String(), "Hoi Bea") {
			t.Fatalf("%s: %d", name, other.Code)
		}
	}
	for _, bad := range []string{"unknown", strings.Repeat("0", 32), "..%2F..%2Fetc", cw.chat + "0"} {
		if rec := cw.get("/child/chat/"+bad, cw.alex); rec.Code == http.StatusOK {
			t.Fatalf("conversation %q served", bad)
		}
	}
}

func TestHomeLinksCrewToChatsOnlyWhenMessagingIsEnabled(t *testing.T) {
	cw := newChatWorld(t)
	if home := cw.get("/child", cw.alex).Body.String(); !strings.Contains(home, `href="/child/chat/`+cw.chat+`"`) {
		t.Fatal("crew entry does not link to the chat")
	}
	plain := newWorld(t, true)
	plain.connect(t)
	id, key := pairingtest.Pair(t, plain.pairing, "guardian-a", "tenant-a", "child-a")
	if home := plain.get("/child", plain.login(t, id, key)).Body.String(); strings.Contains(home, "/child/chat/") {
		t.Fatal("chat link without message store")
	}
	if rec := plain.get("/child/chat/"+strings.Repeat("a", 32), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("chat route without message store: %d", rec.Code)
	}
}

func TestFormSendWorksWithoutJavaScript(t *testing.T) {
	cw := newChatWorld(t)
	page := cw.page(cw.alex).Body.String()
	csrf := csrfInput.FindStringSubmatch(page)[1]
	cid := clientIDInput.FindStringSubmatch(page)
	if cid == nil || len(cid[1]) < 16 {
		t.Fatal("no usable client id on the form")
	}
	if again := clientIDInput.FindStringSubmatch(cw.page(cw.alex).Body.String()); again[1] == cid[1] {
		t.Fatal("client id is not fresh per page")
	}
	valid := url.Values{"csrf_token": {csrf}, "client_id": {cid[1]}, "body": {"Hoi ohne Skript"}}
	for i := 0; i < 2; i++ { // a double submit stores one message
		rec := cw.form(cw.alex, cw.chat, valid)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/child/chat/"+cw.chat {
			t.Fatalf("submit %d: %d %q", i, rec.Code, rec.Header().Get("Location"))
		}
	}
	if got := cw.page(cw.bea).Body.String(); strings.Count(got, "Hoi ohne Skript") != 1 {
		t.Fatal("double submit stored two messages")
	}
	cases := map[string]struct {
		mutate func(url.Values)
		want   int
	}{
		"wrong csrf":        {func(v url.Values) { v.Set("csrf_token", cw.csrf(t, cw.bea)) }, http.StatusForbidden},
		"missing csrf":      {func(v url.Values) { v.Del("csrf_token") }, http.StatusBadRequest},
		"duplicate body":    {func(v url.Values) { v.Add("body", "zweites") }, http.StatusBadRequest},
		"missing client id": {func(v url.Values) { v.Del("client_id") }, http.StatusBadRequest},
		"blank body":        {func(v url.Values) { v.Set("body", "  \n "); v.Set("client_id", clientID(900)) }, http.StatusBadRequest},
		"too long":          {func(v url.Values) { v.Set("body", strings.Repeat("ä", 1001)); v.Set("client_id", clientID(901)) }, http.StatusBadRequest},
		"reused client id":  {func(v url.Values) { v.Set("body", "anderer Text") }, http.StatusConflict},
	}
	for name, c := range cases {
		v := url.Values{"csrf_token": {csrf}, "client_id": {cid[1]}, "body": {"Hoi ohne Skript"}}
		c.mutate(v)
		rec := cw.form(cw.alex, cw.chat, v)
		if rec.Code != c.want {
			t.Errorf("%s: got %d, want %d", name, rec.Code, c.want)
		}
	}
	// A rejected text comes back in the field with an explanation.
	long := strings.Repeat("ä", 1001)
	rejected := cw.form(cw.alex, cw.chat, url.Values{"csrf_token": {csrf}, "client_id": {clientID(902)}, "body": {long}})
	if !strings.Contains(rejected.Body.String(), `role="alert"`) || !strings.Contains(rejected.Body.String(), long) {
		t.Fatal("rejected draft not returned with an explanation")
	}
	if rec := cw.form(nil, cw.chat, valid); rec.Code != http.StatusUnauthorized {
		t.Fatalf("form without session: %d", rec.Code)
	}
	if rec := cw.form(cw.cem, cw.chat, url.Values{"csrf_token": {cw.csrfOf(t, cw.cem)}, "client_id": {clientID(903)}, "body": {"eindringen"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("outsider form send: %d", rec.Code)
	}
	if strings.Contains(cw.page(cw.alex).Body.String(), "eindringen") {
		t.Fatal("outsider message stored")
	}
}

// csrfOf reads a session's CSRF token from the home page, for sessions that
// cannot open the chat page.
func (cw *chatWorld) csrfOf(t *testing.T, cookie *http.Cookie) string {
	t.Helper()
	m := csrfInput.FindStringSubmatch(cw.get("/child", cookie).Body.String())
	if m == nil {
		t.Fatal("no CSRF token on home page")
	}
	return m[1]
}

func TestJSONSendIsAuthorizedIdempotentAndBounded(t *testing.T) {
	cw := newChatWorld(t)
	alex := apiOptions{cookie: cw.alex, csrf: cw.csrf(t, cw.alex)}
	type sent struct {
		Seq       uint64 `json:"seq"`
		At        string `json:"at"`
		Duplicate bool   `json:"duplicate"`
	}
	decode := func(rec *httptest.ResponseRecorder) sent {
		var s sent
		if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
			t.Fatalf("response %q: %v", rec.Body.String(), err)
		}
		return s
	}
	first := cw.apiSend(alex, map[string]string{"client_id": clientID(1), "body": "eins"})
	if first.Code != http.StatusCreated || decode(first).Seq != 1 || decode(first).Duplicate || decode(first).At == "" {
		t.Fatalf("first send: %d %s", first.Code, first.Body.String())
	}
	retry := cw.apiSend(alex, map[string]string{"client_id": clientID(1), "body": "eins"})
	if retry.Code != http.StatusOK || decode(retry).Seq != 1 || !decode(retry).Duplicate {
		t.Fatalf("retry: %d %s", retry.Code, retry.Body.String())
	}
	if rec := cw.apiSend(alex, map[string]string{"client_id": clientID(1), "body": "anders"}); rec.Code != http.StatusConflict {
		t.Fatalf("conflict: %d", rec.Code)
	}
	denied := map[string]struct {
		o       apiOptions
		payload any
		want    int
	}{
		"signed out":             {apiOptions{csrf: alex.csrf}, map[string]string{"client_id": clientID(2), "body": "x"}, http.StatusUnauthorized},
		"no csrf header":         {apiOptions{cookie: cw.alex}, map[string]string{"client_id": clientID(2), "body": "x"}, http.StatusForbidden},
		"other session's csrf":   {apiOptions{cookie: cw.alex, csrf: cw.csrf(t, cw.bea)}, map[string]string{"client_id": clientID(2), "body": "x"}, http.StatusForbidden},
		"outsider":               {apiOptions{cookie: cw.cem, csrf: cw.csrfOf(t, cw.cem)}, map[string]string{"client_id": clientID(2), "body": "x"}, http.StatusForbidden},
		"unknown conversation":   {apiOptions{cookie: cw.alex, csrf: alex.csrf, conversation: strings.Repeat("0", 32)}, map[string]string{"client_id": clientID(2), "body": "x"}, http.StatusForbidden},
		"cross origin":           {apiOptions{cookie: cw.alex, csrf: alex.csrf, mutate: func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }}, map[string]string{"client_id": clientID(2), "body": "x"}, http.StatusForbidden},
		"no origin":              {apiOptions{cookie: cw.alex, csrf: alex.csrf, mutate: func(r *http.Request) { r.Header.Del("Origin") }}, map[string]string{"client_id": clientID(2), "body": "x"}, http.StatusForbidden},
		"form content type":      {apiOptions{cookie: cw.alex, csrf: alex.csrf, mutate: func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }}, map[string]string{"client_id": clientID(2), "body": "x"}, http.StatusUnsupportedMediaType},
		"smuggled sender fields": {alex, map[string]string{"client_id": clientID(2), "body": "x", "from": "child-b", "tenant_id": "tenant-b"}, http.StatusBadRequest},
		"blank body":             {alex, map[string]string{"client_id": clientID(2), "body": "   "}, http.StatusBadRequest},
		"too long":               {alex, map[string]string{"client_id": clientID(2), "body": strings.Repeat("ä", 1001)}, http.StatusBadRequest},
		"bad client id":          {alex, map[string]string{"client_id": "short", "body": "x"}, http.StatusBadRequest},
		"oversized request":      {alex, map[string]string{"client_id": clientID(2), "body": strings.Repeat("x", 20000)}, http.StatusBadRequest},
	}
	for name, c := range denied {
		if rec := cw.apiSend(c.o, c.payload); rec.Code != c.want {
			t.Errorf("%s: got %d, want %d (%s)", name, rec.Code, c.want, rec.Body.String())
		}
	}
	_, history := cw.apiHistory(cw.bea, cw.chat, "")
	if len(history) != 1 || history[0].Body != "eins" {
		t.Fatalf("denied sends changed the conversation: %+v", history)
	}
	for i := 0; i < messages.MaxPerMinute-1; i++ {
		if rec := cw.apiSend(alex, map[string]string{"client_id": clientID(100 + i), "body": "n"}); rec.Code != http.StatusCreated {
			t.Fatalf("send %d: %d", i, rec.Code)
		}
	}
	limited := cw.apiSend(alex, map[string]string{"client_id": clientID(999), "body": "zu viel"})
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limit: %d", limited.Code)
	}
}

func TestJSONHistoryIsPerReaderAndLeaksNoIdentifiers(t *testing.T) {
	cw := newChatWorld(t)
	alexCSRF, beaCSRF := cw.csrf(t, cw.alex), cw.csrf(t, cw.bea)
	cw.apiSend(apiOptions{cookie: cw.alex, csrf: alexCSRF}, map[string]string{"client_id": clientID(1), "body": "von Alex"})
	cw.apiSend(apiOptions{cookie: cw.bea, csrf: beaCSRF}, map[string]string{"client_id": clientID(2), "body": "von Bea"})
	cw.apiSend(apiOptions{cookie: cw.alex, csrf: alexCSRF}, map[string]string{"client_id": clientID(3), "body": "nochmal Alex"})
	rec, forAlex := cw.apiHistory(cw.alex, cw.chat, "")
	if rec.Code != http.StatusOK || len(forAlex) != 3 || !forAlex[0].Mine || forAlex[1].Mine || !forAlex[2].Mine || forAlex[2].Seq != 3 {
		t.Fatalf("history for Alex: %d %+v", rec.Code, forAlex)
	}
	if _, err := time.Parse(time.RFC3339Nano, forAlex[0].At); err != nil {
		t.Fatalf("timestamp %q: %v", forAlex[0].At, err)
	}
	for _, leak := range []string{"tenant", "child-", "client", "web-client-id", "device", "guardian"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("history response contains %q: %s", leak, rec.Body.String())
		}
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("history headers: %v", rec.Header())
	}
	_, forBea := cw.apiHistory(cw.bea, cw.chat, "?after=1")
	if len(forBea) != 2 || !forBea[0].Mine || forBea[1].Mine || forBea[0].Body != "von Bea" {
		t.Fatalf("history for Bea after cursor: %+v", forBea)
	}
	for name, c := range map[string]struct {
		cookie *http.Cookie
		query  string
		want   int
	}{
		"signed out":       {nil, "", http.StatusUnauthorized},
		"outsider":         {cw.cem, "", http.StatusForbidden},
		"negative cursor":  {cw.alex, "?after=-1", http.StatusBadRequest},
		"non-numeric":      {cw.alex, "?after=abc", http.StatusBadRequest},
		"duplicate cursor": {cw.alex, "?after=1&after=2", http.StatusBadRequest},
	} {
		if rec, got := cw.apiHistory(c.cookie, cw.chat, c.query); rec.Code != c.want || len(got) != 0 {
			t.Errorf("%s: %d %+v", name, rec.Code, got)
		}
	}
}

func TestChatAccessEndsWithDeviceOrContact(t *testing.T) {
	cw := newChatWorld(t)
	alexCSRF := cw.csrf(t, cw.alex)
	cw.apiSend(apiOptions{cookie: cw.alex, csrf: alexCSRF}, map[string]string{"client_id": clientID(1), "body": "vorher"})
	if err := cw.pairing.Revoke("guardian-a", "tenant-a", "child-a", cw.alexDeviceID); err != nil {
		t.Fatal(err)
	}
	if rec, got := cw.apiHistory(cw.alex, cw.chat, ""); rec.Code != http.StatusUnauthorized || len(got) != 0 {
		t.Fatalf("revoked device read: %d", rec.Code)
	}
	if rec := cw.apiSend(apiOptions{cookie: cw.alex, csrf: alexCSRF}, map[string]string{"client_id": clientID(2), "body": "x"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked device sent: %d", rec.Code)
	}
	if rec := cw.page(cw.alex); rec.Code != http.StatusSeeOther {
		t.Fatalf("revoked device opened chat page: %d", rec.Code)
	}
	// Bea still reads until the contact itself ends.
	if _, got := cw.apiHistory(cw.bea, cw.chat, ""); len(got) != 1 {
		t.Fatalf("Bea lost access too early: %+v", got)
	}
	if err := cw.contacts.Remove("guardian-a", "tenant-a", "child-a", cw.chat); err != nil {
		t.Fatal(err)
	}
	if rec, got := cw.apiHistory(cw.bea, cw.chat, ""); rec.Code != http.StatusForbidden || len(got) != 0 {
		t.Fatalf("read after contact removal: %d", rec.Code)
	}
	if rec := cw.page(cw.bea); rec.Code != http.StatusSeeOther || strings.Contains(rec.Body.String(), "vorher") {
		t.Fatalf("chat page after contact removal: %d", rec.Code)
	}
}

func TestChatAssetsAreServed(t *testing.T) {
	cw := newChatWorld(t)
	for _, asset := range []string{"/child/chat.js", "/child/chat-utils.js"} {
		rec := cw.get(asset, nil)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
			t.Fatalf("%s: %d", asset, rec.Code)
		}
		for _, risky := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval("} {
			if strings.Contains(rec.Body.String(), risky) {
				t.Errorf("%s uses %s", asset, risky)
			}
		}
	}
}
