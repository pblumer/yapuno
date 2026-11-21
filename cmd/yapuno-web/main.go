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
	"github.com/pblumer/yapuno/internal/fontassets"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
	"github.com/pblumer/yapuno/internal/guardianlock"
	"github.com/pblumer/yapuno/internal/guardianlogin"
	"github.com/pblumer/yapuno/internal/guardianmailer"
	"github.com/pblumer/yapuno/internal/guardiansession"
	"github.com/pblumer/yapuno/internal/messages"
	"github.com/pblumer/yapuno/internal/pairingweb"
	"github.com/pblumer/yapuno/internal/reports"
	"github.com/pblumer/yapuno/internal/signup"
)

// reminderInterval is how often unseen reports are checked for a due reminder.
const reminderInterval = 5 * time.Minute

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
	// guardianLocks enables the emergency lock of a guardian's own access.
	guardianLocks *guardianlock.Store
	// reports lets children report single messages to their own guardians.
	reports *reports.Store
	// signup switches invited self-registration on: guardians invite other
	// families, who register themselves and create their own child profiles.
	// Guardianship is then self-declared; see docs/adr/draft-invited-self-registration.md.
	signup *signup.Store
	// now is the clock for report notices and reminders; nil means time.Now.
	now func() time.Time
}

// app is the HTTP handler plus the background work that belongs to it.
type app struct {
	handler http.Handler
	// reminder is nil unless guardians are e-mailed about reports.
	reminder *reports.Reminder
}

// runner is one pass of periodic work.
type runner interface {
	Run(ctx context.Context) int
}

// remindEvery calls the reminder at a fixed interval until ctx ends.
func remindEvery(ctx context.Context, r runner, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Run(ctx)
		}
	}
}

