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
	"strconv"
	"syscall"
	"time"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/childsession"
	"github.com/pblumer/yapuno/internal/childweb"
	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/devicepairing"
	"github.com/pblumer/yapuno/internal/erasure"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/familyweb"
	"github.com/pblumer/yapuno/internal/fontassets"
	"github.com/pblumer/yapuno/internal/games"
	"github.com/pblumer/yapuno/internal/guardianchat"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
	"github.com/pblumer/yapuno/internal/guardianlock"
	"github.com/pblumer/yapuno/internal/guardianlogin"
	"github.com/pblumer/yapuno/internal/guardianmailer"
	"github.com/pblumer/yapuno/internal/guardiannames"
	"github.com/pblumer/yapuno/internal/guardiansession"
	"github.com/pblumer/yapuno/internal/invitenotice"
	"github.com/pblumer/yapuno/internal/messages"
	"github.com/pblumer/yapuno/internal/pairingweb"
	"github.com/pblumer/yapuno/internal/play"
	"github.com/pblumer/yapuno/internal/practice"
	"github.com/pblumer/yapuno/internal/rating"
	"github.com/pblumer/yapuno/internal/reports"
	"github.com/pblumer/yapuno/internal/signup"
	"github.com/pblumer/yapuno/internal/webpush"
)

// version is set at build time (-ldflags "-X main.version=..."). It is shown
// on the family overview so an operator can tell which build is running.
var version = ""

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
	// guardianChat lets guardians chat with their own children and with
	// guardians of families whose children are connected to theirs.
	guardianChat *guardianchat.Store
	// guardianNames lets guardians choose a name for their own children and
	// one for guardians of connected families. It needs guardianChat, the
	// only place where these names are shown.
	guardianNames *guardiannames.Store
	// play records which games guardians allow for each child; games stores
	// chess games between approved contacts. games needs play, pairing and
	// contacts.
	play  *play.Settings
	games *games.Store
	// practice stores games against the computer; it needs games.
	practice *practice.Store
	// ratings keeps each child's private chess rating; it needs games.
	ratings *rating.Store
	// pushKey and push switch content-free notifications to children's
	// devices on; both or neither. See docs/adr/draft-web-push.md.
	pushKey *webpush.Key
	push    *webpush.Store
	// pushClient sends the pushes; nil means webpush.NewClient.
	pushClient webpush.Doer
	// now is the clock for report notices and reminders; nil means time.Now.
	now func() time.Time
}

// inactive names, in German, what this installation does not offer. The
// family overview shows the list.
func (o optionalStores) inactive() []string {
	var out []string
	for _, f := range []struct {
		off  bool
		name string
	}{
		{o.pairing == nil, "Geräte verbinden"},
		{o.contacts == nil, "Kontakte zwischen Familien"},
		{o.messages == nil, "Chat der Kinder"},
		{o.guardianChat == nil, "Chat für Eltern"},
		{o.reports == nil, "Melden"},
		{o.appearance == nil, "Mein Look"},
		{o.guardianLocks == nil, "Notfallsperre"},
		{o.guardianNames == nil, "Namen für Eltern"},
		{o.push == nil, "Benachrichtigungen"},
		{o.signup == nil, "Registrierung per Einladung"},
		{o.games == nil, "Schach"},
	} {
		if f.off {
			out = append(out, f.name)
		}
	}
	return out
}

// noConnections and noDevices stand in for the contact and pairing stores
// when those are not configured: nobody is connected, no device is approved.
type noConnections struct{}

func (noConnections) Connections(contacts.Party) []contacts.Connection { return nil }

type noDevices struct{}

func (noDevices) ActiveDevice(string, string, string) (devicepairing.Device, bool) {
	return devicepairing.Device{}, false
}

// childThreads shows a child its conversations with its own guardians.
// Guardians have no display names yet, so they are numbered in a stable order.
type childThreads struct {
	side guardianchat.ChildSide
	// names is nil where guardians have no names.
	names *guardiannames.Store
}

func childPrincipal(s childsession.Session) messages.Principal {
	return messages.Principal{Party: contacts.Party{TenantID: s.TenantID, ChildID: s.ChildID}, DeviceID: s.DeviceID}
}

