package pairingweb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"path/filepath"

	"github.com/pblumer/yapuno/internal/devicepairing"
)

func TestLocalScannerAssetsAndFallback(t *testing.T) {
	store, err := devicepairing.Open(filepath.Join(t.TempDir(), "device.json"), testFamily{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/pair/scan", "/pair/scan.js", "/pair/scan-utils.js"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://app.example.org"+path, nil))
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" ||
			w.Header().Get("Referrer-Policy") != "no-referrer" ||
			!strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") {
			t.Fatalf("unsafe scanner route %s: status %d", path, w.Code)
		}
		if len(w.Body.Bytes()) == 0 {
			t.Fatalf("empty scanner route %s", path)
		}
		if path == "/pair/scan" && (!strings.Contains(w.Body.String(), "Kamera starten") ||
			!strings.Contains(w.Body.String(), "normale Kamera-App") ||
			!strings.Contains(w.Body.String(), "/pair/scan-utils.js")) {
			t.Fatal("scan page has no camera or fallback")
		}
	}
}
