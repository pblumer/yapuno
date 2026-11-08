package familyweb

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
)

func deviceSetup(t *testing.T, authenticated bool) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	families, err := familystore.Open(filepath.Join(dir, "family.json"))
	if err != nil {
		t.Fatal(err)
	}
	child := familydashboard.Child{ID: "child-a", TenantID: "tenant-a", DisplayName: "Alex"}
	grant := authz.GuardianGrant{GuardianID: "guardian-a", ChildID: "child-a", TenantID: "tenant-a", Active: true, CanManageDevices: true, CanApproveContacts: true}
	if err := families.Bootstrap(child, grant); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "devices.json")
	devices, err := devicepairing.Open(path, families, func() time.Time { return time.Now() })
	if err != nil {
		t.Fatal(err)
	}
	auth := testAuth{session: Session{GuardianID: "guardian-a", TenantID: "tenant-a", CSRFToken: "csrf-test-token"}}
	if !authenticated {
		auth.err = errors.New("no session")
	}
	handler, err := NewDevicePages(auth, devices)
	if err != nil {
		t.Fatal(err)
	}
	return handler, path
}

func devicePost(t *testing.T, handler http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestDeviceViewsAreAuthenticatedAndScoped(t *testing.T) {
	h, _ := deviceSetup(t, false)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/devices?child_id=child-a", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous view %d", w.Code)
	}
	f := url.Values{"csrf_token": {"csrf-test-token"}, "child_id": {"child-a"}}
	if w := devicePost(t, h, "/family/devices/start", f); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous start %d", w.Code)
	}
	h, _ = deviceSetup(t, true)
	for _, child := range []string{"other", "child-b"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/devices?child_id="+child, nil))
		if w.Code != http.StatusForbidden {
			t.Fatalf("wrong child %s: %d", child, w.Code)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/family/devices?child_id=child-a", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Pairing-Code erzeugen") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("device overview: %d", w.Code)
	}
}

func TestDeviceStartRequiresCSRFAndDoesNotPersistToken(t *testing.T) {
	h, path := deviceSetup(t, true)
	form := url.Values{"csrf_token": {"wrong"}, "child_id": {"child-a"}}
	if w := devicePost(t, h, "/family/devices/start", form); w.Code != http.StatusForbidden {
		t.Fatalf("csrf bypass: %d", w.Code)
	}
	form.Set("csrf_token", "csrf-test-token")
	form.Add("child_id", "child-a")
	if w := devicePost(t, h, "/family/devices/start", form); w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate child id accepted: %d", w.Code)
	}
	form.Del("child_id")
	form.Set("child_id", "child-b")
	if w := devicePost(t, h, "/family/devices/start", form); w.Code != http.StatusForbidden {
		t.Fatalf("cross-child start accepted: %d", w.Code)
	}
	form.Set("child_id", "child-a")
	w := devicePost(t, h, "/family/devices/start", form)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Einmal-Code") {
		t.Fatalf("failed start %d", w.Code)
	}
	token := regexp.MustCompile(`<output[^>]*>([^<]+)</output>`).FindStringSubmatch(w.Body.String())
	if len(token) != 2 || len(token[1]) != 43 {
		t.Fatalf("invalid invitation: %v", token)
	}
	state, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), token[1]) {
		t.Fatal("raw secret persisted")
	}
}

func TestDeviceRevokeRequiresExplicitConfirmation(t *testing.T) {
	h, _ := deviceSetup(t, true)
	form := url.Values{"csrf_token": {"csrf-test-token"}, "child_id": {"child-a"}, "device_id": {"some-device"}, "confirm": {"no"}}
	if w := devicePost(t, h, "/family/devices/revoke", form); w.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed revoke: %d", w.Code)
	}
	form.Set("confirm", "yes")
	if w := devicePost(t, h, "/family/devices/revoke", form); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown device revoke: %d", w.Code)
	}
}
