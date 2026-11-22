package signup

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeDirectory struct {
	mu       sync.Mutex
	emails   map[string][2]string
	inactive map[string]bool
	fail     bool
}

func (d *fakeDirectory) Has(email string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.emails[email]
	return ok
}

func (d *fakeDirectory) Register(email, guardianID, tenantID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail {
		return errors.New("disk full")
	}
	if _, ok := d.emails[email]; ok {
		return errors.New("exists")
	}
	d.emails[email] = [2]string{guardianID, tenantID}
	return nil
}

func (d *fakeDirectory) ActiveGuardian(_ context.Context, guardianID, tenantID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.inactive[tenantID+"/"+guardianID]
}

type family struct{ tenant, guardian, child, name string }

type fakeFamilies struct {
	mu        sync.Mutex
	created   []family
	discarded []family
	fail      bool
}

func (f *fakeFamilies) CreateFamily(tenantID, guardianID, childID, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("disk full")
	}
	f.created = append(f.created, family{tenantID, guardianID, childID, name})
	return nil
}

func (f *fakeFamilies) DiscardFamily(tenantID, guardianID, childID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discarded = append(f.discarded, family{tenantID, guardianID, childID, ""})
	return nil
}

type sent struct{ email, link string }

type fakeMailer struct {
	mu   sync.Mutex
	sent []sent
	fail bool
}

func (m *fakeMailer) SendRegistrationLink(_ context.Context, email, link string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("smtp down")
	}
	m.sent = append(m.sent, sent{email, link})
	return nil
}

func (m *fakeMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func (m *fakeMailer) last(t *testing.T) sent {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) == 0 {
		t.Fatal("no mail sent")
	}
	return m.sent[len(m.sent)-1]
}

type fakeSessions struct {
	mu     sync.Mutex
	issued [][2]string
	fail   bool
}

func (s *fakeSessions) IssueVerified(w http.ResponseWriter, guardianID, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("no entropy")
	}
	s.issued = append(s.issued, [2]string{guardianID, tenantID})
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "issued"})
	return nil
}

type connection struct{ inviterGuardian, inviterTenant, inviterChild, guardian, tenant, child string }

// fakeConnections knows which guardian may approve contacts for which child
// and records the connections it is asked to make.
type fakeConnections struct {
	mu    sync.Mutex
	names map[string]string // "guardian/tenant/child" -> first name
	made  []connection
	fail  bool
}

func (c *fakeConnections) Inviting(guardianID, tenantID, childID string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	name, ok := c.names[guardianID+"/"+tenantID+"/"+childID]
	return name, ok
}

func (c *fakeConnections) Connect(inviterGuardian, inviterTenant, inviterChild, guardian, tenant, child string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return errors.New("not allowed")
	}
	c.made = append(c.made, connection{inviterGuardian, inviterTenant, inviterChild, guardian, tenant, child})
	return nil
}

type rig struct {
	connections *fakeConnections
	h           *Handler
	routes      http.Handler
	store       *Store
	clock       *clock
	directory   *fakeDirectory
	families    *fakeFamilies
	mailer      *fakeMailer
	sessions    *fakeSessions
}

func newRig(t *testing.T) *rig {
	t.Helper()
	return newRigWith(t, nil)
}

// newConnectingRig is an installation on which contacts are switched on:
// Anna may approve contacts for Lea.
func newConnectingRig(t *testing.T) *rig {
	t.Helper()
	return newRigWith(t, &fakeConnections{names: map[string]string{"anna/family-a/lea": "Lea"}})
}

