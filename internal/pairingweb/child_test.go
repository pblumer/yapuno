package pairingweb

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/familydashboard"
)

type testFamily struct{}

func (testFamily) Cards(guardian, tenant string) []familydashboard.ChildCard {
	if guardian != "parent" || tenant != "tenant" {
		return nil
	}
	return []familydashboard.ChildCard{{ID: "child", Permissions: familydashboard.Permissions{ManageDevices: true}}}
}

// origin is what Chromium sends; webkit is what Safari and every browser on
// an iPhone can send from these no-referrer pages.
var (
	origin = map[string]string{"Origin": "https://app.example.org"}
	webkit = map[string]string{"Origin": "null", "Sec-Fetch-Site": "same-origin"}
)

func api(t *testing.T, handler http.Handler, path string, payload any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://app.example.org"+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func newKey(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return private, base64.RawURLEncoding.EncodeToString(public)
}

func sign(t *testing.T, key *ecdsa.PrivateKey, domain, value string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(domain + value))
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(sig)
}

func setup(t *testing.T) (http.Handler, *devicepairing.Store) {
	t.Helper()
	store, err := devicepairing.Open(filepath.Join(t.TempDir(), "pairing.json"), testFamily{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store
}

type answer struct {
	Token     string `json:"token"`
	Code      string `json:"code"`
	ExpiresIn int    `json:"expires_in"`
	Nonce     string `json:"nonce"`
	DeviceID  string `json:"device_id"`
	Status    string `json:"status"`
}

func read(t *testing.T, w *httptest.ResponseRecorder) answer {
	t.Helper()
	var out answer
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("answer %q: %v", w.Body, err)
	}
	return out
}

func TestADeviceGetsACodeAndIsBoundAfterTheGuardianEntersIt(t *testing.T) {
	for name, headers := range map[string]map[string]string{"chromium": origin, "iphone": webkit} {
		handler, store := setup(t)
		private, public := newKey(t)
		w := api(t, handler, "/pair/request", request{Name: "iPhone", PublicKey: public}, headers)
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("%s request: %d %v %s", name, w.Code, w.Header(), w.Body)
		}
		got := read(t, w)
		if len(got.Token) != 43 || !regexp.MustCompile(`^[0-9]{6}$`).MatchString(got.Code) || got.ExpiresIn != 300 {
			t.Fatalf("%s request answer: %+v", name, got)
		}
		// The answer holds nothing but the token, the code and the lifetime.
		if strings.Contains(w.Body.String(), public) || strings.Contains(w.Body.String(), "fingerprint") {
			t.Fatalf("%s: answer discloses more: %s", name, w.Body)
		}
		// Until a guardian enters the code the device waits.
		if w := api(t, handler, "/pair/nonce", request{Token: got.Token}, headers); w.Code != http.StatusAccepted || read(t, w).Status != "waiting" {
			t.Fatalf("%s before approval: %d %s", name, w.Code, w.Body)
		}
		if w := api(t, handler, "/pair/finish", request{Token: got.Token, Signature: sign(t, private, "yapuno-pair-finish-v1\x00", "x")}, headers); w.Code != http.StatusAccepted {
			t.Fatalf("%s finish before approval: %d", name, w.Code)
		}
		if deviceName, err := store.Approve("parent", "tenant", "child", got.Code); err != nil || deviceName != "iPhone" {
			t.Fatalf("%s approve: %q %v", name, deviceName, err)
		}
		w = api(t, handler, "/pair/nonce", request{Token: got.Token}, headers)
		nonce := read(t, w).Nonce
		if w.Code != http.StatusOK || len(nonce) != 43 {
			t.Fatalf("%s nonce: %d %s", name, w.Code, w.Body)
		}
		// Another key cannot take the approval over.
		other, _ := newKey(t)
		if w := api(t, handler, "/pair/finish", request{Token: got.Token, Signature: sign(t, other, "yapuno-pair-finish-v1\x00", nonce)}, headers); w.Code != http.StatusBadRequest {
			t.Fatalf("%s finish with another key: %d", name, w.Code)
		}
		w = api(t, handler, "/pair/finish", request{Token: got.Token, Signature: sign(t, private, "yapuno-pair-finish-v1\x00", nonce)}, headers)
		id := read(t, w).DeviceID
		if w.Code != http.StatusOK || id == "" {
			t.Fatalf("%s finish: %d %s", name, w.Code, w.Body)
		}
		if _, ok := store.ActiveDevice("tenant", "child", id); !ok {
			t.Fatalf("%s: device not active", name)
		}
		if w := api(t, handler, "/pair/finish", request{Token: got.Token, Signature: sign(t, private, "yapuno-pair-finish-v1\x00", nonce)}, headers); w.Code != http.StatusConflict {
			t.Fatalf("%s finish twice: %d", name, w.Code)
		}
	}
}

