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
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

func api(t *testing.T, handler http.Handler, path string, payload any, origin string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://app.example.org"+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
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

func TestChildPairingHTTPCompletesOnlyAfterGuardianApproval(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	store, err := devicepairing.Open(filepath.Join(t.TempDir(), "pairing.json"), testFamily{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.Start("parent", "tenant", "child")
	if err != nil {
		t.Fatal(err)
	}
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	payload := request{
		Token: token, Name: "Kindertelefon",
		PublicKey: base64.RawURLEncoding.EncodeToString(public),
		Signature: sign(t, private, "yapuno-pair-claim-v1\x00", token),
	}
	if got := api(t, handler, "/pair/claim", payload, "").Code; got != http.StatusForbidden {
		t.Fatalf("cross-site request allowed without Origin: %d", got)
	}
	if got := api(t, handler, "/pair/claim", payload, "https://attacker.example.org").Code; got != http.StatusForbidden {
		t.Fatalf("cross-site request allowed with wrong Origin: %d", got)
	}
	w := api(t, handler, "/pair/claim", payload, "https://app.example.org")
	if w.Code != http.StatusOK {
		t.Fatalf("claim: %d %s", w.Code, w.Body.String())
	}
	var claimed struct{ Fingerprint string }
	if err := json.Unmarshal(w.Body.Bytes(), &claimed); err != nil || len(claimed.Fingerprint) != 64 {
		t.Fatalf("claim output: %+v %v", claimed, err)
	}
	onlyToken := request{Token: token}
	if got := api(t, handler, "/pair/nonce", onlyToken, "https://app.example.org").Code; got != http.StatusAccepted {
		t.Fatalf("nonce before approval: %d", got)
	}
	if err := store.Approve("parent", "tenant", "child", token, claimed.Fingerprint); err != nil {
		t.Fatal(err)
	}
	w = api(t, handler, "/pair/nonce", onlyToken, "https://app.example.org")
	if w.Code != http.StatusOK {
		t.Fatalf("nonce after approval: %d %s", w.Code, w.Body.String())
	}
	var nonce struct{ Nonce string }
	if err := json.Unmarshal(w.Body.Bytes(), &nonce); err != nil || len(nonce.Nonce) != 43 {
		t.Fatalf("invalid nonce: %+v %v", nonce, err)
	}
	wrong, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if got := api(t, handler, "/pair/finish", request{Token: token, Signature: sign(t, wrong, "yapuno-pair-finish-v1\x00", nonce.Nonce)}, "https://app.example.org").Code; got != http.StatusBadRequest {
		t.Fatalf("wrong private key accepted: %d", got)
	}
	finish := request{Token: token, Signature: sign(t, private, "yapuno-pair-finish-v1\x00", nonce.Nonce)}
	w = api(t, handler, "/pair/finish", finish, "https://app.example.org")
	if w.Code != http.StatusOK {
		t.Fatalf("finish: %d %s", w.Code, w.Body.String())
	}
	var bound struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &bound); err != nil || bound.DeviceID == "" {
		t.Fatalf("missing device ID: %+v %v", bound, err)
	}
	if _, ok := store.ActiveDevice("tenant", "child", bound.DeviceID); !ok {
		t.Fatal("device not active after durable finish")
	}
	if got := api(t, handler, "/pair/finish", finish, "https://app.example.org").Code; got != http.StatusConflict {
		t.Fatalf("replay accepted: %d", got)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("pairing secrets may be cached or sent as referrers")
	}
}

func TestPairingRoutesAndUnsafeInput(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("accepted nil pairing store")
	}
	store, err := devicepairing.Open(filepath.Join(t.TempDir(), "pairing.json"), testFamily{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/pair", "/pair/app.js"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://app.example.org"+path, nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("unsafe public assets %s: status=%d", path, w.Code)
		}
	}
	w := api(t, handler, "/pair/claim", request{Token: "short"}, "https://app.example.org")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid token accepted: %d", w.Code)
	}
	token, err := store.Start("parent", "tenant", "child")
	if err != nil {
		t.Fatal(err)
	}
	w = api(t, handler, "/pair/nonce", request{Token: token, Name: "unexpected"}, "https://app.example.org")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("extra fields accepted: %d", w.Code)
	}
	w = api(t, handler, "/pair/claim", request{Token: token, PublicKey: "bad", Signature: "bad"}, "https://app.example.org")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid key accepted: %d", w.Code)
	}
}
