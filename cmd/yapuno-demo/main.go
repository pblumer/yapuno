// Command yapuno-demo serves a loopback-only synthetic dashboard.
// It must never be exposed publicly or used with real child data.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"html/template"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
)

type demoState struct {
	mu sync.Mutex
	children []familydashboard.Child
	token string
}

func newDemoState() *demoState {
	var secret [24]byte
	if _, err := rand.Read(secret[:]); err != nil {
		panic(err)
	}
	return &demoState{
		children: []familydashboard.Child{{ID: "child-a", TenantID: "demo", DisplayName: "Alex"}, {ID: "child-b", TenantID: "demo", DisplayName: "Sam"}},
		token: hex.EncodeToString(secret[:]),
	}
}

func (s *demoState) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.view)
	mux.HandleFunc("POST /profiles", s.add)
	return mux
}

func (s *demoState) add(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > 2048 {
		http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.PostForm.Get("token") != s.token {
		http.Error(w, "Invalid demo token", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	if len([]rune(name)) < 1 || len([]rune(name)) > 40 || strings.ContainsAny(name, "\r\n<>") {
		http.Error(w, "Name must be 1-40 characters", http.StatusBadRequest)
		return
	}
	if len(s.children) >= 8 {
		http.Error(w, "Demo profile limit reached", http.StatusConflict)
		return
	}
	s.children = append(s.children, familydashboard.Child{ID: fmt.Sprintf("child-%d", len(s.children)+1), TenantID: "demo", DisplayName: name})
	http.Redirect(w, r, "/?skin="+r.PostForm.Get("skin"), http.StatusSeeOther)
}

func (s *demoState) view(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	s.mu.Lock()
	children := append([]familydashboard.Child(nil), s.children...)
	token := s.token
	s.mu.Unlock()
	grants := make([]authz.GuardianGrant, 0, len(children))
	for _, child := range children {
		grants = append(grants, authz.GuardianGrant{GuardianID: "demo-guardian", ChildID: child.ID, TenantID: "demo", Active: true, CanManageDevices: true, CanApproveContacts: true})
	}
	cards := familydashboard.Build("demo-guardian", "demo", children, grants)
	skinID := r.URL.Query().Get("skin")
	html, err := familydashboard.RenderHTML(cards, skinID)
	if err != nil {
		http.Error(w, "Unable to render demo", http.StatusInternalServerError)
		return
	}
	const page = `<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Yapuno Family Demo</title></head><body><nav aria-label="Demo Skins"><a href="/?skin=classic">Classic</a> | <a href="/?skin=playful">Playful</a></nav><p>DEMO ONLY – nur Beispieldaten, keine Anmeldung oder echte Verwaltung.</p><form action="/profiles" method="post"><label for="name">Demo-Kinderprofil hinzufügen</label> <input id="name" name="name" maxlength="40" required><input type="hidden" name="token" value="{{.Token}}"><input type="hidden" name="skin" value="{{.Skin}}"><button type="submit">Hinzufügen</button></form>{{.Dashboard}}</body></html>`
	t, err := template.New("page").Parse(page)
	if err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
	_ = t.Execute(w, struct {
		Token string
		Skin string
		Dashboard template.HTML
	}{token, skinID, template.HTML(html)})
}

func main() {
	port := flag.Int("port", 8085, "local demo port")
	flag.Parse()
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Handler: newDemoState().handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("Synthetic demo only: http://%s", listener.Addr())
	log.Fatal(server.Serve(listener))
}
