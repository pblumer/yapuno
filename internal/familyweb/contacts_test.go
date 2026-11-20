package familyweb

import (
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
	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/skin"
)

var (
	sessionA = Session{GuardianID: "guardian-a", TenantID: "tenant-a", CSRFToken: "csrf-a-0123456789"}
	sessionB = Session{GuardianID: "guardian-b", TenantID: "tenant-b", CSRFToken: "csrf-b-0123456789"}
	sessionC = Session{GuardianID: "guardian-c", TenantID: "tenant-c", CSRFToken: "csrf-c-0123456789"}
	alex     = contacts.Party{TenantID: "tenant-a", ChildID: "child-a"}
	bea      = contacts.Party{TenantID: "tenant-b", ChildID: "child-b"}
)

type contactWorld struct {
	store    *contacts.Store
	families *familystore.Store
	looks    *appearance.Store
}

func newContactWorld(t *testing.T) *contactWorld {
	t.Helper()
	dir := t.TempDir()
	families, err := familystore.Open(filepath.Join(dir, "family.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, tenant, guardian, name string }{
		{"child-a", "tenant-a", "guardian-a", "Alex"},
		{"child-b", "tenant-b", "guardian-b", "Bea"},
		{"child-c", "tenant-c", "guardian-c", "Cem"},
	} {
		grant := authz.GuardianGrant{GuardianID: c.guardian, ChildID: c.id, TenantID: c.tenant, Active: true, CanManageDevices: true, CanApproveContacts: true}
		if err := families.Bootstrap(familydashboard.Child{ID: c.id, TenantID: c.tenant, DisplayName: c.name}, grant); err != nil {
			t.Fatal(err)
		}
	}
	store, err := contacts.Open(filepath.Join(dir, "contacts.json"), families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	looks, err := appearance.Open(filepath.Join(dir, "appearance.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &contactWorld{store: store, families: families, looks: looks}
}

func (cw *contactWorld) as(t *testing.T, auth testAuth) http.Handler {
	t.Helper()
	h, err := NewContactPages(auth, cw.store, cw.families, cw.looks)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func contactGet(h http.Handler, child string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/contacts?child_id="+url.QueryEscape(child), nil))
	return w
}

func contactPost(h http.Handler, action string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/family/contacts/"+action, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var (
	codePattern = regexp.MustCompile(`<output[^>]*>([A-Z2-7-]+)</output>`)
	linkPattern = regexp.MustCompile(`name="link_id" value="([0-9a-f]+)"`)
)

// inviteCode creates an invitation as guardian A and returns the shown code.
func inviteCode(t *testing.T, a http.Handler) string {
	t.Helper()
	w := contactPost(a, "invite", url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}})
	if w.Code != http.StatusOK {
		t.Fatalf("invite: %d %s", w.Code, w.Body.String())
	}
	m := codePattern.FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatalf("no code on invitation page: %s", w.Body.String())
	}
	return m[1]
}

func pendingLink(t *testing.T, cw *contactWorld) (http.Handler, http.Handler, string) {
	t.Helper()
	a, b := cw.as(t, testAuth{session: sessionA}), cw.as(t, testAuth{session: sessionB})
	code := inviteCode(t, a)
	if w := contactPost(b, "redeem", url.Values{"csrf_token": {sessionB.CSRFToken}, "child_id": {"child-b"}, "code": {code}}); w.Code != http.StatusSeeOther {
		t.Fatalf("redeem: %d %s", w.Code, w.Body.String())
	}
	m := linkPattern.FindStringSubmatch(contactGet(a, "child-a").Body.String())
	if m == nil {
		t.Fatal("pending request not shown to inviting guardian")
	}
	return a, b, m[1]
}

func TestContactPagesRequireAuthentication(t *testing.T) {
	cw := newContactWorld(t)
	h := cw.as(t, testAuth{err: errors.New("no session")})
	if w := contactGet(h, "child-a"); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET: %d", w.Code)
	}
	for _, action := range []string{"invite", "redeem", "confirm", "remove"} {
		form := url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}, "code": {"x"}, "link_id": {"x"}, "confirm": {"yes"}}
		if w := contactPost(h, action, form); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: %d", action, w.Code)
		}
	}
}