func newRigWith(t *testing.T, connections *fakeConnections) *rig {
	t.Helper()
	store, c, _ := newStore(t)
	r := &rig{store: store, clock: c, connections: connections,
		directory: &fakeDirectory{emails: map[string][2]string{"anna@example.org": {"anna", "family-a"}}, inactive: map[string]bool{}},
		families:  &fakeFamilies{}, mailer: &fakeMailer{}, sessions: &fakeSessions{}}
	cfg := Config{Origin: "https://yapuno.example", Store: store, Directory: r.directory, Families: r.families, Mailer: r.mailer, Sessions: r.sessions, Now: c.now}
	if connections != nil {
		// Assign only a non-nil value: a typed nil would be a non-nil interface.
		cfg.Connections, cfg.ParentsChat = connections, true
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.h, r.routes = h, h.Routes()
	return r
}

// inviteFor creates an invitation with which Anna approves a contact for Lea.
func (r *rig) inviteFor(t *testing.T, child string) (string, Invitation) {
	t.Helper()
	code, inv, err := r.store.Invite("anna", "family-a", child)
	if err != nil {
		t.Fatal(err)
	}
	return code, inv
}

// confirmed runs a registration up to the confirmation link and returns its token.
func (r *rig) confirmed(t *testing.T, code, email, child string) string {
	t.Helper()
	if rec := r.post("/join/request", request(code, email, child)); rec.Code != http.StatusOK {
		t.Fatalf("request: %d %s", rec.Code, rec.Body)
	}
	return tokenOf(t, r.mailer.last(t).link)
}

func (r *rig) invite(t *testing.T) (string, Invitation) {
	t.Helper()
	code, inv, err := r.store.Invite("anna", "family-a", "")
	if err != nil {
		t.Fatal(err)
	}
	return code, inv
}

func (r *rig) get(target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func (r *rig) post(target string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	r.routes.ServeHTTP(rec, req)
	return rec
}

func request(code, email, child string) url.Values {
	return url.Values{"code": {code}, "email": {email}, "child_name": {child}, "confirm": {"yes"}}
}

func tokenOf(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host != "yapuno.example" || u.Path != "/join/verify" || len(u.Query()["token"]) != 1 {
		t.Fatalf("link: %q", link)
	}
	return u.Query().Get("token")
}

func TestAnInvitedGuardianRegistersAFamilyAndIsSignedIn(t *testing.T) {
	r := newRig(t)
	code, inv := r.invite(t)

	page := r.get("/join?code=" + code)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `name="code" value="`+code+`"`) ||
		!strings.Contains(page.Body.String(), `action="/join/request"`) || !strings.Contains(page.Body.String(), "erziehungsberechtigt") {
		t.Fatalf("join page: %d %s", page.Code, page.Body)
	}
	asked := r.post("/join/request", request(code, " Bruno@Example.org ", " Leo "))
	if asked.Code != http.StatusOK || !strings.Contains(asked.Body.String(), "Postfach") {
		t.Fatalf("request: %d %s", asked.Code, asked.Body)
	}
	mail := r.mailer.last(t)
	if mail.email != "bruno@example.org" {
		t.Fatalf("mail to %q", mail.email)
	}
	token := tokenOf(t, mail.link)
	// Opening the link changes nothing: mail scanners must not register anyone.
	confirm := r.get("/join/verify?token=" + token)
	if confirm.Code != http.StatusOK || !strings.Contains(confirm.Body.String(), `method="post" action="/join/verify"`) || len(r.families.created) != 0 {
		t.Fatalf("confirm page: %d %s", confirm.Code, confirm.Body)
	}
	done := r.post("/join/verify", url.Values{"token": {token}})
	if done.Code != http.StatusSeeOther || done.Header().Get("Location") != "/family" {
		t.Fatalf("verify: %d %s", done.Code, done.Body)
	}
	if len(r.families.created) != 1 {
		t.Fatalf("families: %+v", r.families.created)
	}
	f := r.families.created[0]
	ids := regexp.MustCompile(`^[fgc]-[0-9a-f]{32}$`)
	if f.name != "Leo" || !ids.MatchString(f.tenant) || !ids.MatchString(f.guardian) || !ids.MatchString(f.child) ||
		f.tenant[0] != 'f' || f.guardian[0] != 'g' || f.child[0] != 'c' || f.tenant == "family-a" {
		t.Fatalf("family: %+v", f)
	}
	if got := r.directory.emails["bruno@example.org"]; got != [2]string{f.guardian, f.tenant} {
		t.Fatalf("directory: %v", got)
	}
	if len(r.sessions.issued) != 1 || r.sessions.issued[0] != [2]string{f.guardian, f.tenant} || len(done.Result().Cookies()) != 1 {
		t.Fatalf("session: %v", r.sessions.issued)
	}
	// The invitation and the link are spent.
	if _, err := r.store.Usable(inv.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("invitation still usable")
	}
	if again := r.post("/join/verify", url.Values{"token": {token}}); again.Code != http.StatusForbidden {
		t.Fatalf("link reused: %d", again.Code)
	}
	if again := r.get("/join?code=" + code); again.Code != http.StatusNotFound {
		t.Fatalf("invitation reused: %d", again.Code)
	}
	if len(r.families.created) != 1 || r.store.Registered() != 1 {
		t.Fatal("registered twice")
	}
}

func TestPagesAreNotCachedCarryNoScriptAndUseSkinTokensOnly(t *testing.T) {
	// Once without and once with the offer to connect the children.
	for variant, r := range map[string]*rig{"plain": newRig(t), "connecting": newConnectingRig(t)} {
		code, _ := r.inviteFor(t, "lea")
		r.post("/join/request", request(code, "bruno@example.org", "Leo"))
		token := tokenOf(t, r.mailer.last(t).link)
		hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
		confirm := r.get("/join/verify?token=" + token)
		if offered := strings.Contains(confirm.Body.String(), `name="connect"`); offered != (variant == "connecting") {
			t.Fatalf("%s: connection offered: %v", variant, offered)
		}
		for name, rec := range map[string]*httptest.ResponseRecorder{
			"join":    r.get("/join?code=" + code),
			"invalid": r.get("/join?code=" + strings.Repeat("A", 22)),
			"asked":   r.post("/join/request", request(code, "carla@example.org", "Lia")),
			"confirm": confirm,
			"refused": r.post("/join/verify", url.Values{"token": {strings.Repeat("A", 43)}}),
		} {
			name = variant + " " + name
			body := rec.Body.String()
			if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" ||
				rec.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") ||
				!strings.Contains(rec.Header().Get("Content-Security-Policy"), "form-action 'self'") {
				t.Errorf("%s: headers %v", name, rec.Header())
			}
			if strings.Contains(body, "<script") || !strings.Contains(body, `<html lang="de">`) || !strings.Contains(body, "width=device-width") {
				t.Errorf("%s: page shell", name)
			}
			style := body[strings.Index(body, "<style>")+7 : strings.Index(body, "</style>")]
			component := style[strings.Index(style, "body{margin:0}"):]
			if hex.MatchString(component) || !strings.Contains(component, "var(--yp-") {
				t.Errorf("%s: component styles must use skin tokens only", name)
			}
			if !strings.Contains(body, "synthetischen Profilen") {
				t.Errorf("%s: preview note missing", name)
			}
		}
	}
}

func TestTheJoinPageSaysWhatIsNotChecked(t *testing.T) {
	r := newRig(t)
	code, _ := r.invite(t)
	body := r.get("/join?code=" + code).Body.String()
	for _, want := range []string{"prüft nicht", "nicht automatisch Kontakte", "10 Minuten", `autocomplete="email"`, `maxlength="40"`, `type="checkbox" name="confirm" value="yes" required`} {
		if !strings.Contains(body, want) {
			t.Errorf("join page lacks %q", want)
		}
	}
}

func TestAnInvalidInvitationShowsNoFormAndSendsNoMail(t *testing.T) {
	r := newRig(t)
	code, inv := r.invite(t)
	if err := r.store.Withdraw("anna", "family-a", inv.ID); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/join", "/join?code=", "/join?code=" + code, "/join?code=" + strings.Repeat("B", 22), "/join?code=" + code + "&code=" + code} {
		rec := r.get(target)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "<form") {
			t.Errorf("%s: %d", target, rec.Code)
		}
	}
	if rec := r.post("/join/request", request(code, "bruno@example.org", "Leo")); rec.Code != http.StatusNotFound || r.mailer.count() != 0 {
		t.Fatalf("request with a withdrawn invitation: %d, %d mails", rec.Code, r.mailer.count())
	}
}