// newHandler builds the application and returns only its HTTP handler.
func newHandler(origin string, store *familystore.Store, directory *guardiandirectory.Directory, sender guardianlogin.Sender, sessions *guardiansession.Manager, opt optionalStores) (http.Handler, error) {
	a, err := newApp(origin, store, directory, sender, sessions, opt)
	if err != nil {
		return nil, err
	}
	return a.handler, nil
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

func newApp(origin string, store *familystore.Store, directory *guardiandirectory.Directory, sender guardianlogin.Sender, sessions *guardiansession.Manager, opt optionalStores) (*app, error) {
	if store == nil || directory == nil || sender == nil || sessions == nil {
		return nil, errors.New("missing trusted Yapuno dependency")
	}
	now := opt.now
	if now == nil {
		now = time.Now
	}
	var reminder *reports.Reminder
	if opt.messages != nil && (opt.contacts == nil || opt.pairing == nil) {
		return nil, errors.New("messaging requires the contact and pairing stores")
	}
	if opt.reports != nil && opt.messages == nil {
		return nil, errors.New("reports require messaging")
	}
	var locker *guardianlock.Locker
	if opt.guardianLocks != nil {
		// From here on every sign-in and every request also checks the lock.
		lockAware, err := directory.WithLocks(opt.guardianLocks)
		if err != nil {
			return nil, err
		}
		directory = lockAware
		notifier, ok := sender.(guardianlock.Notifier)
		if !ok {
			return nil, errors.New("guardian locks require a mailer that can send lock notices")
		}
		if locker, err = guardianlock.NewLocker(opt.guardianLocks, store, directory, notifier); err != nil {
			return nil, err
		}
	}
	login, err := guardianlogin.New(guardianlogin.Config{Origin: origin, Directory: directory, Sender: sender, Sessions: sessions, Now: time.Now})
	if err != nil {
		return nil, err
	}
	auth := verifiedSessions{sessions: sessions, directory: directory}
	familyOptions := familyweb.Options{DevicePages: opt.pairing != nil, ContactPages: opt.contacts != nil, SecurityPages: true}
	// Assign only a non-nil store: a typed nil pointer would be a non-nil interface.
	var looks familyweb.AppearanceStore
	if opt.appearance != nil {
		looks = opt.appearance
		familyOptions.Looks = looks
	}
	if opt.reports != nil {
		familyOptions.Reports = opt.reports
	}
	var registrationMailer signup.Mailer
	if opt.signup != nil {
		mailer, ok := sender.(signup.Mailer)
		if !ok {
			return nil, errors.New("self-registration requires a mailer that can send registration links")
		}
		registrationMailer = mailer
		familyOptions.Children, familyOptions.InvitePages = store, true
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
	// Self-hosted fonts are public static files; no session is involved.
	mux.Handle(fontassets.Prefix, fontassets.Handler())
	mux.Handle("/login", loginRoutes)
	mux.Handle("/login/", loginRoutes)
	mux.Handle("/family", familyRoutes)
	mux.Handle("/family/", familyRoutes)
	// Sign-out is always available; the emergency lock needs its store.
	securityOptions := familyweb.SecurityOptions{Looks: looks}
	if locker != nil {
		securityOptions.Locker = locker
		if opt.pairing != nil {
			securityOptions.Devices = opt.pairing
		}
	}
	securityRoutes, err := familyweb.NewSecurityPages(auth, sessions, login, store, securityOptions)
	if err != nil {
		return nil, err
	}
	mux.Handle("/family/security", securityRoutes)
	mux.Handle("/family/security/", securityRoutes)
	if opt.signup != nil {
		// The lock-aware directory voids invitations of locked guardians.
		join, err := signup.New(signup.Config{Origin: origin, Store: opt.signup, Directory: directory, Families: store, Mailer: registrationMailer, Sessions: sessions, Now: time.Now})
		if err != nil {
			return nil, err
		}
		joinRoutes := join.Routes()
		mux.Handle("/join", joinRoutes)
		mux.Handle("/join/", joinRoutes)
		inviteRoutes, err := familyweb.NewInvitePages(auth, opt.signup, store, origin, looks)
		if err != nil {
			return nil, err
		}
		mux.Handle("/family/invite", inviteRoutes)
		mux.Handle("/family/invite/", inviteRoutes)
	}
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
		var contactRoutes http.Handler
		if opt.reports != nil {
			contactRoutes, err = familyweb.NewContactPagesWithReports(auth, contactStore, store, looks, opt.reports)
		} else {
			contactRoutes, err = familyweb.NewContactPages(auth, contactStore, store, looks)
		}
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
		lostRoutes, err := familyweb.NewLostDevicePages(auth, opt.pairing, store, looks)
		if err != nil {
			return nil, err
		}
		mux.Handle("/family/devices/lost", lostRoutes)
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
		if opt.reports != nil {
			childOptions.Reports = opt.reports
			// Guardians are e-mailed about a new report when the mailer can
			// do that; the child page then says so. The lock-aware directory
			// keeps locked guardians out of the recipients.
			if mailer, ok := sender.(reports.Mailer); ok {
				announcer, err := reports.NewAnnouncer(store, directory, mailer, now)
				if err != nil {
					return nil, err
				}
				notifying, err := reports.WithNotices(opt.reports, announcer)
				if err != nil {
					return nil, err
				}
				childOptions.Reports, childOptions.ReportNotices = notifying, true
				// Reports no guardian has opened are reminded by a timer.
				if reminder, err = reports.NewReminder(opt.reports, announcer); err != nil {
					return nil, err
				}
			}
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
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != parsed.Host {
			http.Error(w, "Invalid host", http.StatusMisdirectedRequest)
			return
		}
		mux.ServeHTTP(w, r)
	})
	return &app{handler: handler, reminder: reminder}, nil
}

func main() {
	origin := flag.String("origin", os.Getenv("YAPUNO_ORIGIN"), "canonical HTTPS origin")
	listen := flag.String("listen", "127.0.0.1:8086", "loopback address for trusted HTTPS reverse proxy")
	storePath := flag.String("families", os.Getenv("YAPUNO_FAMILIES_FILE"), "durable family state file")
	guardiansPath := flag.String("guardians", os.Getenv("YAPUNO_GUARDIANS_FILE"), "offline-provisioned guardian identity file (0600)")
	pairingPath := flag.String("pairing", os.Getenv("YAPUNO_PAIRING_FILE"), "optional private device pairing file")
	contactsPath := flag.String("contacts", os.Getenv("YAPUNO_CONTACTS_FILE"), "optional private file for guardian-approved contacts")
	messagesPath := flag.String("messages", os.Getenv("YAPUNO_MESSAGES_FILE"), "optional private message log; requires pairing and contacts")
	reportsPath := flag.String("reports", os.Getenv("YAPUNO_REPORTS_FILE"), "optional private file for messages children report to their guardians; requires messages")
	guardianLocksPath := flag.String("guardian-locks", os.Getenv("YAPUNO_GUARDIAN_LOCKS_FILE"), "optional private file for emergency locks of guardian access")
	signupPath := flag.String("signup", os.Getenv("YAPUNO_SIGNUP_FILE"), "optional private invitation file; switches invited self-registration on (guardianship is then self-declared)")
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
	var reportStore *reports.Store
	if *reportsPath != "" {
		if messageStore == nil {
			log.Fatal("YAPUNO_REPORTS_FILE requires YAPUNO_MESSAGES_FILE")
		}
		reportStore, err = reports.Open(*reportsPath, messageStore, store, time.Now)
		if err != nil {
			log.Fatal("report store unavailable, insecure or corrupt")
		}
	}
	var guardianLocks *guardianlock.Store
	if *guardianLocksPath != "" {
		guardianLocks, err = guardianlock.Open(*guardianLocksPath, time.Now)
		if err != nil {
			log.Fatal("guardian lock store unavailable or insecure")
		}
	}
	var signupStore *signup.Store
	if *signupPath != "" {
		signupStore, err = signup.Open(*signupPath, time.Now)
		if err != nil {
			log.Fatal("invitation store unavailable, insecure or corrupt")
		}
		log.Printf("invited self-registration is ON: guardianship is self-declared and not verified; use synthetic profiles only")
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
	application, err := newApp(*origin, store, directory, sender, sessions, optionalStores{pairing: pairingStore, appearance: appearanceStore, contacts: contactStore, messages: messageStore, guardianLocks: guardianLocks, reports: reportStore, signup: signupStore})
	if err != nil {
		log.Fatalf("invalid HTTPS origin or dependencies: %v", err)
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Handler: application.handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if application.reminder != nil {
		go remindEvery(ctx, application.reminder, reminderInterval)
	}
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