func (c childThreads) FamilyThreads(s childsession.Session) []childweb.FamilyThread {
	threads := c.side.Threads(childPrincipal(s))
	out := make([]childweb.FamilyThread, 0, len(threads))
	for i, t := range threads {
		name := "Dein Elternteil"
		if len(threads) > 1 {
			name = "Elternteil " + strconv.Itoa(i+1)
		}
		// The name this guardian chose for their own children.
		if c.names != nil {
			if chosen := c.names.Get(t.Peer.TenantID, t.Peer.ID).ForChildren; chosen != "" {
				name = chosen
			}
		}
		out = append(out, childweb.FamilyThread{ID: t.ID, Name: name,
			Guardian: appearance.Subject{Kind: appearance.KindGuardian, TenantID: t.Peer.TenantID, ID: t.Peer.ID}})
	}
	return out
}

// routedMessages sends each request of the child pages to the store that
// owns the conversation: the chat with a guardian, or the chat between
// children. Each store authorizes the request itself.
type routedMessages struct {
	family   guardianchat.ChildSide
	children childweb.Messages // nil when the children's chat is off
}

func (r routedMessages) Send(p messages.Principal, id, clientID, body string) (messages.Message, bool, error) {
	if r.family.Has(p, id) {
		return r.family.Send(p, id, clientID, body)
	}
	if r.children == nil {
		return messages.Message{}, false, messages.ErrForbidden
	}
	return r.children.Send(p, id, clientID, body)
}

func (r routedMessages) History(p messages.Principal, id string, after uint64, limit int) ([]messages.Message, error) {
	if r.family.Has(p, id) {
		return r.family.History(p, id, after, limit)
	}
	if r.children == nil {
		return nil, messages.ErrForbidden
	}
	return r.children.History(p, id, after, limit)
}

func (r routedMessages) Recent(p messages.Principal, id string, limit int) ([]messages.Message, error) {
	if r.family.Has(p, id) {
		return r.family.Recent(p, id, limit)
	}
	if r.children == nil {
		return nil, messages.ErrForbidden
	}
	return r.children.Recent(p, id, limit)
}

const (
	// guardianSessionLifetime is how long a guardian stays signed in.
	guardianSessionLifetime = 30 * time.Minute
	// rememberGuardiansFor applies instead when the guardian asked to stay
	// signed in on a device and a session file is configured.
	rememberGuardiansFor = 30 * 24 * time.Hour
)

// app is the HTTP handler plus the background work that belongs to it.
type app struct {
	handler http.Handler
	// reminder is nil unless guardians are e-mailed about reports.
	reminder *reports.Reminder
	// notifier is nil unless children's devices are sent notifications.
	notifier *webpush.Notifier
}

// notifyingMessages tells the other child's devices once a message between
// two children is stored. A retry of a stored message tells nobody.
type notifyingMessages struct {
	*messages.Store
	contacts *contacts.Store
	notify   func(tenantID, childID, conversationID string)
}

func (m notifyingMessages) Send(p messages.Principal, id, clientID, body string) (messages.Message, bool, error) {
	msg, duplicate, err := m.Store.Send(p, id, clientID, body)
	if err == nil && !duplicate {
		if peer, _, ok := m.contacts.Grant(id, p.Party); ok {
			m.notify(peer.TenantID, peer.ChildID, id)
		}
	}
	return msg, duplicate, err
}

// notifyingGuardianChat tells a child's devices once one of the child's own
// guardians wrote to it. Messages between adults and messages a child writes
// to a guardian tell nobody: guardians have no device binding yet.
type notifyingGuardianChat struct {
	*guardianchat.Store
	notify func(tenantID, childID, conversationID string)
}

func (c notifyingGuardianChat) Send(a guardianchat.Actor, id, clientID, body string) (guardianchat.Message, bool, error) {
	msg, duplicate, err := c.Store.Send(a, id, clientID, body)
	if err != nil || duplicate || a.Participant.Kind != authz.ParticipantAdult {
		return msg, duplicate, err
	}
	for _, t := range c.Store.Threads(a) {
		if t.ID == id && t.Kind == guardianchat.Family && t.Peer.Kind == authz.ParticipantChild {
			c.notify(t.Peer.TenantID, t.Peer.ID, id)
		}
	}
	return msg, duplicate, err
}