func TestAnInvitationOfAGuardianWhoLostAccessIsVoid(t *testing.T) {
	r := newRig(t)
	code, _ := r.invite(t)
	r.post("/join/request", request(code, "bruno@example.org", "Leo"))
	token := tokenOf(t, r.mailer.last(t).link)
	// The inviter locks their access or is deactivated after sending the link.
	r.directory.inactive["family-a/anna"] = true
	if rec := r.get("/join?code=" + code); rec.Code != http.StatusNotFound {
		t.Fatalf("join page: %d", rec.Code)
	}
	if rec := r.post("/join/request", request(code, "carla@example.org", "Lia")); rec.Code != http.StatusNotFound || r.mailer.count() != 1 {
		t.Fatalf("request: %d", rec.Code)
	}
	if rec := r.post("/join/verify", url.Values{"token": {token}}); rec.Code != http.StatusForbidden {
		t.Fatalf("verify: %d", rec.Code)
	}
	if len(r.families.created) != 0 || r.directory.Has("bruno@example.org") || len(r.sessions.issued) != 0 {
		t.Fatal("registered through a void invitation")
	}
}

func TestARegisteredAddressGetsTheSameAnswerAndNoMail(t *testing.T) {
	r := newRig(t)
	code, _ := r.invite(t)
	known := r.post("/join/request", request(code, "anna@example.org", "Leo"))
	if r.mailer.count() != 0 {
		t.Fatal("mail sent to a registered address")
	}
	fresh := r.post("/join/request", request(code, "bruno@example.org", "Leo"))
	if r.mailer.count() != 1 {
		t.Fatal("no mail for a new address")
	}
	if known.Code != fresh.Code || known.Body.String() != fresh.Body.String() {
		t.Fatal("the answer reveals whether an address is registered")
	}
}

