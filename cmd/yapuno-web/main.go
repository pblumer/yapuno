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

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/childsession"
	"github.com/pblumer/yapuno/internal/childweb"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/familyweb"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
	"github.com/pblumer/yapuno/internal/guardianlogin"
	"github.com/pblumer/yapuno/internal/guardianmailer"
	"github.com/pblumer/yapuno/internal/guardiansession"
	"github.com/pblumer/yapuno/internal/messages"
	"github.com/pblumer/yapuno/internal/pairingweb"
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

// optionalStores enables optional preview features when configured.
type optionalStores struct {
	pairing    *devicepairing.Store
	appearance *appearance.Store
	contacts   *contacts.Store
	messages   *messages.Store
}

// purgingContacts deletes the messages of a contact from disk as soon as a
// guardian ends that contact. Access already stops with the removal itself;
// a failed purge is retried by the next compaction.
type purgingContacts struct {
	*contacts.Store
	messages *messages.Store
}

func (p purgingContacts) Remove(guardianID, tenantID, childID, linkID string) error {
	if err := p.Store.Remove(guardianID, tenantID, childID, linkID); err != nil {
		return err
	}
	if err := p.messages.Compact(); err != nil {
		log.Printf("message purge after contact removal failed; it will be retried at the next compaction")
	}
	return nil
}

func newHandler(origin string, store *familystore.Store, directory *guardiandirectory.Directory, sender guardianlogin.Sender, sessions *guardiansession.Manager, opt optionalStores) (http.Handler, error) {
	if store == nil || directory == nil || sender == nil || sessions == nil {
		return nil, errors.New("missing trusted Yapuno dependency")
	}
	if opt.messages != nil && (opt.contacts == nil || opt.pairing == nil) {
		return nil, errors.New("messaging requires the contact and pairing stores")
	}
	login, err := guardianlogin.New(guardianlogin.Config{Origin: origin, Directory: directory, Sender: sender, Sessions: sessions, Now: time.Now})
	if err != nil {
		return nil, err
	}
	auth := verifiedSessions{sessions: sessions, directory: directory}
	familyOptions := familyweb.Options{DevicePages: opt.pairing != nil, ContactPages: opt.contacts != nil}
	// Assign only a non-nil store: a typed nil pointer would be a non-nil interface.
	var looks familyweb.AppearanceStore
	if opt.appearance != nil {
		looks = opt.appearance
		familyOptions.Looks = looks
	}
	family, err := familyweb.NewWithOptions(auth, store, familyOptions)
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
	if opt.appearance != nil {
		lookRoutes, err := familyweb.NewLookPages(auth, opt.appearance)
		if err != nil {
			return nil, err
		}
		mux.Handle("/family/look", lookRoutes)
	}
	if opt.contacts != nil {
		var contactStore familyweb.ContactStore = opt.contacts
		if opt.messages != nil {
			contactStore = purgingContacts{Store: opt.contacts, messages: opt.messages}
		}
		contactRoutes, err := familyweb.NewContactPages(auth, contactStore, store, looks)
		if err != nil {
			return nil, err
		}
		mux.Handle("/family/contacts", contactRoutes)
		mux.Handle("/family/contacts/", contactRoutes)
	}
	if opt.pairing != nil {
		deviceRoutes, err := familyweb.NewDevicePages(auth, opt.pairing, origin)
		if err != nil {
			return nil, err
		}
		mux.Handle("/family/devices", deviceRoutes)
		mux.Handle("/family/devices/", deviceRoutes)
		childRoutes, err := pairingweb.New(opt.pairing)
		if err != nil {
			return nil, err
		}
		mux.Handle("/pair", childRoutes)
		mux.Handle("/pair/", childRoutes)
		// Paired devices sign in with their bound key; sessions live in this
		// process only and end on restart, after which devices sign in again.
		childSessions, err := childsession.New(opt.pairing, store, time.Now, 12*time.Hour)
		if err != nil {
			return nil, err
		}
		childOptions := childweb.Options{}
		if opt.contacts != nil {
			childOptions.Contacts = opt.contacts
		}
		if opt.appearance != nil {
			childOptions.Looks = opt.appearance
		}
		if opt.messages != nil {
			childOptions.Messages = opt.messages
		}
		childPages, err := childweb.New(childSessions, store, childOptions)
		if err != nil {
			return nil, err
		}
		mux.Handle("/child", childPages)
		mux.Handle("/child/", childPages)
	}
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
	pairingPath := flag.String("pairing", os.Getenv("YAPUNO_PAIRING_FILE"), "optional private device pairing file")
	contactsPath := flag.String("contacts", os.Getenv("YAPUNO_CONTACTS_FILE"), "optional private file for guardian-approved contacts")
	messagesPath := flag.String("messages", os.Getenv("YAPUNO_MESSAGES_FILE"), "optional private message log; requires pairing and contacts")
	appearancePath := flag.String("appearance", os.Getenv("YAPUNO_APPEARANCE_FILE"), "optional private file for saved looks (theme and figure)")
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
	var pairingStore *devicepairing.Store
	if *pairingPath != "" {
		pairingStore, err = devicepairing.Open(*pairingPath, store, time.Now)
		if err != nil {
			log.Fatal("device pairing store unavailable or insecure")
		}
	}
	var appearanceStore *appearance.Store
	if *appearancePath != "" {
		appearanceStore, err = appearance.Open(*appearancePath)
		if err != nil {
			log.Fatal("appearance store unavailable or insecure")
		}
	}
	var contactStore *contacts.Store
	if *contactsPath != "" {
		contactStore, err = contacts.Open(*contactsPath, store, time.Now)
		if err != nil {
			log.Fatal("contact store unavailable or insecure")
		}
	}
	var messageStore *messages.Store
	if *messagesPath != "" {
		if contactStore == nil || pairingStore == nil {
			log.Fatal("YAPUNO_MESSAGES_FILE requires YAPUNO_CONTACTS_FILE and YAPUNO_PAIRING_FILE")
		}
		messageStore, err = messages.Open(*messagesPath, contactStore, pairingStore, store, time.Now)
		if err != nil {
			log.Fatal("message store unavailable, insecure or corrupt")
		}
		defer messageStore.Close()
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
	handler, err := newHandler(*origin, store, directory, sender, sessions, optionalStores{pairing: pairingStore, appearance: appearanceStore, contacts: contactStore, messages: messageStore})
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
