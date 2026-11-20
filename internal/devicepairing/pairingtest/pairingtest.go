// Package pairingtest provides test helpers that run the real guardian-approved
// pairing flow with a software P-256 key. It must only be imported by tests.
package pairingtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"testing"

	"github.com/pblumer/yapuno/internal/devicepairing"
)

// Sign returns an ASN.1 ECDSA signature over SHA-256(message).
func Sign(t testing.TB, key *ecdsa.PrivateKey, message string) []byte {
	t.Helper()
	digest := sha256.Sum256([]byte(message))
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// LoginProof signs a child device login challenge like the browser client.
func LoginProof(t testing.TB, key *ecdsa.PrivateKey, deviceID, nonce string) []byte {
	t.Helper()
	return Sign(t, key, "yapuno-device-login-v1\x00"+deviceID+"."+nonce)
}

// Pair claims, approves and finishes a pairing for one child and returns the
// new device id with its private key.
func Pair(t testing.TB, store *devicepairing.Store, guardian, tenant, child string) (string, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.Start(guardian, tenant, child)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := store.Claim(token, "Test phone", der, Sign(t, key, "yapuno-pair-claim-v1\x00"+token))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Approve(guardian, tenant, child, token, fingerprint); err != nil {
		t.Fatal(err)
	}
	nonce, err := store.Nonce(token)
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.Finish(token, Sign(t, key, "yapuno-pair-finish-v1\x00"+nonce))
	if err != nil {
		t.Fatal(err)
	}
	return device.ID, key
}