func TestContactFlowThroughBothFamilies(t *testing.T) {
	cw := newContactWorld(t)
	a, b, id := pendingLink(t, cw)
	if cw.store.Allowed(alex, bea) {
		t.Fatal("contact active before the inviting family confirmed")
	}
	pageA := contactGet(a, "child-a").Body.String()
	if !strings.Contains(pageA, "Bea") || !strings.Contains(pageA, `action="/family/contacts/confirm"`) {
		t.Fatal("inviting guardian cannot see or confirm the request")
	}
	pageB := contactGet(b, "child-b").Body.String()
	if !strings.Contains(pageB, "Alex") || strings.Contains(pageB, `action="/family/contacts/confirm"`) {
		t.Fatal("redeeming guardian must see the pending request but must not be able to confirm it")
	}
	// The other family's internal identifiers never reach the page.
	for _, leak := range []string{"tenant-b", "child-b", "guardian-b"} {
		if strings.Contains(pageA, leak) {
			t.Fatalf("identifier %q of the other family leaked", leak)
		}
	}
	confirm := url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}, "link_id": {id}, "confirm": {"yes"}}
	if w := contactPost(a, "confirm", confirm); w.Code != http.StatusSeeOther || !strings.HasPrefix(w.Header().Get("Location"), "/family/contacts?child_id=child-a") {
		t.Fatalf("confirm: %d %q", w.Code, w.Header().Get("Location"))
	}
	if !cw.store.Allowed(alex, bea) {
		t.Fatal("contact not active after confirmation")
	}
	if !strings.Contains(contactGet(b, "child-b").Body.String(), "Alex") {
		t.Fatal("active contact not listed for the other family")
	}
	remove := url.Values{"csrf_token": {sessionB.CSRFToken}, "child_id": {"child-b"}, "link_id": {id}, "confirm": {"yes"}}
	if w := contactPost(b, "remove", remove); w.Code != http.StatusSeeOther {
		t.Fatalf("remove: %d", w.Code)
	}
	if cw.store.Allowed(alex, bea) {
		t.Fatal("contact still active after the other family removed it")
	}
}

func TestContactPagesUseSessionIdentityAndChildScope(t *testing.T) {
	cw := newContactWorld(t)
	a, _, id := pendingLink(t, cw)
	c := cw.as(t, testAuth{session: sessionC})
	// Guardian C names another family's child and smuggles identity fields.
	if w := contactGet(c, "child-a"); w.Code != http.StatusForbidden {
		t.Fatalf("foreign overview: %d", w.Code)
	}
	for action, form := range map[string]url.Values{
		"invite":  {"child_id": {"child-a"}},
		"redeem":  {"child_id": {"child-a"}, "code": {"AAAAA-AAAAA-AAAAA-AAAAA-AAAAA-A"}},
		"confirm": {"child_id": {"child-a"}, "link_id": {id}, "confirm": {"yes"}},
		"remove":  {"child_id": {"child-a"}, "link_id": {id}, "confirm": {"yes"}},
	} {
		form.Set("csrf_token", sessionC.CSRFToken)
		form.Set("guardian_id", "guardian-a")
		form.Set("tenant_id", "tenant-a")
		if w := contactPost(c, action, form); w.Code != http.StatusForbidden {
			t.Errorf("%s with forged identity: %d", action, w.Code)
		}
	}
	// With their own child, C still cannot touch a request between A and B.
	for _, action := range []string{"confirm", "remove"} {
		form := url.Values{"csrf_token": {sessionC.CSRFToken}, "child_id": {"child-c"}, "link_id": {id}, "confirm": {"yes"}}
		if w := contactPost(c, action, form); w.Code != http.StatusForbidden {
			t.Errorf("%s on foreign request: %d", action, w.Code)
		}
	}
	if !strings.Contains(contactGet(a, "child-a").Body.String(), id) || cw.store.Allowed(alex, bea) {
		t.Fatal("foreign attempts changed the request")
	}
}

