package childweb

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/appearance"
	"github.com/pblumer/yapuno/internal/childsession"
	"github.com/pblumer/yapuno/internal/games"
	"github.com/pblumer/yapuno/internal/messages"
	"github.com/pblumer/yapuno/internal/rating"
)

type familyThreads struct{}

func (familyThreads) FamilyThreads(s childsession.Session) []FamilyThread {
	if s.TenantID != "tenant-a" || s.ChildID != "child-a" {
		return nil
	}
	return []FamilyThread{{ID: strings.Repeat("f", 32), Name: "Mama",
		Guardian: appearance.Subject{Kind: appearance.KindGuardian, TenantID: "tenant-a", ID: "guardian-a"}}}
}

type noMessages struct{}

func (noMessages) Send(messages.Principal, string, string, string) (messages.Message, bool, error) {
	return messages.Message{}, false, messages.ErrForbidden
}
func (noMessages) History(messages.Principal, string, uint64, int) ([]messages.Message, error) {
	return nil, messages.ErrForbidden
}
func (noMessages) Recent(messages.Principal, string, int) ([]messages.Message, error) {
	return nil, messages.ErrForbidden
}

type activeGuardians struct{}

func (activeGuardians) ActiveGuardian(context.Context, string, string) bool { return true }

func TestFamilyChessAndPrivateRating(t *testing.T) {
	pw := newPlayWorld(t)
	if err := pw.store.EnableFamilies(pw.families, activeGuardians{}); err != nil {
		t.Fatal(err)
	}
	ratings, err := rating.Open(filepath.Join(t.TempDir(), "ratings.json"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if pw.handler, err = New(pw.sessions, pw.families, Options{Contacts: pw.contacts, Looks: pw.looks, Messages: noMessages{},
		Family: familyThreads{}, Games: pw.store, Ratings: ratings}); err != nil {
		t.Fatal(err)
	}
	scope := games.FamilyScope("tenant-a", "guardian-a", "child-a")
	home := pw.get("/child", pw.alex).Body.String()
	if !strings.Contains(home, `href="/child/play/`+scope+`"`) || !strings.Contains(home, "Schach mit Mama") {
		t.Fatalf("no family chess link: %s", home)
	}
	if strings.Contains(home, "Schachwertung") {
		t.Fatal("rating shown before any rated game")
	}
	page := pw.get("/child/play/"+scope, pw.alex)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Schach mit Mama") {
		t.Fatalf("family chess page: %d", page.Code)
	}
	f := pw.form(t, pw.alex, scope)
	if rec := pw.post(pw.alex, "/child/play/"+scope+"/invite", url.Values{"csrf_token": {f.csrf}, "client_id": {f.client}, "mode": {"correspondence"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("invite Mama: %d %s", rec.Code, rec.Body.String())
	}
	anna := games.GuardianPrincipal{TenantID: "tenant-a", GuardianID: "guardian-a"}
	v, ok, err := pw.store.GuardianLatest(anna, "child-a")
	if err != nil || !ok || !v.YourTurnToAnswer() {
		t.Fatalf("guardian sees the invitation: %+v %v %v", v, ok, err)
	}
	// Another child cannot reach Alex's family game.
	if rec := pw.get("/child/play/"+scope, pw.bea); rec.Code != http.StatusSeeOther {
		t.Errorf("Bea opened Alex's family game: %d", rec.Code)
	}
	if code, _ := pw.sig(t, pw.bea, scope); code != http.StatusForbidden {
		t.Errorf("Bea polled Alex's family game: %d", code)
	}
	// Each child sees only its own rating.
	ratings.Record("game:x", rating.Player{Kind: rating.Child, TenantID: "tenant-a", ID: "child-a"},
		rating.Player{Kind: rating.Child, TenantID: "tenant-b", ID: "child-b"}, 1)
	alexHome := pw.get("/child", pw.alex).Body.String()
	if !strings.Contains(alexHome, "Deine Schachwertung: <strong>1020</strong> (vorläufig, nach 1 Partie). Nur du siehst diese Zahl.") {
		t.Fatalf("Alex's rating: %s", alexHome)
	}
	beaHome := pw.get("/child", pw.bea).Body.String()
	if !strings.Contains(beaHome, "<strong>980</strong>") || strings.Contains(beaHome, "1020") {
		t.Fatalf("Bea's rating: %s", beaHome)
	}
}
