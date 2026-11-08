package familyweb

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/url"
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

func TestGuardianReviewsExactDeviceFingerprintAndApproves(t *testing.T) {
	dir := t.TempDir()
	families, err := familystore.Open(filepath.Join(dir, "family.json"))
	if err != nil {
		t.Fatal(err)
	}
	child := familydashboard.Child{ID: "c1", TenantID: "t1", DisplayName: "Alex"}
	grant := authz.GuardianGrant{GuardianID: "g1", ChildID: "c1", TenantID: "t1", Active: true, CanManageDevices: true, CanApproveContacts: true}
	if err := families.Bootstrap(child, grant); err != nil {
		t.Fatal(err)
	}
	devices, err := devicepairing.Open(filepath.Join(dir, "pairing.json"), families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	auth := testAuth{session: Session{GuardianID: "g1", TenantID: "t1", CSRFToken: "session-csrf"}}
	handler, err := NewDevicePages(auth, devices, "https://app.example.org")
	if err != nil {
		t.Fatal(err)
	}
	baseForm := url.Values{"csrf_token": {"session-csrf"}, "child_id": {"c1"}}
	w := devicePost(t, handler, "/family/devices/start", baseForm)
	if w.Code != http.StatusOK {
		t.Fatalf("start: %d", w.Code)
	}
	m := regexp.MustCompile(`<output[^>]*>([^<]+)</output>`).FindStringSubmatch(w.Body.String())
	if len(m) != 2 || len(m[1]) != 43 || !strings.Contains(w.Body.String(), "/pair#") {
		t.Fatalf("missing private fragment invitation: %s", w.Body.String())
	}
	token := m[1]
	status := url.Values{"csrf_token": {"session-csrf"}, "child_id": {"c1"}, "token": {token}}
	w = devicePost(t, handler, "/family/devices/status", status)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "noch keine Kopplungsanfrage") {
		t.Fatalf("unclaimed status: %d %s", w.Code, w.Body.String())
	}
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("yapuno-pair-claim-v1\x00" + token))
	signature, err := ecdsa.SignASN1(rand.Reader, private, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	fp, err := devices.Claim(token, "Kindergerät", public, signature)
	if err != nil {
		t.Fatal(err)
	}
	w = devicePost(t, handler, "/family/devices/status", status)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), fp) {
		t.Fatalf("fingerprint not shown to guardian: %d", w.Code)
	}
	approval := url.Values{"csrf_token": {"session-csrf"}, "child_id": {"c1"}, "token": {token}, "fingerprint": {fp}, "confirm": {"no"}}
	if got := devicePost(t, handler, "/family/devices/approve", approval).Code; got != http.StatusBadRequest {
		t.Fatalf("missing consent accepted: %d", got)
	}
	approval.Set("confirm", "yes")
	approval.Set("csrf_token", "forged")
	if got := devicePost(t, handler, "/family/devices/approve", approval).Code; got != http.StatusForbidden {
		t.Fatalf("forged CSRF accepted: %d", got)
	}
	approval.Set("csrf_token", "session-csrf")
	approval.Set("child_id", "other-child")
	if got := devicePost(t, handler, "/family/devices/approve", approval).Code; got != http.StatusForbidden {
		t.Fatalf("wrong child approval accepted: %d", got)
	}
	approval.Set("child_id", "c1")
	approval.Set("fingerprint", strings.Repeat("a", 64))
	if got := devicePost(t, handler, "/family/devices/approve", approval).Code; got != http.StatusConflict {
		t.Fatalf("wrong fingerprint accepted: %d", got)
	}
	approval.Set("fingerprint", fp)
	w = devicePost(t, handler, "/family/devices/approve", approval)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "freigegeben") {
		t.Fatalf("approval failed: %d %s", w.Code, w.Body.String())
	}
	nonce, err := devices.Nonce(token)
	if err != nil || len(nonce) != 43 {
		t.Fatalf("post approval nonce: %s %v", nonce, err)
	}
	finishHash := sha256.Sum256([]byte("yapuno-pair-finish-v1\x00" + nonce))
	finishSig, err := ecdsa.SignASN1(rand.Reader, private, finishHash[:])
	if err != nil {
		t.Fatal(err)
	}
	d, err := devices.Finish(token, finishSig)
	if err != nil || d.ID == "" || base64.RawURLEncoding.EncodeToString(public) != d.PublicKey {
		t.Fatalf("unbound device after approval: %+v %v", d, err)
	}
	if _, ok := devices.ActiveDevice("t1", "c1", d.ID); !ok {
		t.Fatal("device is not active after approved finish")
	}
}