func TestContactPostsRejectInvalidSubmissions(t *testing.T) {
	cw := newContactWorld(t)
	a, _, id := pendingLink(t, cw)
	valid := func() url.Values {
		return url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}, "link_id": {id}, "confirm": {"yes"}}
	}
	cases := map[string]struct {
		mutate func(url.Values)
		want   int
	}{
		"wrong csrf":           {func(f url.Values) { f.Set("csrf_token", sessionB.CSRFToken) }, http.StatusForbidden},
		"missing csrf":         {func(f url.Values) { f.Del("csrf_token") }, http.StatusBadRequest},
		"duplicate link id":    {func(f url.Values) { f.Add("link_id", "other") }, http.StatusBadRequest},
		"no confirmation":      {func(f url.Values) { f.Del("confirm") }, http.StatusBadRequest},
		"confirmation not yes": {func(f url.Values) { f.Set("confirm", "no") }, http.StatusBadRequest},
		"unknown link":         {func(f url.Values) { f.Set("link_id", strings.Repeat("0", 32)) }, http.StatusNotFound},
		"malformed link":       {func(f url.Values) { f.Set("link_id", "../../etc") }, http.StatusBadRequest},
		"invalid child":        {func(f url.Values) { f.Set("child_id", "<script>") }, http.StatusBadRequest},
		"oversized body":       {func(f url.Values) { f.Set("padding", strings.Repeat("x", 8192)) }, http.StatusBadRequest},
	}
	for name, c := range cases {
		for _, action := range []string{"confirm", "remove"} {
			form := valid()
			c.mutate(form)
			if w := contactPost(a, action, form); w.Code != c.want {
				t.Errorf("%s/%s: got %d, want %d", action, name, w.Code, c.want)
			}
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/family/contacts/confirm", strings.NewReader(valid().Encode()))
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("content type: %d", w.Code)
	}
	if cw.store.Allowed(alex, bea) || !strings.Contains(contactGet(a, "child-a").Body.String(), id) {
		t.Fatal("rejected submissions changed the request")
	}
}

func TestRedeemProblemsAreExplained(t *testing.T) {
	cw := newContactWorld(t)
	a, b := cw.as(t, testAuth{session: sessionA}), cw.as(t, testAuth{session: sessionB})
	c := cw.as(t, testAuth{session: sessionC})
	redeem := func(h http.Handler, s Session, child, code string) *httptest.ResponseRecorder {
		return contactPost(h, "redeem", url.Values{"csrf_token": {s.CSRFToken}, "child_id": {child}, "code": {code}})
	}
	const injected = `<script>alert(1)</script>`
	bad := redeem(b, sessionB, "child-b", injected)
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), `role="alert"`) || strings.Contains(bad.Body.String(), injected) {
		t.Fatalf("invalid code: %d", bad.Code)
	}
	code := inviteCode(t, a)
	if w := redeem(a, sessionA, "child-a", code); w.Code != http.StatusBadRequest {
		t.Fatalf("own code: %d", w.Code)
	}
	if w := redeem(b, sessionB, "child-b", code); w.Code != http.StatusSeeOther {
		t.Fatalf("redeem: %d", w.Code)
	}
	// A second family learns the code was used, which reveals interception.
	used := redeem(c, sessionC, "child-c", code)
	if used.Code != http.StatusConflict || !strings.Contains(used.Body.String(), `role="alert"`) || !strings.Contains(used.Body.String(), "bereits") {
		t.Fatalf("reused code: %d %s", used.Code, used.Body.String())
	}
	if strings.Contains(used.Body.String(), "Alex") || strings.Contains(used.Body.String(), "Bea") {
		t.Fatal("a failed redeem revealed who is behind the code")
	}
	again := inviteCode(t, a)
	if w := redeem(b, sessionB, "child-b", again); w.Code != http.StatusConflict {
		t.Fatalf("duplicate relationship: %d", w.Code)
	}
}

