package main

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/devicepairing/pairingtest"
)

var (
	guardianThread = regexp.MustCompile(`href="/family/chat/([0-9a-f]{32})"`)
	childThread    = regexp.MustCompile(`href="/child/chat/([0-9a-f]{32})"`)
	clientField    = regexp.MustCompile(`name="client_id" value="([^"]+)"`)
)

type chatServer struct {
	*securityServer
	link string // contact between Alex (family a) and Bea (family b)
}

func newChatServer(t *testing.T, withLocks bool) *chatServer {
	t.Helper()
	s := newSecurityServer(t, withLocks)
	s.chatPath = filepath.Join(s.root, "guardian-chat.log")
	s.start(withLocks)
	code, err := s.contacts.Invite("guardian-a", "tenant-a", "child-a")
	if err != nil {
		t.Fatal(err)
	}
	link, err := s.contacts.Redeem("guardian-b", "tenant-b", "child-b", code)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.contacts.Confirm("guardian-a", "tenant-a", "child-a", link); err != nil {
		t.Fatal(err)
	}
	return &chatServer{securityServer: s, link: link}
}

// threads returns the ids of the section of the guardian's inbox whose
// heading is given, in page order.
func (s *chatServer) threads(cookie *http.Cookie, heading string) []string {
	s.t.Helper()
	page := s.do(http.MethodGet, "/family/chat", cookie, nil).Body.String()
	start := strings.Index(page, heading)
	if start < 0 {
		s.t.Fatalf("inbox has no section %q", heading)
	}
	section := page[start:]
	if end := strings.Index(section, "</section>"); end >= 0 {
		section = section[:end]
	}
	var ids []string
	for _, m := range guardianThread.FindAllStringSubmatch(section, -1) {
		ids = append(ids, m[1])
	}
	return ids
}

// write sends one message through the real form of a chat page.
func (s *chatServer) write(base string, cookie *http.Cookie, id, body string) int {
	s.t.Helper()
	page := s.do(http.MethodGet, base+"/"+id, cookie, nil)
	csrf, client := csrfField.FindStringSubmatch(page.Body.String()), clientField.FindStringSubmatch(page.Body.String())
	if page.Code != http.StatusOK || csrf == nil || client == nil {
		return page.Code
	}
	return s.do(http.MethodPost, base+"/"+id+"/send", cookie, url.Values{"csrf_token": {csrf[1]}, "client_id": {client[1]}, "body": {body}}).Code
}

func (s *chatServer) read(base string, cookie *http.Cookie, id string) string {
	w := s.do(http.MethodGet, base+"/"+id, cookie, nil)
	if w.Code != http.StatusOK {
		return ""
	}
	return w.Body.String()
}

func (s *chatServer) child(guardian, tenant, child string) *http.Cookie {
	s.t.Helper()
	id, key := pairingtest.Pair(s.t, s.pairing, guardian, tenant, child)
	return childLogin(s.t, s.handler, id, key)
}

