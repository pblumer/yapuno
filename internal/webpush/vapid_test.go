package webpush

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newKey(t *testing.T) (*Key, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vapid.pem")
	if err := GenerateKeyFile(path); err != nil {
		t.Fatal(err)
	}
	key, err := LoadKey(path, "https://app.example.org")
	if err != nil {
		t.Fatal(err)
	}
	return key, path
}

func TestGeneratedKeyFileIsPrivateAndNeverOverwritten(t *testing.T) {
	_, path := newKey(t)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("key file mode: %v %v", info.Mode(), err)
	}
	before, _ := os.ReadFile(path)
	if err := GenerateKeyFile(path); err == nil {
		t.Fatal("an existing key was overwritten")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("key file changed")
	}
}

func TestLoadKeyFailsClosed(t *testing.T) {
	_, path := newKey(t)
	dir := t.TempDir()
	write := func(name string, data []byte, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good, _ := os.ReadFile(path)
	other, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(other)
	link := filepath.Join(dir, "link.pem")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{
		"readable by group": write("group.pem", good, 0640),
		"not PEM":           write("junk.pem", []byte("nope"), 0600),
		"wrong curve":       write("p384.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600),
		"trailing data":     write("trail.pem", append(append([]byte{}, good...), good...), 0600),
		"symbolic link":     link,
		"missing":           filepath.Join(dir, "missing.pem"),
	} {
		if _, err := LoadKey(p, "https://app.example.org"); err == nil {
			t.Errorf("%s: key accepted", name)
		}
	}
	for _, subject := range []string{"", "http://app.example.org", "https://app.example.org/path", "mailto:someone@example.org"} {
		if _, err := LoadKey(path, subject); err == nil {
			t.Errorf("subject %q accepted", subject)
		}
	}
}

func TestPublicKeyIsTheUncompressedP256Point(t *testing.T) {
	key, _ := newKey(t)
	raw, err := base64.RawURLEncoding.DecodeString(key.PublicKey())
	if err != nil || len(raw) != 65 || raw[0] != 4 {
		t.Fatalf("public key: %d bytes, %v", len(raw), err)
	}
	x, y := new(big.Int).SetBytes(raw[1:33]), new(big.Int).SetBytes(raw[33:])
	if !elliptic.P256().IsOnCurve(x, y) {
		t.Fatal("public key is not on P-256")
	}
}

func TestAuthorizationIsAVerifiableES256Token(t *testing.T) {
	key, _ := newKey(t)
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	header, err := key.authorization("https://fcm.googleapis.com/fcm/send/abc", now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(header, "vapid t=") || !strings.HasSuffix(header, ", k="+key.PublicKey()) {
		t.Fatalf("header: %s", header)
	}
	token := strings.TrimSuffix(strings.TrimPrefix(header, "vapid t="), ", k="+key.PublicKey())
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts", len(parts))
	}
	decode := func(s string, v any) {
		raw, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatal(err)
		}
	}
	var head map[string]string
	decode(parts[0], &head)
	if head["alg"] != "ES256" || head["typ"] != "JWT" || len(head) != 2 {
		t.Fatalf("header: %v", head)
	}
	var claims map[string]any
	decode(parts[1], &claims)
	if claims["aud"] != "https://fcm.googleapis.com" || claims["sub"] != "https://app.example.org" || claims["exp"] != float64(now.Add(time.Hour).Unix()) || len(claims) != 3 {
		t.Fatalf("claims: %v", claims)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("signature: %d bytes", len(sig))
	}
	raw, _ := base64.RawURLEncoding.DecodeString(key.PublicKey())
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(raw[1:33]), Y: new(big.Int).SetBytes(raw[33:])}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("signature does not verify")
	}
	// The audience is the push service, not the rest of the endpoint.
	if _, err := key.authorization("ftp://fcm.googleapis.com/x", now); err == nil {
		t.Fatal("token for a non-HTTPS endpoint")
	}
}
