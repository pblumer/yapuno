package childweb

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/childsession"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/messages"
)

const familyThread = "0123456789abcdef0123456789abcdef"

// familyStub gives child-a one conversation with a guardian and keeps its
// messages in memory; everything else goes to the children's store.
type familyStub struct {
	Messages
	mu     sync.Mutex
	stored []messages.Message
}

func (f *familyStub) FamilyThreads(s childsession.Session) []FamilyThread {
	if s.TenantID != "tenant-a" || s.ChildID != "child-a" {
		return nil
	}
	return []FamilyThread{{ID: familyThread, Name: "Mama", Guardian: appearance.Subject{Kind: appearance.KindGuardian, TenantID: "tenant-a", ID: "guardian-a"}}}
}

func (f *familyStub) mine(p messages.Principal, id string) bool {
	return id == familyThread && p.Party == (contacts.Party{TenantID: "tenant-a", ChildID: "child-a"})
}

func (f *familyStub) Send(p messages.Principal, id, clientID, body string) (messages.Message, bool, error) {
	if id != familyThread {
		return f.Messages.Send(p, id, clientID, body)
	}
	if !f.mine(p, id) {
		return messages.Message{}, false, messages.ErrForbidden
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m := messages.Message{Seq: uint64(len(f.stored) + 1), From: p.Party, ClientID: clientID, Body: body, At: time.Now()}
	f.stored = append(f.stored, m)
	return m, false, nil
}

func (f *familyStub) Recent(p messages.Principal, id string, limit int) ([]messages.Message, error) {
	if id != familyThread {
		return f.Messages.Recent(p, id, limit)
	}
	if !f.mine(p, id) {
		return nil, messages.ErrForbidden
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]messages.Message(nil), f.stored...), nil
}

func (f *familyStub) History(p messages.Principal, id string, after uint64, limit int) ([]messages.Message, error) {
	if id != familyThread {
		return f.Messages.History(p, id, after, limit)
	}
	return f.Recent(p, id, limit)
}

func newFamilyWorld(t *testing.T) (*chatWorld, *familyStub) {
	t.Helper()
	cw := newChatWorld(t)
	stub := &familyStub{Messages: cw.store}
	// A guardian wrote first.
	stub.stored = append(stub.stored, messages.Message{Seq: 1, From: contacts.Party{TenantID: "tenant-a"}, ClientID: "guardian-client-0001", Body: "Znacht isch parat", At: time.Now()})
	var err error
	if cw.handler, err = New(cw.sessions, cw.families, Options{Contacts: cw.contacts, Looks: cw.looks, Messages: stub, Family: stub}); err != nil {
		t.Fatal(err)
	}
	return cw, stub
}

func TestTheStartPageListsTheChildsOwnGuardians(t *testing.T) {
	cw, _ := newFamilyWorld(t)
	page := cw.get("/child", cw.alex).Body.String()
	for _, want := range []string{"<h2>Deine Eltern</h2>", `href="/child/chat/` + familyThread + `"`, "<span>Mama</span>", "<h2>Deine Crew</h2>", "Bea"} {
		if !strings.Contains(page, want) {
			t.Errorf("start page lacks %q", want)
		}
	}
	// The guardian's figure is shown, never the guardian's ids.
	if !strings.Contains(page[strings.Index(page, "Deine Eltern"):strings.Index(page, "Deine Crew")], "<svg") || strings.Contains(page, "guardian-a") {
		t.Error("guardian figure missing or guardian id exposed")
	}
	// A child without such a conversation sees no empty section.
	if other := cw.get("/child", cw.bea).Body.String(); strings.Contains(other, "Deine Eltern") || strings.Contains(other, familyThread) {
		t.Error("another child sees the section or the thread")
	}
}

func TestAChildTalksToItsGuardianOnTheSameChatPage(t *testing.T) {
	cw, stub := newFamilyWorld(t)
	rec := cw.get("/child/chat/"+familyThread, cw.alex)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("page: %d", rec.Code)
	}
	for _, want := range []string{"<h1>Mama</h1>", "Nur du und dein Elternteil", "Znacht isch parat", `action="/child/chat/` + familyThread + `/send"`,
		`data-base="/child/chat/` + familyThread + `/messages"`, `data-home="/child"`, `<script src="/child/chat.js" defer></script>`, "yp-theirs"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// Nothing about approval by two families, and no way to block or report a guardian here.
	for _, absent := range []string{"beiden Familien", `class="yp-help"`, "/safety", "Melden", "Blockieren"} {
		if strings.Contains(body, absent) {
			t.Errorf("page contains %q", absent)
		}
	}
	csrf := csrfInput.FindStringSubmatch(body)[1]
	sent := cw.form(cw.alex, familyThread, url.Values{"csrf_token": {csrf}, "client_id": {clientID(1)}, "body": {"Chume grad"}})
	if sent.Code != http.StatusSeeOther || sent.Header().Get("Location") != "/child/chat/"+familyThread {
		t.Fatalf("send: %d %s", sent.Code, sent.Body)
	}
	if len(stub.stored) != 2 || stub.stored[1].Body != "Chume grad" {
		t.Fatalf("stored: %+v", stub.stored)
	}
	after := cw.get("/child/chat/"+familyThread, cw.alex).Body.String()
	if !strings.Contains(after, "Chume grad") || strings.Count(after, "yp-mine") < 1 {
		t.Fatal("own message not shown as own")
	}
	// The scripted path works for it as well.
	if rec, got := cw.apiHistory(cw.alex, familyThread, "?after=0"); rec.Code != http.StatusOK || len(got) != 2 || got[0].Mine || !got[1].Mine {
		t.Fatalf("history: %d %+v", rec.Code, got)
	}
}