func TestGuardiansChildrenAndOtherParentsChatEndToEnd(t *testing.T) {
	s := newChatServer(t, false)
	anna, ben, bruno := s.issue("guardian-a", "tenant-a"), s.issue("guardian-a2", "tenant-a"), s.issue("guardian-b", "tenant-b")
	overview := s.do(http.MethodGet, "/family", anna, nil).Body.String()
	if !strings.Contains(overview, `href="/family/chat"`) || !strings.Contains(overview, "Mit Alex schreiben") || strings.Contains(overview, "Preview only") {
		t.Fatal("family overview does not lead to the chats")
	}
	own, others := s.threads(anna, "Deine Kinder"), s.threads(anna, "Andere Eltern")
	if len(own) != 1 || len(others) != 1 {
		t.Fatalf("anna's inbox: %v %v", own, others)
	}

	// Guardian and own child.
	if code := s.write("/family/chat", anna, own[0], "Znacht isch parat"); code != http.StatusSeeOther {
		t.Fatalf("guardian writes to child: %d", code)
	}
	alex := s.child("guardian-a", "tenant-a", "child-a")
	home := s.do(http.MethodGet, "/child", alex, nil).Body.String()
	parents := childThread.FindAllStringSubmatch(home[strings.Index(home, "<h2>Deine Eltern</h2>"):strings.Index(home, "<h2>Deine Crew</h2>")], -1)
	if len(parents) != 2 || !strings.Contains(home, "Elternteil 1") || !strings.Contains(home, "Elternteil 2") {
		t.Fatalf("child start page lists %d guardians", len(parents))
	}
	var withAnna string
	for _, m := range parents {
		if strings.Contains(s.read("/child/chat", alex, m[1]), "Znacht isch parat") {
			withAnna = m[1]
		}
	}
	if withAnna == "" || withAnna != own[0] {
		t.Fatal("the child does not see the guardian's message in the same conversation")
	}
	if code := s.write("/child/chat", alex, withAnna, "Chume grad"); code != http.StatusSeeOther {
		t.Fatalf("child answers: %d", code)
	}
	if page := s.read("/family/chat", anna, own[0]); !strings.Contains(page, "Chume grad") || !strings.Contains(page, "<h1>Alex</h1>") {
		t.Fatal("guardian does not see the child's answer")
	}
	// The second guardian of the same child has a conversation of their own
	// and does not read this one; neither does anybody from another family.
	benOwn := s.threads(ben, "Deine Kinder")
	if len(benOwn) != 1 || benOwn[0] == own[0] || strings.Contains(s.read("/family/chat", ben, benOwn[0]), "Znacht") {
		t.Fatal("the second guardian shares the first guardian's conversation")
	}
	for name, cookie := range map[string]*http.Cookie{"second guardian": ben, "other family": bruno} {
		if w := s.do(http.MethodGet, "/family/chat/"+own[0], cookie, nil); w.Code != http.StatusSeeOther || strings.Contains(w.Body.String(), "Znacht") {
			t.Errorf("%s opens the conversation: %d", name, w.Code)
		}
		form := url.Values{"csrf_token": {s.csrf(cookie, "/family/security")}, "client_id": {"foreign-client-id-01"}, "body": {"hoi"}}
		if w := s.do(http.MethodPost, "/family/chat/"+own[0]+"/send", cookie, form); w.Code != http.StatusForbidden {
			t.Errorf("%s writes into the conversation: %d", name, w.Code)
		}
	}
	if strings.Contains(s.read("/family/chat", anna, own[0]), ">hoi<") {
		t.Fatal("a foreign message was stored")
	}

	// The two families' guardians.
	if code := s.write("/family/chat", anna, others[0], "Hoi Bruno"); code != http.StatusSeeOther {
		t.Fatalf("guardian writes to the other parent: %d", code)
	}
	brunoOthers := s.threads(bruno, "Andere Eltern")
	if len(brunoOthers) != 2 {
		t.Fatalf("bruno should see both guardians of Alex: %v", brunoOthers)
	}
	page := s.read("/family/chat", bruno, others[0])
	if !strings.Contains(page, "Hoi Bruno") || !strings.Contains(page, "<h1>Elternteil von Alex</h1>") || strings.Contains(page, "guardian-a") {
		t.Fatal("the other parent does not see the message, or sees identifiers")
	}
	if code := s.write("/family/chat", bruno, others[0], "Hoi Anna"); code != http.StatusSeeOther {
		t.Fatalf("answer: %d", code)
	}
	if !strings.Contains(s.read("/family/chat", anna, others[0]), "Hoi Anna") {
		t.Fatal("answer not shown")
	}
	// No child reads what the adults write, and no adult reads the children's chat.
	bea := s.child("guardian-b", "tenant-b", "child-b")
	for name, cookie := range map[string]*http.Cookie{"own child": alex, "other child": bea} {
		if w := s.do(http.MethodGet, "/child/chat/"+others[0], cookie, nil); w.Code != http.StatusSeeOther || strings.Contains(w.Body.String(), "Hoi Bruno") {
			t.Errorf("%s reads the parents' conversation: %d", name, w.Code)
		}
	}
	if code := s.write("/child/chat", alex, s.link, "Hoi Bea"); code != http.StatusSeeOther {
		t.Fatalf("children's chat: %d", code)
	}
	if !strings.Contains(s.read("/child/chat", bea, s.link), "Hoi Bea") {
		t.Fatal("children's chat broken by the guardians' chat")
	}
	for _, cookie := range []*http.Cookie{anna, bruno} {
		if w := s.do(http.MethodGet, "/family/chat/"+s.link, cookie, nil); w.Code != http.StatusSeeOther || strings.Contains(w.Body.String(), "Hoi Bea") {
			t.Errorf("a guardian reads the children's chat: %d", w.Code)
		}
	}

	// Everything survives a restart.
	s.start(false)
	if !strings.Contains(s.read("/family/chat", anna, own[0]), "Chume grad") || !strings.Contains(s.read("/family/chat", bruno, others[0]), "Hoi Anna") {
		t.Fatal("messages lost by a restart")
	}

	// Ending the children's contact ends the parents' conversation and
	// deletes its messages; the family conversation stays.
	csrf := s.csrf(bruno, "/family/contacts?child_id=child-b")
	if w := s.do(http.MethodPost, "/family/contacts/remove", bruno, url.Values{"csrf_token": {csrf}, "child_id": {"child-b"}, "link_id": {s.link}, "confirm": {"yes"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("ending the contact: %d %s", w.Code, w.Body)
	}
	if len(s.threads(anna, "Andere Eltern")) != 0 || len(s.threads(bruno, "Andere Eltern")) != 0 {
		t.Fatal("the parents' conversation outlived the contact")
	}
	if w := s.do(http.MethodGet, "/family/chat/"+others[0], anna, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("ended conversation still opens: %d", w.Code)
	}
	raw, _ := os.ReadFile(s.chatPath)
	if strings.Contains(string(raw), "Hoi Bruno") || strings.Contains(string(raw), "Hoi Anna") || !strings.Contains(string(raw), "Chume grad") {
		t.Fatal("the log does not follow the ended contact")
	}
	info, _ := os.Lstat(s.chatPath)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("log mode %v", info.Mode())
	}
}

func TestALockedGuardianLeavesEveryChat(t *testing.T) {
	s := newChatServer(t, true)
	anna, bruno := s.issue("guardian-a", "tenant-a"), s.issue("guardian-b", "tenant-b")
	own, others := s.threads(anna, "Deine Kinder"), s.threads(anna, "Andere Eltern")
	s.write("/family/chat", anna, own[0], "vorher")
	alex := s.child("guardian-a", "tenant-a", "child-a")
	if w := s.act(anna, "lock", url.Values{"confirm": {"yes"}}); w.Code != http.StatusOK {
		t.Fatalf("lock: %d", w.Code)
	}
	// Nobody writes to or as the locked guardian, whatever session exists.
	fresh := s.issue("guardian-a", "tenant-a")
	if w := s.do(http.MethodGet, "/family/chat", fresh, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("locked guardian opens the chats: %d", w.Code)
	}
	if w := s.do(http.MethodGet, "/child/chat/"+own[0], alex, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("child still opens the chat with the locked guardian: %d", w.Code)
	}
	home := s.do(http.MethodGet, "/child", alex, nil).Body.String()
	if strings.Contains(home, own[0]) || len(childThread.FindAllString(home[strings.Index(home, "<h2>Deine Eltern</h2>"):strings.Index(home, "<h2>Deine Crew</h2>")], -1)) != 1 {
		t.Fatal("the locked guardian is still offered to the child")
	}
	left := s.threads(bruno, "Andere Eltern")
	if len(left) != 1 || left[0] == others[0] {
		t.Fatalf("the other family still sees the locked guardian: %v", left)
	}
	// The messages are kept: the operator can lift the lock.
	raw, _ := os.ReadFile(s.chatPath)
	if !strings.Contains(string(raw), "vorher") {
		t.Fatal("a lock deleted messages")
	}
}

func TestWithoutTheSwitchThereIsNoGuardianChat(t *testing.T) {
	s := newSecurityServer(t, false)
	anna := s.issue("guardian-a", "tenant-a")
	overview := s.do(http.MethodGet, "/family", anna, nil).Body.String()
	if strings.Contains(overview, "/family/chat") || !strings.Contains(overview, "Chat für Eltern") || !strings.Contains(overview, "nicht eingeschaltet") {
		t.Fatal("the overview offers the chat or does not say that it is off")
	}
	for _, path := range []string{"/family/chat", "/family/chat/" + strings.Repeat("0", 32), "/family/chat.js"} {
		if w := s.do(http.MethodGet, path, anna, nil); w.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d", path, w.Code)
		}
	}
	id, key := pairingtest.Pair(t, s.pairing, "guardian-a", "tenant-a", "child-a")
	if home := s.do(http.MethodGet, "/child", childLogin(t, s.handler, id, key), nil).Body.String(); strings.Contains(home, "<h2>Deine Eltern</h2>") {
		t.Fatal("the child area offers guardians without the switch")
	}
}
