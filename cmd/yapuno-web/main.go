// Command yapuno-web serves the guardian login and family administration portal.
// It only accepts loopback connections and requires HTTPS termination at a
// trusted reverse proxy. It is an engineering preview, not a child pilot.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/familyweb"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
	"github.com/pblumer/yapuno/internal/guardianlogin"
	"github.com/pblumer/yapuno/internal/guardianmailer"
	"github.com/pblumer/yapuno/internal/guardiansession"
)

// verifiedSessions invalidates already-issued sessions when guardian identity
// or their last child-scoped grant has been revoked in the authoritative data.
type verifiedSessions struct {
	sessions  *guardiansession.Manager
	directory *guardiandirectory.Directory
}

func (v verifiedSessions) Authenticate(r *http.Request) (familyweb.Session, error) {
	session, err := v.sessions.Authenticate(r)
	if err != nil {
		return familyweb.Session{}, err
	}
	if !v.directory.ActiveGuardian(r.Context(), session.GuardianID, session.TenantID) {
		return familyweb.Session{}, guardiansession.ErrUnauthenticated
	}
	return session, nil
}

func newHandler(origin string, store *familystore.Store, directory *guardiandirectory.Directory, sender guardianlogin.Sender, sessions *guardiansession.Manager) (http.Handler, error) {
	if store == nil || directory == nil || sender == nil || sessions == nil {
		return nil, errors.New("missing trusted Yapuno dependency")
	}
	login, err := guardianlogin.New(guardianlogin.Config{Origin: origin, Directory: directory, Sender: sender, Sessions: sessions, Now: time.Now})
	if err != nil {
		return nil, err
	}
	family, err := familyweb.New(verifiedSessions{sessions: sessions, directory: directory}, store)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	loginRoutes, familyRoutes := login.Routes(), family
	mux.Handle("/login", loginRoutes)
	mux.Handle("/login/", loginRoutes)
	mux.Handle("/family", familyRoutes)
	mux.Handle("/family/", familyRoutes)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/login", http.StatusSeeOther) })
	// Reject host-header poisoning even behind a TLS-terminating reverse proxy.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != parsed.Host {
			http.Error(w, "Invalid host", http.StatusMisdirectedRequest)
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}

func main() {
	origin := flag.String("origin", os.Getenv("YAPUNO_ORIGIN"), "canonical HTTPS origin")
	listen := flag.String("listen", "127.0.0.1:8086", "loopback address for trusted HTTPS reverse proxy")
	storePath := flag.String("families", os.Getenv("YAPUNO_FAMILIES_FILE"), "durable family state file")
	guardiansPath := flag.String("guardians", os.Getenv("YAPUNO_GUARDIANS_FILE"), "offline-provisioned guardian identity file (0600)")
	flag.Parse()
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("listen must specify a numeric loopback IP, e.g. 127.0.0.1:8086")
	}
	if *storePath == "" || *guardiansPath == "" {
		log.Fatal("YAPUNO_FAMILIES_FILE and YAPUNO_GUARDIANS_FILE are required")
	}
	store, err := familystore.Open(*storePath)
	if err != nil {
		log.Fatalf("family store unavailable: %v", err)
	}
	directory, err := guardiandirectory.New(*guardiansPath, store)
	if err != nil {
		log.Fatal("guardian directory unavailable or not private (0600)")
	}
	smtpPort := 465
	sender, err := guardianmailer.New(guardianmailer.Config{
		Host: os.Getenv("YAPUNO_SMTP_HOST"), Port: smtpPort,
		Username: os.Getenv("YAPUNO_SMTP_USER"), Password: os.Getenv("YAPUNO_SMTP_PASSWORD"),
		From: os.Getenv("YAPUNO_SMTP_FROM"),
	})
	if err != nil {
		log.Fatal("invalid SMTP configuration; TLS 465 is required")
	}
	sessions, err := guardiansession.New(time.Now, 30*time.Minute)
	if err != nil {
		log.Fatal(err)
	}
	handler, err := newHandler(*origin, store, directory, sender, sessions)
	if err != nil {
		log.Fatalf("invalid HTTPS origin or dependencies: %v", err)
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(closeCtx)
	}()
	log.Printf("Yapuno preview listening on %s behind HTTPS proxy for %s (no child pilot)", listener.Addr(), *origin)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