func TestMailsAreLimitedPerAddressAndPerInvitation(t *testing.T) {
	r := newRig(t)
	code, _ := r.invite(t)
	first := r.post("/join/request", request(code, "bruno@example.org", "Leo"))
	again := r.post("/join/request", request(code, "BRUNO@example.org", "Leo"))
	if r.mailer.count() != 1 || first.Body.String() != again.Body.String() {
		t.Fatalf("cooldown: %d mails", r.mailer.count())
	}
	r.clock.add(emailCooldown)
	r.post("/join/request", request(code, "bruno@example.org", "Leo"))
	if r.mailer.count() != 2 {
		t.Fatalf("after the cooldown: %d mails", r.mailer.count())
	}
	// Requests for registered addresses use up the budget too, so the budget
	// is no oracle either.
	r.post("/join/request", request(code, "anna@example.org", "Leo"))
	for _, address := range []string{"c@example.org", "d@example.org"} {
		r.post("/join/request", request(code, address, "Leo"))
	}
	if r.mailer.count() != 4 {
		t.Fatalf("within the budget: %d mails", r.mailer.count())
	}
	over := r.post("/join/request", request(code, "e@example.org", "Leo"))
	if r.mailer.count() != 4 || over.Code != http.StatusOK || over.Body.String() != first.Body.String() {
		t.Fatalf("over the budget: %d mails, %d", r.mailer.count(), over.Code)
	}
	// Another invitation has its own budget; the address cooldown still holds.
	other, _ := r.invite(t)
	r.post("/join/request", request(other, "e@example.org", "Leo"))
	if r.mailer.count() != 4 {
		t.Fatal("the address cooldown does not hold across invitations")
	}
	r.post("/join/request", request(other, "f@example.org", "Leo"))
	if r.mailer.count() != 5 {
		t.Fatal("a second invitation has no budget")
	}
}

