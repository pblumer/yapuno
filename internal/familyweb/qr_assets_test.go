package familyweb

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOfflineQRAssetsAndInvitationCSP(t *testing.T) {
	handler, _ := deviceSetup(t, true)
	for _, path := range []string{"/family/devices/qrcode.js", "/family/devices/qr-render.js"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "javascript") ||
			w.Header().Get("Cache-Control") != "no-store" || len(w.Body.Bytes()) == 0 {
			t.Fatalf("QR asset not served securely %s: status %d", path, w.Code)
		}
	}
	form := url.Values{"csrf_token": {"csrf-test-token"}, "child_id": {"child-a"}}
	w := devicePost(t, handler, "/family/devices/start", form)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "pairing-qr") ||
		!strings.Contains(w.Body.String(), "qr-render.js") ||
		!strings.Contains(w.Body.String(), "Link kopieren") ||
		!strings.Contains(w.Body.String(), "/pair/scan") {
		t.Fatalf("invitation missing local QR UI: %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatal("invitation must allow only local scripts")
	}
	if w.Header().Get("Referrer-Policy") != "no-referrer" ||
		w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("one-time invitation is cacheable or can leak via referrer")
	}
}