func TestRequestsFromOtherSitesAreRefused(t *testing.T) {
	handler, _ := setup(t)
	_, public := newKey(t)
	for name, headers := range map[string]map[string]string{
		"no origin":                      {},
		"other origin":                   {"Origin": "https://attacker.example.org"},
		"null origin without metadata":   {"Origin": "null"},
		"null origin from another site":  {"Origin": "null", "Sec-Fetch-Site": "cross-site"},
		"right origin from another site": {"Origin": "https://app.example.org", "Sec-Fetch-Site": "cross-site"},
		"metadata from a sibling host":   {"Sec-Fetch-Site": "same-site"},
	} {
		for _, path := range []string{"/pair/request", "/pair/nonce", "/pair/finish"} {
			if w := api(t, handler, path, request{Name: "iPhone", PublicKey: public, Token: strings.Repeat("A", 43)}, headers); w.Code != http.StatusForbidden {
				t.Errorf("%s %s: %d", name, path, w.Code)
			}
		}
	}
}

func TestBadPairingInputIsRefused(t *testing.T) {
	handler, _ := setup(t)
	_, public := newKey(t)
	post := func(path, contentType, body string) int {
		req := httptest.NewRequest(http.MethodPost, "https://app.example.org"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Origin", "https://app.example.org")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}
	good := `{"name":"iPhone","public_key":"` + public + `"}`
	if got := post("/pair/request", "application/json", good); got != http.StatusOK {
		t.Fatalf("precondition: %d", got)
	}
	for name, c := range map[string]struct {
		path, contentType, body string
		want                    int
	}{
		"form body":             {"/pair/request", "application/x-www-form-urlencoded", "name=x", http.StatusUnsupportedMediaType},
		"unknown field":         {"/pair/request", "application/json", `{"name":"iPhone","public_key":"` + public + `","child":"x"}`, http.StatusBadRequest},
		"trailing data":         {"/pair/request", "application/json", good + good, http.StatusBadRequest},
		"no key":                {"/pair/request", "application/json", `{"name":"iPhone"}`, http.StatusBadRequest},
		"key that is not a key": {"/pair/request", "application/json", `{"name":"iPhone","public_key":"AAAA"}`, http.StatusBadRequest},
		"no name":               {"/pair/request", "application/json", `{"public_key":"` + public + `"}`, http.StatusBadRequest},
		"markup in name":        {"/pair/request", "application/json", `{"name":"<b>x</b>","public_key":"` + public + `"}`, http.StatusBadRequest},
		"token on a request":    {"/pair/request", "application/json", `{"name":"iPhone","public_key":"` + public + `","token":"` + strings.Repeat("A", 43) + `"}`, http.StatusBadRequest},
		"oversized":             {"/pair/request", "application/json", `{"name":"` + strings.Repeat("x", 5000) + `"}`, http.StatusBadRequest},
		"short token":           {"/pair/nonce", "application/json", `{"token":"abc"}`, http.StatusBadRequest},
		"unknown token":         {"/pair/nonce", "application/json", `{"token":"` + strings.Repeat("A", 43) + `"}`, http.StatusBadRequest},
		"name on a nonce":       {"/pair/nonce", "application/json", `{"token":"` + strings.Repeat("A", 43) + `","name":"x"}`, http.StatusBadRequest},
		"finish without proof":  {"/pair/finish", "application/json", `{"token":"` + strings.Repeat("A", 43) + `"}`, http.StatusBadRequest},
	} {
		if got := post(c.path, c.contentType, c.body); got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
	for _, path := range []string{"/pair/request", "/pair/nonce", "/pair/finish"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://app.example.org"+path, nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: %d", path, w.Code)
		}
	}
}

// busy refuses every request like a store with too many open pairings.
type busy struct{ Store }

func (busy) Request(string, []byte) (string, string, error) {
	return "", "", devicepairing.ErrBusy
}

func TestTooManyOpenRequestsAskTheDeviceToWait(t *testing.T) {
	_, store := setup(t)
	handler, err := New(busy{store})
	if err != nil {
		t.Fatal(err)
	}
	_, public := newKey(t)
	w := api(t, handler, "/pair/request", request{Name: "iPhone", PublicKey: public}, origin)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Fatalf("busy: %d %v", w.Code, w.Header())
	}
	if _, err := New(nil); err == nil {
		t.Fatal("nil store accepted")
	}
	if !errors.Is(devicepairing.ErrBusy, devicepairing.ErrBusy) {
		t.Fatal("unreachable")
	}
}

func TestThePairingPageShowsACodeAndNothingOfTheOldFlow(t *testing.T) {
	handler, _ := setup(t)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://app.example.org"+path, nil))
		return w
	}
	page := get("/pair")
	body := page.Body.String()
	csp := page.Header().Get("Content-Security-Policy")
	if page.Code != http.StatusOK || page.Header().Get("Cache-Control") != "no-store" || page.Header().Get("Referrer-Policy") != "no-referrer" ||
		!strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-eval") {
		t.Fatalf("page: %d %v", page.Code, page.Header())
	}
	for _, want := range []string{`<html lang="de">`, "width=device-width", "<h1>Gerät verbinden</h1>", `id="code"`, "Zeig diesen Code deinen Eltern", "5 Minuten",
		`<script src="/pair/app.js" defer></script>`, `href="/child"`, "<noscript>", `aria-live="polite"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// No name to type, no fingerprint to compare, no QR code, no inline script.
	for _, absent := range []string{"Fingerabdruck", "fingerprint", "QR", "/pair/scan", "<input", "<script>", "onclick"} {
		if strings.Contains(body, absent) {
			t.Errorf("page contains %q", absent)
		}
	}
	script := get("/pair/app.js")
	if script.Code != http.StatusOK || !strings.HasPrefix(script.Header().Get("Content-Type"), "text/javascript") || script.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("script: %d %v", script.Code, script.Header())
	}
	js := script.Body.String()
	for _, want := range []string{`"/pair/request"`, `"/pair/nonce"`, `"/pair/finish"`, "extractable", "yapuno-pair-finish-v1"} {
		if !strings.Contains(js, want) && !(want == "extractable" && strings.Contains(js, "generateKey")) {
			t.Errorf("script lacks %q", want)
		}
	}
	// The key is generated non-extractable and the code is set as text only.
	if !regexp.MustCompile(`generateKey\([^)]*\},\s*false,`).MatchString(js) || strings.Contains(js, "innerHTML") || strings.Contains(js, "/pair/claim") {
		t.Error("script: key must be non-extractable, no innerHTML, no old endpoint")
	}
	for _, gone := range []string{"/pair/scan", "/pair/scan.js", "/pair/scan-utils.js", "/pair/claim"} {
		if w := get(gone); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s still served: %d", gone, w.Code)
		}
	}
}
