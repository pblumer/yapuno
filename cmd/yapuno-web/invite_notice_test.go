package main

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/contacts"
	"github.com/pblumer/yapuno/internal/signup"
)

// The inviting guardian approved before knowing who registers. The mail is
// how the guardians of the inviting child learn that the contact exists.
func TestGuardiansOfTheInvitingChildAreToldAboutTheNewContact(t *testing.T) {
	s := newInvitingServer(t)
	anna := s.issue("guardian-a", "tenant-a")
	if page := s.do(http.MethodGet, "/family/invite", anna, nil).Body.String(); !strings.Contains(page, "schickt euch dann eine E-Mail") {
		t.Fatalf("invite page does not announce the notice: %s", page)
	}
	// Registrations that connect nobody cause no notice.
	s.register(s.inviteFor(anna, "child-a"), "carla@example.org", "Momo", "no")
	s.register(s.inviteFor(anna, ""), "dario@example.org", "Nia", "yes")
	if _, location := s.register(s.inviteFor(anna, "child-a"), "bruno@example.org", "Leo", "yes"); location != "/family?done=connected" {
		t.Fatalf("sent to %q", location)
	}
	// Both guardians of Alex are told, each about their own child only.
	got := s.mailer.contacts(2)
	if len(got) != 2 || got[0] != "anna@example.org about Alex" || got[1] != "ben@example.org about Alex" {
		t.Fatalf("notices: %v", got)
	}
	for _, notice := range got {
		if strings.Contains(notice, "Leo") || strings.Contains(notice, "bruno") {
			t.Fatalf("a notice names the other family: %q", notice)
		}
	}
	// A contact made with a code was confirmed by name and needs no notice.
	code, err := s.contacts.Invite("guardian-a", "tenant-a", "child-a")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.contacts.Redeem("guardian-b", "tenant-b", "child-b", code)
	if err != nil {
		t.Fatal(err)
	}
	if w := s.do(http.MethodPost, "/family/contacts/confirm", anna, url.Values{"csrf_token": {s.csrf(anna, "/family/contacts?child_id=child-a")}, "child_id": {"child-a"}, "link_id": {id}, "confirm": {"yes"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("confirm: %d", w.Code)
	}
	time.Sleep(50 * time.Millisecond)
	if got := s.mailer.contacts(2); len(got) != 2 {
		t.Fatalf("notices after a contact by code: %v", got)
	}
}

func TestALockedGuardianIsNotToldAboutTheNewContact(t *testing.T) {
	s := newSecurityServer(t, true)
	s.signupPath = filepath.Join(s.root, "signup.json")
	s.chatPath = filepath.Join(s.root, "guardian-chat.log")
	s.start(true)
	anna, ben := s.issue("guardian-a", "tenant-a"), s.issue("guardian-a2", "tenant-a")
	if w := s.act(ben, "lock", url.Values{"confirm": {"yes"}}); w.Code != http.StatusOK {
		t.Fatalf("lock: %d %s", w.Code, w.Body)
	}
	if _, location := s.register(s.inviteFor(anna, "child-a"), "bruno@example.org", "Leo", "yes"); location != "/family?done=connected" {
		t.Fatalf("sent to %q", location)
	}
	time.Sleep(50 * time.Millisecond)
	if got := s.mailer.contacts(1); len(got) != 1 || got[0] != "anna@example.org about Alex" {
		t.Fatalf("notices: %v", got)
	}
}

// registrationMailer can send sign-in and registration links, nothing else.
type registrationMailer struct{ links *noticeMailer }

func (m registrationMailer) SendSignInLink(ctx context.Context, email, link string) error {
	return m.links.SendSignInLink(ctx, email, link)
}

func (m registrationMailer) SendRegistrationLink(ctx context.Context, email, link string) error {
	return m.links.SendRegistrationLink(ctx, email, link)
}

func TestWithoutAMailerForNoticesInvitationsStillConnectAndPromiseNoMail(t *testing.T) {
	s := newSecurityServer(t, false)
	invitations, err := signup.Open(filepath.Join(s.root, "signup.json"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	contactStore, err := contacts.Open(filepath.Join(s.root, "contacts.json"), s.families, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if s.handler, err = newHandler("https://app.example.org", s.families, s.guardians, registrationMailer{links: s.mailer}, s.sessions, optionalStores{pairing: s.pairing, signup: invitations, contacts: contactStore}); err != nil {
		t.Fatal(err)
	}
	anna := s.issue("guardian-a", "tenant-a")
	if page := s.do(http.MethodGet, "/family/invite", anna, nil).Body.String(); !strings.Contains(page, "Ja, mit Alex") || strings.Contains(page, "E-Mail") {
		t.Fatalf("invite page: %s", page)
	}
	if _, location := s.register(s.inviteFor(anna, "child-a"), "bruno@example.org", "Leo", "yes"); location != "/family?done=connected" {
		t.Fatalf("sent to %q", location)
	}
	if len(contactStore.Connections(contacts.Party{TenantID: "tenant-a", ChildID: "child-a"})) != 1 {
		t.Fatal("the children are not connected")
	}
}

// Only a connection that was really made is announced, and it is announced
// to the inviting side.
func TestTheNoticeFollowsASuccessfulConnectionOfTheInvitingChild(t *testing.T) {
	s := newInvitingServer(t)
	var told [][2]string
	offer := invitedContacts{families: s.families, contacts: s.contacts, notify: func(tenantID, childID string) { told = append(told, [2]string{tenantID, childID}) }}
	if err := offer.Connect("guardian-b", "tenant-a", "child-a", "guardian-b", "tenant-b", "child-b"); err == nil {
		t.Fatal("connected without authority")
	}
	if len(told) != 0 {
		t.Fatalf("a refused connection was announced: %v", told)
	}
	if err := offer.Connect("guardian-a", "tenant-a", "child-a", "guardian-b", "tenant-b", "child-b"); err != nil {
		t.Fatal(err)
	}
	if len(told) != 1 || told[0] != [2]string{"tenant-a", "child-a"} {
		t.Fatalf("announced: %v", told)
	}
	// The same pair again: nothing new, nothing announced.
	if err := offer.Connect("guardian-a", "tenant-a", "child-a", "guardian-b", "tenant-b", "child-b"); err == nil || len(told) != 1 {
		t.Fatalf("second connection: %v, announced %v", err, told)
	}
}
