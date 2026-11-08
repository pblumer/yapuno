package familyweb

import (
	_ "embed"
	"net/http"
)

// All QR generation happens on the guardian's device, without remote requests.
//
//go:embed qrcode.vendor.js
var qrVendor []byte

//go:embed qr-render.js
var qrRenderer []byte

func registerQRAssets(mux *http.ServeMux) {
	mux.HandleFunc("GET /family/devices/qrcode.js", func(w http.ResponseWriter, _ *http.Request) {
		headers(w)
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(qrVendor)
	})
	mux.HandleFunc("GET /family/devices/qr-render.js", func(w http.ResponseWriter, _ *http.Request) {
		headers(w)
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(qrRenderer)
	})
}