func TestInvitationPageAndLimit(t *testing.T) {
	cw := newContactWorld(t)
	a := cw.as(t, testAuth{session: sessionA})
	w := contactPost(a, "invite", url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}})
	body := w.Body.String()
	for _, want := range []string{"Alex", "24 Stunden", "einmal", `href="/family/contacts?child_id=child-a"`} {
		if !strings.Contains(body, want) {
			t.Errorf("invitation page misses %q", want)
		}
	}
	if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatal("invitation page lacks security headers")
	}
	for i := 1; i < contacts.MaxOpenInvitations; i++ {
		inviteCode(t, a)
	}
	limited := contactPost(a, "invite", url.Values{"csrf_token": {sessionA.CSRFToken}, "child_id": {"child-a"}})
	if limited.Code != http.StatusTooManyRequests || !strings.Contains(limited.Body.String(), `role="alert"`) {
		t.Fatalf("limit: %d", limited.Code)
	}
}

func TestContactPageIsAccessibleAndSkinned(t *testing.T) {
	cw := newContactWorld(t)
	if err := cw.looks.Set(appearance.Subject{Kind: appearance.KindGuardian, TenantID: "tenant-a", ID: "guardian-a"},
		appearance.Preference{Skin: "night", Figure: appearance.Default().Figure}); err != nil {
		t.Fatal(err)
	}
	a, _, _ := pendingLink(t, cw)
	w := contactGet(a, "child-a")
	body := w.Body.String()
	for _, want := range []string{
		`<html lang="de">`, `data-yp-skin="night"`, `<h1>Kontakte von Alex</h1>`, `href="/family"`,
		`name="csrf_token" value="` + sessionA.CSRFToken + `"`, `<label class="yp-field" for="yp-code">`, `id="yp-code" name="code"`,
		`autocomplete="off"`, "beide Familien", `type="checkbox" name="confirm" value="yes" required`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "<script") {
		t.Fatal("contact page must work without JavaScript")
	}
	if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatal("missing security headers")
	}
	start, end := strings.Index(body, "<style>"), strings.Index(body, "</style>")
	style := body[start:end]
	tokens, err := skin.CSSVariables(skin.Catalog()["night"])
	if err != nil {
		t.Fatal(err)
	}
	rest := strings.Replace(style, strings.TrimSuffix(strings.TrimPrefix(tokens, ":root{"), "}"), "", 1)
	if hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).FindAllString(rest, -1); len(hex) != 0 {
		t.Fatalf("hard-coded colors in contact page: %v", hex)
	}
	for _, m := range regexp.MustCompile(`var\((--yp-[a-z-]+)\)`).FindAllStringSubmatch(style, -1) {
		if !strings.Contains(style, m[1]+":") {
			t.Errorf("undeclared token %s", m[1])
		}
	}
	// Without an appearance store the page falls back to the classic skin.
	plain, err := NewContactPages(testAuth{session: sessionA}, cw.store, cw.families, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(contactGet(plain, "child-a").Body.String(), `data-yp-skin="classic"`) {
		t.Fatal("no classic fallback")
	}
	notice := httptest.NewRecorder()
	a.ServeHTTP(notice, httptest.NewRequest(http.MethodGet, "/family/contacts?child_id=child-a&notice=confirmed", nil))
	if !strings.Contains(notice.Body.String(), `role="status"`) {
		t.Fatal("no status message after an action")
	}
	forged := httptest.NewRecorder()
	a.ServeHTTP(forged, httptest.NewRequest(http.MethodGet, "/family/contacts?child_id=child-a&notice=%3Cb%3Ehacked", nil))
	if strings.Contains(forged.Body.String(), "hacked") || strings.Contains(forged.Body.String(), `role="status"`) {
		t.Fatal("arbitrary notice text rendered")
	}
}

func TestFamilyPageLinksToContactsOnlyWhenEnabled(t *testing.T) {
	cw := newContactWorld(t)
	with, err := NewWithOptions(testAuth{session: sessionA}, cw.families, Options{ContactPages: true})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	with.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family", nil))
	if !strings.Contains(w.Body.String(), `href="/family/contacts?child_id=child-a"`) {
		t.Fatal("no contact link on family page")
	}
	without, err := New(testAuth{session: sessionA}, cw.families)
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	without.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family", nil))
	if strings.Contains(w.Body.String(), "/family/contacts") {
		t.Fatal("contact link shown although contact pages are disabled")
	}
}