func TestOnlyTheChildItselfReachesItsFamilyConversation(t *testing.T) {
	cw, stub := newFamilyWorld(t)
	for name, cookie := range map[string]*http.Cookie{"another child": cw.bea, "nobody": nil} {
		if rec := cw.get("/child/chat/"+familyThread, cookie); rec.Code != http.StatusSeeOther || strings.Contains(rec.Body.String(), "Znacht") {
			t.Errorf("%s: page %d", name, rec.Code)
		}
	}
	csrf := cw.csrf(t, cw.bea)
	if rec := cw.form(cw.bea, familyThread, url.Values{"csrf_token": {csrf}, "client_id": {clientID(2)}, "body": {"hoi"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("foreign send: %d", rec.Code)
	}
	if rec, _ := cw.apiHistory(cw.bea, familyThread, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("foreign history: %d", rec.Code)
	}
	if len(stub.stored) != 1 {
		t.Fatal("a foreign message was stored")
	}
}

func TestAGuardianConversationCannotBeBlockedOrReported(t *testing.T) {
	cw, _ := newFamilyWorld(t)
	csrf := cw.csrf(t, cw.alex)
	if rec := cw.get("/child/chat/"+familyThread+"/safety", cw.alex); rec.Code == http.StatusOK {
		t.Fatalf("safety page for a guardian: %d", rec.Code)
	}
	r := func(path string) int {
		req := cw.post2(path, url.Values{"csrf_token": {csrf}, "confirm": {"yes"}}, cw.alex)
		return req.Code
	}
	if code := r("/child/chat/" + familyThread + "/block"); code == http.StatusSeeOther || code == http.StatusOK {
		t.Fatalf("block of a guardian: %d", code)
	}
	if len(cw.contacts.BlockedBy(contacts.Party{TenantID: "tenant-a", ChildID: "child-a"})) != 0 {
		t.Fatal("something was blocked")
	}
	// The conversation is still there.
	if rec := cw.get("/child/chat/"+familyThread, cw.alex); rec.Code != http.StatusOK {
		t.Fatalf("conversation after the attempt: %d", rec.Code)
	}
}

func TestFamilyConversationsNeedMessagingButNoContactStore(t *testing.T) {
	cw, stub := newFamilyWorld(t)
	if _, err := New(cw.sessions, cw.families, Options{Family: stub}); err == nil {
		t.Error("family conversations accepted without a message store")
	}
	h, err := New(cw.sessions, cw.families, Options{Messages: stub, Family: stub})
	if err != nil {
		t.Fatalf("family conversations alone: %v", err)
	}
	cw.handler = h
	page := cw.get("/child", cw.alex).Body.String()
	if !strings.Contains(page, "Deine Eltern") || !strings.Contains(page, "Mama") {
		t.Fatal("start page without contacts lacks the guardians")
	}
	if rec := cw.get("/child/chat/"+familyThread, cw.alex); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<h1>Mama</h1>") {
		t.Fatalf("chat without contacts: %d", rec.Code)
	}
	if _, err := New(cw.sessions, cw.families, Options{Messages: cw.store}); err == nil {
		t.Error("children's messaging accepted without the contact store")
	}
}

// wrongSubject offers a conversation but names a child as the guardian.
type wrongSubject struct{ *familyStub }

func (wrongSubject) FamilyThreads(childsession.Session) []FamilyThread {
	return []FamilyThread{{ID: familyThread, Name: "Mama", Guardian: appearance.Subject{Kind: appearance.KindChild, TenantID: "tenant-b", ID: "child-b"}}}
}

func TestOnlyAGuardiansFigureIsShownForAGuardian(t *testing.T) {
	cw, stub := newFamilyWorld(t)
	var err error
	if cw.handler, err = New(cw.sessions, cw.families, Options{Contacts: cw.contacts, Looks: cw.looks, Messages: stub, Family: wrongSubject{stub}}); err != nil {
		t.Fatal(err)
	}
	page := cw.get("/child", cw.alex).Body.String()
	section := page[strings.Index(page, "<h2>Deine Eltern</h2>"):strings.Index(page, "<h2>Deine Crew</h2>")]
	if strings.Contains(section, "<svg") || !strings.Contains(section, "Mama") {
		t.Fatalf("a child's figure was shown for a guardian: %s", section)
	}
}

// post2 posts a form to any child path.
func (cw *chatWorld) post2(path string, values url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, origin+path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	cw.handler.ServeHTTP(rec, r)
	return rec
}

// webkit is what Safari and every browser on an iPhone send for scripted
// requests from these pages: they set Referrer-Policy no-referrer, so the
// Origin header is "null" and only fetch metadata names the source.
func webkit(r *http.Request) {
	r.Header.Set("Origin", "null")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
}

func TestChildSignInAndChatAcceptBrowsersThatSendANullOrigin(t *testing.T) {
	cw := newChatWorld(t)
	// Sign-in: the challenge is the first scripted request of a paired device.
	if rec := cw.post("/child/session/challenge", map[string]string{"device_id": cw.alexDeviceID}, webkit); rec.Code != http.StatusOK {
		t.Fatalf("challenge from an iPhone refused: %d %s", rec.Code, rec.Body)
	}
	bare := func(r *http.Request) { r.Header.Set("Origin", "null") }
	if rec := cw.post("/child/session/challenge", map[string]string{"device_id": cw.alexDeviceID}, bare); rec.Code != http.StatusForbidden {
		t.Fatalf("challenge with a bare null origin: %d", rec.Code)
	}
	crossSite := func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }
	if rec := cw.post("/child/session/challenge", map[string]string{"device_id": cw.alexDeviceID}, crossSite); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site challenge with the right origin: %d", rec.Code)
	}
	// Chat: the scripted send.
	csrf := cw.csrf(t, cw.alex)
	payload := map[string]string{"client_id": clientID(900), "body": "Hoi vom iPhone"}
	if rec := cw.apiSend(apiOptions{cookie: cw.alex, csrf: csrf, mutate: webkit}, payload); rec.Code != http.StatusCreated {
		t.Fatalf("send from an iPhone refused: %d %s", rec.Code, rec.Body)
	}
	if rec := cw.apiSend(apiOptions{cookie: cw.alex, csrf: csrf, mutate: bare}, map[string]string{"client_id": clientID(901), "body": "x"}); rec.Code != http.StatusForbidden {
		t.Fatalf("send with a bare null origin: %d", rec.Code)
	}
	if rec := cw.apiSend(apiOptions{cookie: cw.alex, csrf: csrf, mutate: crossSite}, map[string]string{"client_id": clientID(902), "body": "x"}); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site send with the right origin: %d", rec.Code)
	}
	// The session token is still required: metadata is no substitute for it.
	if rec := cw.apiSend(apiOptions{cookie: cw.alex, mutate: webkit}, map[string]string{"client_id": clientID(903), "body": "x"}); rec.Code != http.StatusForbidden {
		t.Fatalf("send without CSRF token: %d", rec.Code)
	}
}

