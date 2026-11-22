// Package pairingweb serves the pages and endpoints a device uses to be
// paired. Nobody is signed in here: a device asks for a short code, shows it,
// and waits until a guardian has entered that code in the guardian area.
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

	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/sameorigin"
)

//go:embed pair.html
var pairHTML []byte

//go:embed pair.js
var pairJS []byte

// lifetime is how long the device should show a code, in seconds.
const lifetime = 300

type Store interface {
	Request(name string, publicDER []byte) (token, code string, err error)
	Nonce(token string) (string, error)
	Finish(token string, signature []byte) (devicepairing.Device, error)
}

type handler struct {
	store Store
}

// New exposes only a narrowly bounded same-origin pairing flow. Nothing here
// creates a guardian or child session, and nothing is bound to a child until
// a signed-in guardian enters the code.
func New(store Store) (http.Handler, error) {
	if store == nil {
		return nil, errors.New("pairing storage required")
	}
	h := &handler{store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /pair", h.page)
	mux.HandleFunc("GET /pair/app.js", h.script)
	mux.HandleFunc("POST /pair/request", h.request)
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
	Token     string `json:"token,omitempty"`
	Name      string `json:"name,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// readRequest accepts a small same-origin JSON body without unknown fields
// or trailing data. These endpoints are unauthenticated: no cross-origin
// call, not even a simple cross-site form post, may reach the backend.
func readRequest(w http.ResponseWriter, r *http.Request) (request, bool) {
	if !sameorigin.Request(r) {
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
	return input, true
}

// tokenRequest is readRequest for the calls a device makes with its token.
func tokenRequest(w http.ResponseWriter, r *http.Request) (request, bool) {
	input, ok := readRequest(w, r)
	if !ok {
		return request{}, false
	}
	decodedToken, err := base64.RawURLEncoding.DecodeString(input.Token)
	if err != nil || len(input.Token) != 43 || len(decodedToken) != 32 {
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
	case errors.Is(err, devicepairing.ErrBusy):
		w.Header().Set("Retry-After", "60")
		http.Error(w, "Too many devices are waiting", http.StatusServiceUnavailable)
	default:
		http.Error(w, "Pairing temporarily unavailable", http.StatusServiceUnavailable)
	}
}

func respond(w http.ResponseWriter, value any) {
	// Encode into a buffer to avoid writing an incomplete response on errors.
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(value); err != nil {
		http.Error(w, "Unable to encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b.Bytes())
}

// request registers the device's key and answers with the device's secret
// token and the code to show. The code is useless without a guardian.
func (h *handler) request(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	input, ok := readRequest(w, r)
	if !ok {
		return
	}
	public, okPub := decode(input.PublicKey, 512)
	if !okPub || input.Token != "" || input.Signature != "" {
		http.Error(w, "Invalid pairing request", http.StatusBadRequest)
		return
	}
	token, code, err := h.store.Request(input.Name, public)
	if err != nil {
		pairingError(w, err)
		return
	}
	respond(w, struct {
		Token     string `json:"token"`
		Code      string `json:"code"`
		ExpiresIn int    `json:"expires_in"`
	}{token, code, lifetime})
}

func (h *handler) nonce(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	input, ok := tokenRequest(w, r)
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
	input, ok := tokenRequest(w, r)
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