// newNotifier builds the push notifier. A device is told only if its child
// may read the conversation now, by the rules of the store that owns it.
func newNotifier(opt optionalStores, now func() time.Time) (*webpush.Notifier, error) {
	may := func(d webpush.Device, conversationID string) bool {
		p := messages.Principal{Party: contacts.Party{TenantID: d.TenantID, ChildID: d.ChildID}, DeviceID: d.DeviceID}
		if opt.guardianChat != nil && opt.guardianChat.ForChildren().Has(p, conversationID) {
			return true
		}
		return opt.messages != nil && opt.messages.CanRead(p, conversationID)
	}
	client := opt.pushClient
	if client == nil {
		client = webpush.NewClient()
	}
	return webpush.NewNotifier(opt.push, opt.pushKey, may, client, now)
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
	messages *messages.Store     // nil when the children's chat is off
	guardian *guardianchat.Store // nil when the guardians' chat is off
	games    *games.Store        // nil when chess is off
}

func (p purgingContacts) Remove(guardianID, tenantID, childID, linkID string) error {
	if err := p.Store.Remove(guardianID, tenantID, childID, linkID); err != nil {
		return err
	}
	if p.messages != nil {
		if err := p.messages.Compact(); err != nil {
			log.Printf("message purge after contact removal failed; it will be retried at the next compaction")
		}
	}
	// The children's games end with the contact.
	if p.games != nil {
		if err := p.games.Compact(); err != nil {
			log.Printf("game purge after contact removal failed; it will be retried at the next compaction")
		}
	}
	// The conversation of the two families' guardians ends with the contact.
	if p.guardian != nil {
		if err := p.guardian.Compact(); err != nil {
			log.Printf("guardian chat purge after contact removal failed; it will be retried at the next compaction")
		}
	}
	return nil
}

// invitedContacts connects the child an invitation names with the child of
// the family that registered through it. The inviting guardian approved by
// naming the child, the new guardian in the last step of the registration;
// the contact store checks the current authority of both.
type invitedContacts struct {
	families interface {
		Cards(guardianID, tenantID string) []familydashboard.ChildCard
	}
	contacts *contacts.Store
	// notify, if set, tells the guardians of the inviting child afterwards.
	notify func(tenantID, childID string)
}

func (c invitedContacts) Inviting(guardianID, tenantID, childID string) (string, bool) {
	for _, card := range c.families.Cards(guardianID, tenantID) {
		if card.ID == childID && card.Permissions.ApproveContacts {
			return card.DisplayName, true
		}
	}
	return "", false
}

func (c invitedContacts) Connect(inviterGuardian, inviterTenant, inviterChild, guardianID, tenantID, childID string) error {
	_, err := c.contacts.Connect(inviterGuardian, contacts.Party{TenantID: inviterTenant, ChildID: inviterChild},
		guardianID, contacts.Party{TenantID: tenantID, ChildID: childID})
	if err == nil && c.notify != nil {
		// The inviting guardian approved before knowing who would register.
		c.notify(inviterTenant, inviterChild)
	}
	return err
}

// invitationsOfChild lets the deletion of one child reach the invitations
// that name it. What a deleted family leaves in the invitation store is
// removed later, once the family is gone.
type invitationsOfChild struct{ invitations *signup.Store }

func (i invitationsOfChild) PurgeChild(tenantID, childID string) error {
	return i.invitations.PurgeChild(tenantID, childID)
}

func (invitationsOfChild) PurgeTenant(string) error { return nil }