// sideList returns the list of conversations beside the open one.
func sideList(t *testing.T, body string) string {
	t.Helper()
	from, to := strings.Index(body, `<nav class="yp-side"`), strings.Index(body, "</nav>")
	if from < 0 || to < from {
		t.Fatal("no list of conversations")
	}
	return body[from:to]
}

func TestAConversationListsTheChildsOwnChatsBesideIt(t *testing.T) {
	cw, _ := newFamilyWorld(t)
	list := sideList(t, cw.get("/child/chat/"+cw.chat, cw.alex).Body.String())
	for _, want := range []string{"Deine Eltern", `<a href="/child/chat/` + familyThread + `">`, "Mama", "Deine Crew", `<a href="/child/chat/` + cw.chat + `" aria-current="page">`, "Bea"} {
		if !strings.Contains(list, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	for _, absent := range []string{"child-b", "tenant-b", "guardian-a", "Cem"} {
		if strings.Contains(list, absent) {
			t.Errorf("list contains %q", absent)
		}
	}
	if own := sideList(t, cw.get("/child/chat/"+familyThread, cw.alex).Body.String()); !strings.Contains(own, `<a href="/child/chat/`+familyThread+`" aria-current="page">`) || strings.Count(own, "aria-current") != 1 {
		t.Errorf("the open conversation is not the marked one: %s", own)
	}
	// Bea sees Alex and nothing of Alex's family.
	other := sideList(t, cw.get("/child/chat/"+cw.chat, cw.bea).Body.String())
	if !strings.Contains(other, "Alex") || !strings.Contains(other, `<a href="/child/chat/`+cw.chat+`" aria-current="page">`) {
		t.Errorf("Bea's list: %s", other)
	}
	for _, absent := range []string{"Mama", "Deine Eltern", familyThread} {
		if strings.Contains(other, absent) {
			t.Errorf("Bea's list contains %q", absent)
		}
	}
	// The list follows the contact: it is read again for every page.
	if err := cw.contacts.Remove("guardian-a", "tenant-a", "child-a", cw.chat); err != nil {
		t.Fatal(err)
	}
	if after := sideList(t, cw.get("/child/chat/"+familyThread, cw.alex).Body.String()); strings.Contains(after, "Bea") || strings.Contains(after, "Deine Crew") || !strings.Contains(after, "Mama") {
		t.Errorf("list does not follow the contact: %s", after)
	}
}
