package familyweb

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
)

type testAuth struct {
	session Session
	err     error
}

func (a testAuth) Authenticate(*http.Request) (Session, error) { return a.session, a.err }

func setup(t *testing.T, auth testAuth) http.Handler {
	t.Helper()
	s, err := familystore.Open(filepath.Join(t.TempDir(), "family.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range []struct {
		id, tenant, guardian, name string
	}{
		{"child-a", "tenant-a", "guardian-a", "Alex"},
		{"child-b", "tenant-b", "guardian-b", "Bea"},
	} {
		grant := authz.GuardianGrant{GuardianID: child.guardian, ChildID: child.id, TenantID: child.tenant, Active: true, CanManageDevices: true, CanApproveContacts: true}
		if err := s.Bootstrap(familydashboard.Child{ID: child.id, TenantID: child.tenant, DisplayName: child.name}, grant); err != nil {
			t.Fatal(err)
		}
	}
	h, err := New(auth, s)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func post(t *testing.T, handler http.Handler, fields url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/family/rename", strings.NewReader(fields.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestRejectUnauthenticated(t *testing.T) {
	h := setup(t, testAuth{err: errors.New("no session")})
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r := httptest.NewRequest(method, "/family", nil)
		if method == http.MethodPost {
			r.URL.Path = "/family/rename"
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: got %d", method, w.Code)
		}
	}
}

func TestAuthorizedViewAndRenameAreTenantScoped(t *testing.T) {
	h := setup(t, testAuth{session: Session{GuardianID: "guardian-a", TenantID: "tenant-a", CSRFToken: "session-opaque-token"}})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family?skin=playful", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Alex") || strings.Contains(w.Body.String(), "Bea") || !strings.Contains(w.Body.String(), `data-yp-skin="playful"`) {
		t.Fatalf("dashboard not properly scoped: status=%d", w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("sensitive response headers missing")
	}
	fields := url.Values{"csrf_token": {"session-opaque-token"}, "child_id": {"child-b"}, "name": {"Stolen"}}
	if got := post(t, h, fields).Code; got != http.StatusForbidden {
		t.Fatalf("cross-tenant mutation: status %d", got)
	}
	fields.Set("child_id", "child-a")
	fields.Set("name", "Nina")
	if got := post(t, h, fields).Code; got != http.StatusSeeOther {
		t.Fatalf("authorized mutation: status %d", got)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family", nil))
	if !strings.Contains(w.Body.String(), "Nina") {
		t.Fatalf("updated name missing")
	}
}

func TestRejectCSRFAndInvalidForms(t *testing.T) {
	h := setup(t, testAuth{session: Session{GuardianID: "guardian-a", TenantID: "tenant-a", CSRFToken: "secret"}})
	fields := url.Values{"csrf_token": {"incorrect"}, "child_id": {"child-a"}, "name": {"Nina"}}
	if got := post(t, h, fields).Code; got != http.StatusForbidden {
		t.Fatalf("CSRF bypass: %d", got)
	}
	fields.Set("csrf_token", "secret")
	fields.Add("child_id", "child-b")
	if got := post(t, h, fields).Code; got != http.StatusBadRequest {
		t.Fatalf("duplicate form field accepted: %d", got)
	}
}

func TestNoAuthenticatorOrStore(t *testing.T) {
	if _, err := New(nil, nil); err == nil {
		t.Fatal("must fail closed on missing dependencies")
	}
}
