// Package webpush sends content-free Web Push notices to the paired devices
// of a child when somebody wrote to that child.
//
// A push carries no payload: no text, no sender, no conversation and no
// identifier of a child or device. The service worker on the device always
// shows the same fixed sentence. Only the endpoint of a device is stored, and
// only for a device the child signed in with. Before every push the device's
// approval and the child's current right to read the conversation are checked
// again. The server identifies itself to the push service with VAPID
// (RFC 8292), an ES256-signed JWT. See docs/adr/draft-web-push.md.
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
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tokenLifetime is how long a VAPID token is valid. RFC 8292 allows up to a
// day; one hour stays well within what every push service accepts.
const tokenLifetime = time.Hour

const maxKeyFile = 4 << 10

// Key is the server's VAPID signing key.
type Key struct {
	private *ecdsa.PrivateKey
	public  string
	subject string
}

// GenerateKeyFile writes a new P-256 private key as PKCS#8 PEM to a new file
// with mode 0600. It never overwrites an existing file.
func GenerateKeyFile(path string) error {
	if path == "" {
		return ErrInvalid
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		defer d.Close()
		return d.Sync()
	}
	return nil
}

// LoadKey reads a private P-256 key from a private regular file. The subject
// is the server's own HTTPS origin; it is sent to push services as the
// contact of the application server, so no e-mail address is disclosed.
func LoadKey(path, subject string) (*Key, error) {
	origin, err := url.Parse(subject)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || (origin.Path != "" && origin.Path != "/") || origin.RawQuery != "" || origin.Fragment != "" {
		return nil, ErrInvalid
	}
	stat, err := os.Lstat(path)
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 || stat.Size() > maxKeyFile {
		return nil, ErrInvalid
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrInvalid
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, ErrInvalid
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, ErrInvalid
	}
	private, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || private.Curve != elliptic.P256() {
		return nil, ErrInvalid
	}
	public, err := private.PublicKey.ECDH()
	if err != nil {
		return nil, ErrInvalid
	}
	return &Key{private: private, public: base64.RawURLEncoding.EncodeToString(public.Bytes()),
		subject: "https://" + origin.Host}, nil
}

// PublicKey is the uncompressed public point, base64url without padding. A
// browser needs it as applicationServerKey to subscribe; it is not secret.
func (k *Key) PublicKey() string { return k.public }

// authorization returns the Authorization header for one push to endpoint:
// a JWT whose audience is the origin of the push service (RFC 8292).
func (k *Key) authorization(endpoint string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", ErrInvalid
	}
	header, err := json.Marshal(map[string]string{"typ": "JWT", "alg": "ES256"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}{"https://" + u.Host, now.Add(tokenLifetime).Unix(), k.subject})
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	input := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, k.private, digest[:])
	if err != nil {
		return "", err
	}
	// JWS ES256 is the fixed-length concatenation of r and s (RFC 7518).
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return "vapid t=" + input + "." + enc.EncodeToString(signature) + ", k=" + k.public, nil
}

var (
	ErrInvalid     = errors.New("invalid push subscription or key")
	ErrForbidden   = errors.New("device not allowed to subscribe")
	ErrFull        = errors.New("too many push subscriptions")
	ErrUnavailable = errors.New("push subscriptions unavailable")
)
