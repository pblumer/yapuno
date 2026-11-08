// Package pairingweb serves the child-side pairing proof-of-possession flow.
// It never creates sessions or grants contact or conversation permissions.
package pairingweb

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/pblumer/yapuno/internal/devicepairing"
)

//go:embed pair.html
var pairHTML []byte

//go:embed pair.js
var pairJS []byte

type Store interface {
	Claim(token, name string, publicDER, claimSignature []byte) (string, error)
	Nonce(token string) (string, error)
	Finish(token string, signature []byte) (devicepairing.Device, error)
}

type handler struct {
	store Store
}

// New exposes only a narrowly bounded same-origin pairing flow. Neither a
// guardian nor a child session can be minted from a pairing token alone.
func New(store Store) (http.Handler, error) {
	if store == nil {
		return nil, errors.New("pairing storage required")
	}
	h := &handler{store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /pair", h.page)
	mux.HandleFunc("GET /pair/app.js", h.script)
	mux.HandleFunc("POST /pair/claim", h.claim)
	mux.HandleFunc("POST /pair/nonce", h.nonce)
	mux.HandleFunc("POST /pair/finish", h.finish)
	return mux, nil
}

func securityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; connect-src 'self'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
}

func (h *handler) page(w http.ResponseWriter, _ *http.Request) {
	securityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(pairHTML)
}

func (h *handler) script(w http.ResponseWriter, _ *http.Request) {
	securityHeaders(w)
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write(pairJS)
}

type request struct {
	Token     string `json:"token"`
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"`
}

func readRequest(w http.ResponseWriter, r *http.Request) (request, bool) {
	// This is an unauthenticated, token-bound API. No cross-origin calls,
	// even simple cross-site form posts, may reach the backend.
	if r.Header.Get("Origin") != "https://"+r.Host {
		http.Error(w, "Same-origin HTTPS required", http.StatusForbidden)
		return request{}, false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
		return request{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var input request
	if err := dec.Decode(&input); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return request{}, false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return request{}, false
	}
	if len(input.Token) != 43 || !strings.ContainsAny(input.Token, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") {
		// Full base64url validation is done by devicepairing.Store.
		http.Error(w, "Invalid pairing token", http.StatusBadRequest)
		return request{}, false
	}
	return input, true
}

func decode(data string, max int) ([]byte, bool) {
	if data == "" || len(data) > max*2 {
		return nil, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(data)
	return decoded, err == nil && len(decoded) <= max
}

func pairingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, devicepairing.ErrNotReady):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":"waiting"}`)
	case errors.Is(err, devicepairing.ErrExpired):
		http.Error(w, "Pairing expired", http.StatusGone)
	case errors.Is(err, devicepairing.ErrConsumed):
		http.Error(w, "Pairing already used", http.StatusConflict)
	case errors.Is(err, devicepairing.ErrForbidden):
		http.Error(w, "Pairing not allowed", http.StatusForbidden)
	case errors.Is(err, devicepairing.ErrInvalid):
		http.Error(w, "Invalid pairing request", http.StatusBadRequest)
	default:
		http.Error(w, "Pairing temporarily unavailable", http.StatusServiceUnavailable)
	}
}

func respond(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	// Encode into a buffer to avoid writing an incomplete response on errors.
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(value); err != nil {
		http.Error(w, "Unable to encode response", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(b.Bytes())
}

func (h *handler) claim(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	input, ok := readRequest(w, r)
	if !ok {
		return
	}
	public, okPub := decode(input.PublicKey, 512)
	sig, okSig := decode(input.Signature, 256)
	if !okPub || !okSig {
		http.Error(w, "Invalid public key or signature", http.StatusBadRequest)
		return
	}
	fingerprint, err := h.store.Claim(input.Token, input.Name, public, sig)
	if err != nil {
		pairingError(w, err)
		return
	}
	respond(w, map[string]string{"fingerprint": fingerprint})
}

func (h *handler) nonce(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	input, ok := readRequest(w, r)
	if !ok {
		return
	}
	if input.Name != "" || input.PublicKey != "" || input.Signature != "" {
		http.Error(w, "Unexpected fields", http.StatusBadRequest)
		return
	}
	nonce, err := h.store.Nonce(input.Token)
	if err != nil {
		pairingError(w, err)
		return
	}
	respond(w, map[string]string{"nonce": nonce})
}

func (h *handler) finish(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	input, ok := readRequest(w, r)
	if !ok {
		return
	}
	if input.Name != "" || input.PublicKey != "" {
		http.Error(w, "Unexpected fields", http.StatusBadRequest)
		return
	}
	signature, ok := decode(input.Signature, 256)
	if !ok {
		http.Error(w, "Invalid signature", http.StatusBadRequest)
		return
	}
	device, err := h.store.Finish(input.Token, signature)
	if err != nil {
		pairingError(w, err)
		return
	}
	respond(w, map[string]string{"device_id": device.ID})
}