// newEraser assembles the deletion of a child or a family from the stores
// this installation runs. The order is fixed by the erasure package: first
// what a child acts with, then the profile, then identities and sessions,
// then what restricts or records guardians, then the message logs.
func newEraser(families *familystore.Store, directory *guardiandirectory.Directory, sessions *guardiansession.Manager, links *guardianlogin.Handler, opt optionalStores) (*erasure.Service, error) {
	cfg := erasure.Config{
		Families:   families,
		Identities: directory,
		SignOut: []func(guardianID, tenantID string){
			func(g, t string) { sessions.RevokeAll(g, t) },
			func(g, t string) { links.Cancel(g, t) },
		},
	}
	// Assign only stores that exist: a typed nil would be a non-nil interface.
	if opt.pairing != nil {
		cfg.Before = append(cfg.Before, erasure.Dependent{Name: "devices", Store: opt.pairing})
	}
	if opt.push != nil {
		cfg.Before = append(cfg.Before, erasure.Dependent{Name: "push subscriptions", Store: opt.push})
	}
	if opt.contacts != nil {
		cfg.Before = append(cfg.Before, erasure.Dependent{Name: "contacts", Store: opt.contacts})
	}
	if opt.reports != nil {
		cfg.Before = append(cfg.Before, erasure.Dependent{Name: "reports", Store: opt.reports})
	}
	if opt.appearance != nil {
		cfg.Before = append(cfg.Before, erasure.Dependent{Name: "looks", Store: opt.appearance})
	}
	if opt.guardianNames != nil {
		cfg.Records = append(cfg.Records, erasure.TenantDependent{Name: "guardian names", Store: opt.guardianNames})
	}
	if opt.signup != nil {
		cfg.Before = append(cfg.Before, erasure.Dependent{Name: "invitations naming the child", Store: invitationsOfChild{opt.signup}})
		cfg.After = append(cfg.After, erasure.TenantDependent{Name: "invitations", Store: opt.signup})
	}
	if opt.guardianLocks != nil {
		cfg.After = append(cfg.After, erasure.TenantDependent{Name: "locks", Store: opt.guardianLocks})
	}
	if opt.play != nil {
		cfg.Before = append(cfg.Before, erasure.Dependent{Name: "game settings", Store: opt.play})
	}
	if opt.practice != nil {
		cfg.Before = append(cfg.Before, erasure.Dependent{Name: "practice games", Store: opt.practice})
	}
	if opt.ratings != nil {
		cfg.Before = append(cfg.Before, erasure.Dependent{Name: "chess ratings", Store: opt.ratings})
	}
	if opt.messages != nil {
		cfg.Logs = append(cfg.Logs, erasure.Log{Name: "the children's chat", Log: opt.messages})
	}
	if opt.games != nil {
		cfg.Logs = append(cfg.Logs, erasure.Log{Name: "chess games", Log: opt.games})
	}
	if opt.guardianChat != nil {
		cfg.Logs = append(cfg.Logs, erasure.Log{Name: "the guardians' chat", Log: opt.guardianChat})
	}
	return erasure.New(cfg)
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
	if opt.guardianNames != nil && opt.guardianChat == nil {
		return nil, errors.New("guardian names require the guardian chat")
	}
	if (opt.pushKey == nil) != (opt.push == nil) {
		return nil, errors.New("notifications require both the push key and the subscription file")
	}
	var notifier *webpush.Notifier
	if opt.push != nil {
		if opt.pairing == nil || (opt.messages == nil && opt.guardianChat == nil) {
			return nil, errors.New("notifications require device pairing and a chat")
		}
		var err error
		if notifier, err = newNotifier(opt, now); err != nil {
			return nil, err
		}
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
	loginConfig := guardianlogin.Config{Origin: origin, Directory: directory, Sender: sender, Sessions: sessions, Now: time.Now, PairingPage: opt.pairing != nil}
	inactive := opt.inactive()
	if sessions.Remembers() {
		// The last step of the sign-in offers to stay signed in on that device.
		loginConfig.Remembered = sessions
		loginConfig.RememberDays = int(sessions.RememberedFor() / (24 * time.Hour))
		loginConfig.SessionMinutes = int(sessions.Lifetime() / time.Minute)
	} else {
		inactive = append(inactive, "Angemeldet bleiben")
	}
	login, err := guardianlogin.New(loginConfig)
	if err != nil {
		return nil, err
	}
	auth := verifiedSessions{sessions: sessions, directory: directory}
	familyOptions := familyweb.Options{DevicePages: opt.pairing != nil, ContactPages: opt.contacts != nil, SecurityPages: true, Inactive: inactive, Version: version}
	if opt.guardianChat != nil {
		// From here on the chat asks the directory that knows the locks.
		if err := opt.guardianChat.SetGuardians(directory); err != nil {
			return nil, err
		}
		familyOptions.Chat = opt.guardianChat
		if opt.guardianNames != nil {
			familyOptions.Names = opt.guardianNames
		}
	}
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
		// Guardians who create families and profiles themselves can also
		// delete them.
		familyOptions.DeletePages = true
	}
	if opt.games != nil {
		if opt.play == nil || opt.contacts == nil || opt.pairing == nil {
			return nil, errors.New("chess requires game settings, contacts and device pairing")
		}
		familyOptions.GamePages = true
		// Guardians play their own children where the guardians' chat,
		// which shows a child its guardians, is on. The directory knows
		// emergency locks.
		if opt.guardianChat != nil {
			if err := opt.games.EnableFamilies(store, directory); err != nil {
				return nil, err
			}
			familyOptions.FamilyGames = opt.games
		}
	}
	if opt.ratings != nil {
		if opt.games == nil {
			return nil, errors.New("ratings require chess")
		}
		opt.games.SetRater(opt.ratings)
		if opt.practice != nil {
			opt.practice.SetRater(opt.ratings)
		}
	}
	if opt.practice != nil && opt.games == nil {
		return nil, errors.New("practice requires chess")
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
	securityOptions := familyweb.SecurityOptions{Looks: looks, DeleteFamily: opt.signup != nil}
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
		joinConfig := signup.Config{Origin: origin, Store: opt.signup, Directory: directory, Families: store, Mailer: registrationMailer, Sessions: sessions, Now: time.Now}
		inviteOptions := familyweb.InviteOptions{Looks: looks}
		if opt.contacts != nil {
			// With contacts an invitation can also connect the children.
			parentsChat := opt.guardianChat != nil
			connections := invitedContacts{families: store, contacts: opt.contacts}
			// The lock-aware directory keeps locked guardians out of the recipients.
			if mailer, ok := sender.(invitenotice.Mailer); ok {
				notifier, err := invitenotice.New(store, directory, mailer)
				if err != nil {
					return nil, err
				}
				connections.notify, inviteOptions.Notice = notifier.Connected, true
			}
			joinConfig.Connections, joinConfig.ParentsChat = connections, parentsChat
			inviteOptions.Connect, inviteOptions.ParentsChat = true, parentsChat
		}
		// The registering guardian may give a name for other adults.
		if opt.guardianNames != nil {
			joinConfig.Names = opt.guardianNames
		}
		join, err := signup.New(joinConfig)
		if err != nil {
			return nil, err
		}
		joinRoutes := join.Routes()
		mux.Handle("/join", joinRoutes)
		mux.Handle("/join/", joinRoutes)
		inviteRoutes, err := familyweb.NewInvitePages(auth, opt.signup, store, origin, inviteOptions)
		if err != nil {
			return nil, err
		}
		mux.Handle("/family/invite", inviteRoutes)
		mux.Handle("/family/invite/", inviteRoutes)
		eraser, err := newEraser(store, directory, sessions, login, opt)
		if err != nil {
			return nil, err
		}
		deleteRoutes, err := familyweb.NewDeletePages(auth, eraser, store, store, sessions, looks)
		if err != nil {
			return nil, err
		}
		mux.Handle("/family/children/delete", deleteRoutes)
		mux.Handle("/family/delete", deleteRoutes)
	}
	if opt.guardianChat != nil {
		// Assign only a store that exists: a typed nil would be a non-nil interface.
		var chosenNames familyweb.GuardianNames
		if opt.guardianNames != nil {
			chosenNames = opt.guardianNames
			nameRoutes, err := familyweb.NewNamePages(auth, opt.guardianNames, looks)
			if err != nil {
				return nil, err
			}
			mux.Handle("/family/name", nameRoutes)
		}
		var guardianChat familyweb.GuardianChat = opt.guardianChat
		if notifier != nil {
			guardianChat = notifyingGuardianChat{Store: opt.guardianChat, notify: notifier.Notify}
		}
		chatRoutes, err := familyweb.NewChatPagesWithNames(auth, guardianChat, store, chosenNames, looks)
		if err != nil {
			return nil, err
		}
		for _, pattern := range []string{"/family/chat", "/family/chat/", "/family/chat.js", "/family/chat-utils.js"} {
			mux.Handle(pattern, chatRoutes)
		}
	}
	if opt.appearance != nil {
		lookRoutes, err := familyweb.NewLookPages(auth, opt.appearance)
		if err != nil {
			return nil, err
		}
		mux.Handle("/family/look", lookRoutes)
	}
	if opt.games != nil {
		gameRoutes, err := familyweb.NewGamePages(auth, opt.play, store, looks)
		if err != nil {
			return nil, err
		}
		mux.Handle("/family/games", gameRoutes)
		if familyOptions.FamilyGames != nil {
			playRoutes, err := familyweb.NewPlayPages(auth, opt.games, store, looks)
			if err != nil {
				return nil, err
			}
			mux.Handle("/family/play/", playRoutes)
			mux.Handle("/family/chess.js", playRoutes)
			mux.Handle("/family/chess-utils.js", playRoutes)
		}
	}
	if opt.contacts != nil {
		var contactStore familyweb.ContactStore = opt.contacts
		if opt.messages != nil || opt.guardianChat != nil || opt.games != nil {
			contactStore = purgingContacts{Store: opt.contacts, messages: opt.messages, guardian: opt.guardianChat, games: opt.games}
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
		deviceRoutes, err := familyweb.NewDevicePages(auth, opt.pairing, store, origin, looks)
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
			if notifier != nil {
				childOptions.Messages = notifyingMessages{Store: opt.messages, contacts: opt.contacts, notify: notifier.Notify}
			}
		}
		if notifier != nil {
			childOptions.Push = childweb.Push{Key: opt.pushKey, Subscriptions: opt.push}
		}
		if opt.games != nil {
			childOptions.Games = opt.games
		}
		if opt.practice != nil {
			childOptions.Practice = opt.practice
		}
		if opt.ratings != nil {
			childOptions.Ratings = opt.ratings
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
		if opt.guardianChat != nil {
			side := opt.guardianChat.ForChildren()
			childOptions.Family = childThreads{side: side, names: opt.guardianNames}
			childOptions.Messages = routedMessages{family: side, children: childOptions.Messages}
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
	return &app{handler: handler, reminder: reminder, notifier: notifier}, nil
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
	guardianChatPath := flag.String("guardian-chat", os.Getenv("YAPUNO_GUARDIAN_CHAT_FILE"), "optional private log for chats of guardians with their own children and with guardians of connected families")
	namesPath := flag.String("guardian-names", os.Getenv("YAPUNO_GUARDIAN_NAMES_FILE"), "optional private file for the names guardians choose to be shown by; requires the guardian chat")
	sessionsPath := flag.String("guardian-sessions", os.Getenv("YAPUNO_GUARDIAN_SESSIONS_FILE"), "optional private file; lets guardians stay signed in on a device for 30 days, also across restarts")
	signupPath := flag.String("signup", os.Getenv("YAPUNO_SIGNUP_FILE"), "optional private invitation file; switches invited self-registration on (guardianship is then self-declared)")
	playPath := flag.String("play", os.Getenv("YAPUNO_PLAY_FILE"), "optional private file for the games guardians allow per child; requires the games log")
	ratingsPath := flag.String("ratings", os.Getenv("YAPUNO_RATINGS_FILE"), "optional private file for each child's private chess rating; requires the games log")
	practicePath := flag.String("practice", os.Getenv("YAPUNO_PRACTICE_FILE"), "optional private file for practice games against the computer; requires the games log")
	gamesPath := flag.String("games", os.Getenv("YAPUNO_GAMES_FILE"), "optional private log of chess games between approved contacts; requires the play file, pairing and contacts")
	appearancePath := flag.String("appearance", os.Getenv("YAPUNO_APPEARANCE_FILE"), "optional private file for saved looks (theme and figure)")
	pushKeyPath := flag.String("push-key", os.Getenv("YAPUNO_PUSH_KEY_FILE"), "optional private VAPID key (0600, create with yapuno-pushkey); requires -push-subscriptions")
	pushPath := flag.String("push-subscriptions", os.Getenv("YAPUNO_PUSH_SUBSCRIPTIONS_FILE"), "optional private file of children's push subscriptions; requires -push-key, pairing and a chat")
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
	var guardianChatStore *guardianchat.Store
	if *guardianChatPath != "" {
		var connections guardianchat.Contacts = noConnections{}
		if contactStore != nil {
			connections = contactStore
		}
		var approved guardianchat.Devices = noDevices{}
		if pairingStore != nil {
			approved = pairingStore
		}
		guardianChatStore, err = guardianchat.Open(*guardianChatPath, store, connections, directory, approved, time.Now)
		if err != nil {
			log.Fatal("guardian chat log unavailable, insecure or corrupt")
		}
		defer guardianChatStore.Close()
	}
	var guardianNameStore *guardiannames.Store
	if *namesPath != "" {
		if guardianChatStore == nil {
			log.Fatal("YAPUNO_GUARDIAN_NAMES_FILE requires YAPUNO_GUARDIAN_CHAT_FILE")
		}
		guardianNameStore, err = guardiannames.Open(*namesPath)
		if err != nil {
			log.Fatal("guardian names file unavailable, insecure or corrupt")
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
	var playSettings *play.Settings
	var gameStore *games.Store
	if (*playPath == "") != (*gamesPath == "") {
		log.Fatal("YAPUNO_PLAY_FILE and YAPUNO_GAMES_FILE are needed together")
	}
	if *gamesPath != "" {
		if contactStore == nil || pairingStore == nil {
			log.Fatal("YAPUNO_GAMES_FILE requires YAPUNO_CONTACTS_FILE and YAPUNO_PAIRING_FILE")
		}
		if playSettings, err = play.OpenSettings(*playPath, store); err != nil {
			log.Fatal("play settings unavailable, insecure or corrupt")
		}
		if gameStore, err = games.Open(*gamesPath, contactStore, pairingStore, store, playSettings, time.Now); err != nil {
			log.Fatal("game log unavailable, insecure or corrupt")
		}
		defer gameStore.Close()
	}
	var ratingStore *rating.Store
	if *ratingsPath != "" {
		if gameStore == nil {
			log.Fatal("YAPUNO_RATINGS_FILE requires YAPUNO_PLAY_FILE and YAPUNO_GAMES_FILE")
		}
		if ratingStore, err = rating.Open(*ratingsPath, time.Now); err != nil {
			log.Fatal("ratings file unavailable, insecure or corrupt")
		}
	}
	var practiceStore *practice.Store
	if *practicePath != "" {
		if gameStore == nil {
			log.Fatal("YAPUNO_PRACTICE_FILE requires YAPUNO_PLAY_FILE and YAPUNO_GAMES_FILE")
		}
		if practiceStore, err = practice.Open(*practicePath, pairingStore, store, playSettings, time.Now); err != nil {
			log.Fatal("practice file unavailable, insecure or corrupt")
		}
	}
	var pushKey *webpush.Key
	var pushStore *webpush.Store
	if *pushKeyPath != "" || *pushPath != "" {
		if *pushKeyPath == "" || *pushPath == "" || pairingStore == nil {
			log.Fatal("YAPUNO_PUSH_KEY_FILE and YAPUNO_PUSH_SUBSCRIPTIONS_FILE go together and require YAPUNO_PAIRING_FILE")
		}
		if pushKey, err = webpush.LoadKey(*pushKeyPath, *origin); err != nil {
			log.Fatal("push key unavailable or not private (0600), or the origin is not an HTTPS origin")
		}
		if pushStore, err = webpush.Open(*pushPath, pairingStore); err != nil {
			log.Fatal("push subscription store unavailable, insecure or corrupt")
		}
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
	sessions, err := guardiansession.New(time.Now, guardianSessionLifetime)
	if err != nil {
		log.Fatal(err)
	}
	if *sessionsPath != "" {
		if err := sessions.Remember(*sessionsPath, rememberGuardiansFor); err != nil {
			log.Fatal("guardian session file unavailable, insecure or corrupt")
		}
	}
	application, err := newApp(*origin, store, directory, sender, sessions, optionalStores{pairing: pairingStore, appearance: appearanceStore, contacts: contactStore, messages: messageStore, guardianLocks: guardianLocks, reports: reportStore, signup: signupStore, guardianChat: guardianChatStore, guardianNames: guardianNameStore, play: playSettings, games: gameStore, practice: practiceStore, ratings: ratingStore, pushKey: pushKey, push: pushStore})
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
	if application.notifier != nil {
		go application.notifier.Run(ctx)
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