func TestBadRequestsAreRefusedBeforeAnythingHappens(t *testing.T) {
	r := newRig(t)
	code, _ := r.invite(t)
	good := request(code, "bruno@example.org", "Leo")
	with := func(key string, values ...string) url.Values {
		out := url.Values{}
		for k, v := range good {
			out[k] = v
		}
		if values == nil {
			delete(out, key)
		} else {
			out[key] = values
		}
		return out
	}
	for name, form := range map[string]url.Values{
		"no confirmation":    with("confirm"),
		"wrong confirmation": with("confirm", "no"),
		"two confirmations":  with("confirm", "yes", "yes"),
		"no address":         with("email"),
		"two addresses":      with("email", "a@example.org", "b@example.org"),
		"address with name":  with("email", "Bruno <bruno@example.org>"),
		"header injection":   with("email", "bruno@example.org\r\nBcc: x@example.org"),
		"no child":           with("child_name"),
		"blank child":        with("child_name", "   "),
		"markup in name":     with("child_name", "<b>Leo</b>"),
		"long name":          with("child_name", strings.Repeat("x", 41)),
		"two names":          with("child_name", "Leo", "Lia"),
		"no code":            with("code"),
	} {
		rec := r.post("/join/request", form)
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	big := request(code, "bruno@example.org", "Leo")
	big.Set("padding", strings.Repeat("x", 5000))
	if rec := r.post("/join/request", big); rec.Code != http.StatusBadRequest {
		t.Errorf("oversized form: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/join/request", strings.NewReader(`{"code":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("json body: %d", rec.Code)
	}
	if r.mailer.count() != 0 {
		t.Fatalf("%d mails for bad requests", r.mailer.count())
	}
	// None of them used up the budget of the invitation.
	if inv, _ := r.store.Lookup(code); inv.Mails != 0 {
		t.Fatalf("bad requests charged %d mails", inv.Mails)
	}
}

func TestTheConfirmationLinkExpiresAndMustBeWellFormed(t *testing.T) {
	r := newRig(t)
	code, inv := r.invite(t)
	r.post("/join/request", request(code, "bruno@example.org", "Leo"))
	token := tokenOf(t, r.mailer.last(t).link)
	for _, bad := range []url.Values{{}, {"token": {""}}, {"token": {"short"}}, {"token": {token, token}}, {"token": {strings.Repeat("A", 43)}}, {"token": {token + "A"}}} {
		if rec := r.post("/join/verify", bad); rec.Code != http.StatusForbidden && rec.Code != http.StatusBadRequest {
			t.Errorf("%v: %d", bad, rec.Code)
		}
	}
	if rec := r.get("/join/verify"); rec.Code != http.StatusBadRequest {
		t.Errorf("confirm page without token: %d", rec.Code)
	}
	r.clock.add(linkTTL)
	if rec := r.post("/join/verify", url.Values{"token": {token}}); rec.Code != http.StatusForbidden {
		t.Fatalf("expired link: %d", rec.Code)
	}
	if len(r.families.created) != 0 || r.directory.Has("bruno@example.org") {
		t.Fatal("registered with an expired link")
	}
	// The invitation itself is still good for a new attempt.
	if _, err := r.store.Usable(inv.ID); err != nil {
		t.Fatal("an expired link burned the invitation")
	}
}

func TestOneLinkRegistersOneFamilyEvenWhenConfirmedConcurrently(t *testing.T) {
	r := newRig(t)
	code, _ := r.invite(t)
	r.post("/join/request", request(code, "bruno@example.org", "Leo"))
	token := tokenOf(t, r.mailer.last(t).link)
	var wg sync.WaitGroup
	codes := make(chan int, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- r.post("/join/verify", url.Values{"token": {token}}).Code
		}()
	}
	wg.Wait()
	close(codes)
	ok := 0
	for c := range codes {
		if c == http.StatusSeeOther {
			ok++
		}
	}
	if ok != 1 || len(r.families.created) != 1 || len(r.sessions.issued) != 1 {
		t.Fatalf("%d confirmations succeeded, %d families", ok, len(r.families.created))
	}
}

func TestTwoLinksForOneInvitationRegisterOnlyOneFamily(t *testing.T) {
	r := newRig(t)
	code, _ := r.invite(t)
	r.post("/join/request", request(code, "bruno@example.org", "Leo"))
	first := tokenOf(t, r.mailer.last(t).link)
	r.post("/join/request", request(code, "carla@example.org", "Lia"))
	second := tokenOf(t, r.mailer.last(t).link)
	if rec := r.post("/join/verify", url.Values{"token": {second}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("first confirmation: %d", rec.Code)
	}
	if rec := r.post("/join/verify", url.Values{"token": {first}}); rec.Code != http.StatusForbidden {
		t.Fatalf("second confirmation: %d", rec.Code)
	}
	if len(r.families.created) != 1 || r.directory.Has("bruno@example.org") || !r.directory.Has("carla@example.org") {
		t.Fatal("one invitation registered two families")
	}
}

func TestAnAddressRegisteredInTheMeantimeIsRefusedWithoutBurningTheInvitation(t *testing.T) {
	r := newRig(t)
	one, _ := r.invite(t)
	two, second := r.invite(t)
	r.post("/join/request", request(one, "bruno@example.org", "Leo"))
	first := tokenOf(t, r.mailer.last(t).link)
	r.clock.add(emailCooldown)
	r.post("/join/request", request(two, "bruno@example.org", "Leo"))
	other := tokenOf(t, r.mailer.last(t).link)
	if rec := r.post("/join/verify", url.Values{"token": {first}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("first: %d", rec.Code)
	}
	if rec := r.post("/join/verify", url.Values{"token": {other}}); rec.Code != http.StatusForbidden {
		t.Fatalf("same address again: %d", rec.Code)
	}
	if len(r.families.created) != 1 {
		t.Fatal("one address owns two families")
	}
	if _, err := r.store.Usable(second.ID); err != nil {
		t.Fatal("the refused attempt burned the other invitation")
	}
}

func TestAFailedFamilyWriteLeavesNothingBehind(t *testing.T) {
	r := newRig(t)
	code, inv := r.invite(t)
	r.post("/join/request", request(code, "bruno@example.org", "Leo"))
	r.families.fail = true
	if rec := r.post("/join/verify", url.Values{"token": {tokenOf(t, r.mailer.last(t).link)}}); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("verify: %d", rec.Code)
	}
	if r.directory.Has("bruno@example.org") || len(r.sessions.issued) != 0 || r.store.Registered() != 0 {
		t.Fatal("half a registration was kept")
	}
	if _, err := r.store.Usable(inv.ID); err != nil {
		t.Fatal("the invitation was burned by a server failure")
	}
	// The link itself is spent whatever happened; a new one is needed.
	r.families.fail = false
	if rec := r.post("/join/verify", url.Values{"token": {tokenOf(t, r.mailer.last(t).link)}}); rec.Code != http.StatusForbidden || len(r.families.created) != 0 {
		t.Fatalf("link reused after a failure: %d", rec.Code)
	}
}

func TestAFailedIdentityWriteDiscardsTheNewFamily(t *testing.T) {
	r := newRig(t)
	code, inv := r.invite(t)
	r.post("/join/request", request(code, "bruno@example.org", "Leo"))
	r.directory.fail = true
	if rec := r.post("/join/verify", url.Values{"token": {tokenOf(t, r.mailer.last(t).link)}}); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("verify: %d", rec.Code)
	}
	if len(r.families.created) != 1 || len(r.families.discarded) != 1 ||
		r.families.discarded[0].tenant != r.families.created[0].tenant || r.families.discarded[0].guardian != r.families.created[0].guardian || r.families.discarded[0].child != r.families.created[0].child {
		t.Fatalf("created %+v, discarded %+v", r.families.created, r.families.discarded)
	}
	if len(r.sessions.issued) != 0 || r.store.Registered() != 0 {
		t.Fatal("half a registration was kept")
	}
	if _, err := r.store.Usable(inv.ID); err != nil {
		t.Fatal("the invitation was burned by a server failure")
	}
}

func TestAFailedSessionStillLeavesAUsableAccount(t *testing.T) {
	r := newRig(t)
	code, _ := r.invite(t)
	r.post("/join/request", request(code, "bruno@example.org", "Leo"))
	r.sessions.fail = true
	rec := r.post("/join/verify", url.Values{"token": {tokenOf(t, r.mailer.last(t).link)}})
	// The family exists; the guardian signs in through the normal login.
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" || !r.directory.Has("bruno@example.org") || len(r.families.discarded) != 0 {
		t.Fatalf("verify: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestAMailThatCouldNotBeSentLeavesNoUsableLink(t *testing.T) {
	r := newRig(t)
	code, _ := r.invite(t)
	r.mailer.fail = true
	if rec := r.post("/join/request", request(code, "bruno@example.org", "Leo")); rec.Code != http.StatusOK {
		t.Fatalf("request: %d", rec.Code)
	}
	r.h.mu.Lock()
	left := len(r.h.pending)
	r.h.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d pending registrations without a mail", left)
	}
}

func TestPendingRegistrationsAreBounded(t *testing.T) {
	r := newRig(t)
	r.h.mu.Lock()
	for i := 0; i < maxPending; i++ {
		var d [32]byte
		d[0], d[1], d[2] = byte(i), byte(i>>8), byte(i>>16)
		r.h.pending[d] = registration{expires: r.clock.now().Add(linkTTL)}
	}
	r.h.mu.Unlock()
	code, _ := r.invite(t)
	r.post("/join/request", request(code, "bruno@example.org", "Leo"))
	if r.mailer.count() != 0 {
		t.Fatal("accepted beyond the bound")
	}
	// Expired entries make room again.
	r.clock.add(linkTTL)
	r.post("/join/request", request(code, "bruno@example.org", "Leo"))
	if r.mailer.count() != 1 {
		t.Fatal("expired entries were not cleared")
	}
}

func TestNewRejectsIncompleteOrInsecureConfiguration(t *testing.T) {
	r := newRig(t)
	good := Config{Origin: "https://yapuno.example", Store: r.store, Directory: r.directory, Families: r.families, Mailer: r.mailer, Sessions: r.sessions, Now: time.Now}
	if _, err := New(good); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Config){
		"http origin":      func(c *Config) { c.Origin = "http://yapuno.example" },
		"origin with path": func(c *Config) { c.Origin = "https://yapuno.example/app" },
		"no store":         func(c *Config) { c.Store = nil },
		"no directory":     func(c *Config) { c.Directory = nil },
		"no families":      func(c *Config) { c.Families = nil },
		"no mailer":        func(c *Config) { c.Mailer = nil },
		"no sessions":      func(c *Config) { c.Sessions = nil },
		"no clock":         func(c *Config) { c.Now = nil },
	} {
		cfg := good
		change(&cfg)
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
