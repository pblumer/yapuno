// Command yapuno-demo serves a loopback-only synthetic dashboard.
// It must never be exposed publicly or used with real child data.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
)

func main() {
	port := flag.Int("port", 8085, "local demo port")
	flag.Parse()
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
		children := []familydashboard.Child{{ID: "child-a", TenantID: "demo", DisplayName: "Alex"}, {ID: "child-b", TenantID: "demo", DisplayName: "Sam"}}
		grants := []authz.GuardianGrant{{GuardianID: "demo-guardian", ChildID: "child-a", TenantID: "demo", Active: true, CanManageDevices: true, CanApproveContacts: true}, {GuardianID: "demo-guardian", ChildID: "child-b", TenantID: "demo", Active: true, CanApproveContacts: true}}
		cards := familydashboard.Build("demo-guardian", "demo", children, grants)
		html, err := familydashboard.RenderHTML(cards, r.URL.Query().Get("skin"))
		if err != nil {
			http.Error(w, "Unable to render demo", http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>Yapuno Family Demo</title></head><body><nav aria-label=\"Demo skins\"><a href=\"/?skin=classic\">Classic</a> | <a href=\"/?skin=playful\">Playful</a></nav><p>DEMO ONLY — synthetic data; no login or management actions.</p>", html, "</body></html>")
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("Synthetic demo only: http://%s", listener.Addr())
	log.Fatal(server.Serve(listener))
}
