package contacts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type fixture struct {
	store    *Store
	families *familystore.Store
	clock    *clock
	path     string
}

var (
	lea = Party{TenantID: "family-a", ChildID: "lea"}
	tim = Party{TenantID: "family-a", ChildID: "tim"}
	mia = Party{TenantID: "family-b", ChildID: "mia"}
	// noah deliberately reuses the child id "lea" in another family.
	noah = Party{TenantID: "family-c", ChildID: "lea"}
)

func setup(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	families, err := familystore.Open(filepath.Join(dir, "families.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		party    Party
		name     string
		guardian string
	}{
		{lea, "Lea", "anna"}, {tim, "Tim", "anna"}, {mia, "Mia", "bruno"}, {noah, "Noah", "carla"},
	} {
		grant := authz.GuardianGrant{GuardianID: c.guardian, ChildID: c.party.ChildID, TenantID: c.party.TenantID, Active: true, CanManageDevices: true, CanApproveContacts: true}
		if err := families.Bootstrap(familydashboard.Child{ID: c.party.ChildID, TenantID: c.party.TenantID, DisplayName: c.name}, grant); err != nil {
			t.Fatal(err)
		}
	}
	// A second, equal guardian for Lea.
	if err := families.BootstrapGuardian(authz.GuardianGrant{GuardianID: "albert", ChildID: "lea", TenantID: "family-a", Active: true, CanManageDevices: true, CanApproveContacts: true}); err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	path := filepath.Join(dir, "contacts.json")
	s, err := Open(path, families, c.now)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{store: s, families: families, clock: c, path: path}
}

// connect runs the full happy path: Anna invites for Lea, Bruno redeems for
// Mia, Anna confirms.
func (f *fixture) connect(t *testing.T) string {
	t.Helper()
	code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, code)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Confirm("anna", lea.TenantID, lea.ChildID, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMutualApprovalIsRequiredInBothDirections(t *testing.T) {
	f := setup(t)
	if f.store.Allowed(lea, mia) {
		t.Fatal("contact allowed without any approval")
	}
	code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	if f.store.Allowed(lea, mia) || f.store.Allowed(mia, lea) {
		t.Fatal("an open invitation must not allow contact")
	}
	id, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, code)
	if err != nil {
		t.Fatal(err)
	}
	if f.store.Allowed(lea, mia) || f.store.Allowed(mia, lea) {
		t.Fatal("one-sided approval must not allow contact")
	}
	if err := f.store.Confirm("anna", lea.TenantID, lea.ChildID, id); err != nil {
		t.Fatal(err)
	}
	if !f.store.Allowed(lea, mia) || !f.store.Allowed(mia, lea) {
		t.Fatal("mutually approved contact denied")
	}
	for name, pair := range map[string][2]Party{
		"sibling of approved child":    {tim, mia},
		"same child id, other family":  {noah, mia},
		"unrelated family":             {lea, noah},
		"self":                         {lea, lea},
		"unknown child":                {lea, {TenantID: "family-b", ChildID: "ghost"}},
		"approved id in wrong tenant":  {lea, {TenantID: "family-c", ChildID: "mia"}},
		"empty party":                  {lea, {}},
		"tenant only":                  {lea, {TenantID: "family-b"}},
		"swapped tenant on own side":   {{TenantID: "family-b", ChildID: "lea"}, mia},
		"approved pair, empty tenants": {{ChildID: "lea"}, {ChildID: "mia"}},
	} {
		if f.store.Allowed(pair[0], pair[1]) {
			t.Errorf("%s: contact allowed", name)
		}
	}
}

func TestOnlyAuthorizedGuardiansMayAct(t *testing.T) {
	f := setup(t)
	for name, who := range map[string][3]string{
		"other family's guardian":  {"bruno", "family-a", "lea"},
		"guardian with own tenant": {"bruno", "family-b", "lea"},
		"unknown guardian":         {"mallory", "family-a", "lea"},
		"empty guardian":           {"", "family-a", "lea"},
		"unknown child":            {"anna", "family-a", "ghost"},
		"child of other tenant":    {"anna", "family-b", "mia"},
	} {
		if _, err := f.store.Invite(who[0], who[1], who[2]); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s: invite returned %v", name, err)
		}
		if _, err := f.store.Overview(who[0], who[1], who[2]); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s: overview returned %v", name, err)
		}
	}
	code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	// Carla has no authority over Mia and cannot redeem on her behalf.
	if _, err := f.store.Redeem("carla", mia.TenantID, mia.ChildID, code); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unauthorized redeem: %v", err)
	}
	id, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, code)
	if err != nil {
		t.Fatalf("a rejected redeem must not consume the invitation: %v", err)
	}
	// The redeeming family cannot confirm its own request.
	if err := f.store.Confirm("bruno", mia.TenantID, mia.ChildID, id); !errors.Is(err, ErrForbidden) {
		t.Fatalf("invitee confirmed own request: %v", err)
	}
	// Anna may act for Tim, but this request belongs to Lea.
	if err := f.store.Confirm("anna", tim.TenantID, tim.ChildID, id); !errors.Is(err, ErrForbidden) {
		t.Fatalf("confirmed through sibling: %v", err)
	}
	if err := f.store.Confirm("carla", noah.TenantID, noah.ChildID, id); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger confirmed: %v", err)
	}
	if err := f.store.Remove("carla", noah.TenantID, noah.ChildID, id); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger removed request: %v", err)
	}
	if f.store.Allowed(lea, mia) {
		t.Fatal("rejected confirmations activated the contact")
	}
	// Equal guardians: Albert may confirm although Anna created the invitation.
	if err := f.store.Confirm("albert", lea.TenantID, lea.ChildID, id); err != nil {
		t.Fatalf("equal guardian could not confirm: %v", err)
	}
	if !f.store.Allowed(lea, mia) {
		t.Fatal("contact not active after confirmation")
	}
}

func TestInvitationIsSingleUseAndExpires(t *testing.T) {
	f := setup(t)
	code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, code); err != nil {
		t.Fatal(err)
	}
	// A second family with the same code learns that it was already used.
	if _, err := f.store.Redeem("carla", noah.TenantID, noah.ChildID, code); !errors.Is(err, ErrConsumed) {
		t.Fatalf("replayed code: %v", err)
	}
	expiring, err := f.store.Invite("anna", tim.TenantID, tim.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	f.clock.advance(InvitationTTL)
	if _, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, expiring); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired code: %v", err)
	}
	if f.store.Allowed(tim, mia) {
		t.Fatal("expired invitation created a contact")
	}
}

func TestCodeFormatAndGuessing(t *testing.T) {
	f := setup(t)
	code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.ReplaceAll(code, "-", ""); len(got) != 26 {
		t.Fatalf("code %q does not carry 128 bits in base32", code)
	}
	raw, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), code) || strings.Contains(string(raw), strings.ReplaceAll(code, "-", "")) {
		t.Fatal("invitation code stored in clear text")
	}
	for _, bad := range []string{"", "ABC", strings.Repeat("A", 26), strings.Repeat("1", 26), code + "A", "<script>" + code} {
		if _, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("code %q: %v", bad, err)
		}
	}
	// Typing variants of the real code are accepted: lower case and spaces.
	typed := strings.ToLower(strings.ReplaceAll(code, "-", " "))
	if _, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, "  "+typed+" "); err != nil {
		t.Fatalf("normalized code rejected: %v", err)
	}
}

func TestRedeemRejectsSelfAndDuplicates(t *testing.T) {
	f := setup(t)
	code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Redeem("anna", lea.TenantID, lea.ChildID, code); !errors.Is(err, ErrInvalid) {
		t.Fatalf("child connected to itself: %v", err)
	}
	id, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, code)
	if err != nil {
		t.Fatalf("self-redeem must not consume the invitation: %v", err)
	}
	// While a request or contact exists for the pair, neither direction may add another.
	for _, dir := range []struct {
		inviter, redeemer [3]string
	}{
		{[3]string{"anna", "family-a", "lea"}, [3]string{"bruno", "family-b", "mia"}},
		{[3]string{"bruno", "family-b", "mia"}, [3]string{"anna", "family-a", "lea"}},
	} {
		again, err := f.store.Invite(dir.inviter[0], dir.inviter[1], dir.inviter[2])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.Redeem(dir.redeemer[0], dir.redeemer[1], dir.redeemer[2], again); !errors.Is(err, ErrExists) {
			t.Fatalf("duplicate relationship: %v", err)
		}
	}
	if err := f.store.Confirm("anna", lea.TenantID, lea.ChildID, id); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Confirm("anna", lea.TenantID, lea.ChildID, id); !errors.Is(err, ErrNotPending) {
		t.Fatalf("double confirmation: %v", err)
	}
}

func TestSiblingsNeedExplicitApprovalToo(t *testing.T) {
	f := setup(t)
	if f.store.Allowed(lea, tim) {
		t.Fatal("siblings are contacts without approval")
	}
	code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.store.Redeem("anna", tim.TenantID, tim.ChildID, code)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Confirm("anna", lea.TenantID, lea.ChildID, id); err != nil {
		t.Fatal(err)
	}
	if !f.store.Allowed(lea, tim) {
		t.Fatal("approved sibling contact denied")
	}
}

func TestEitherFamilyCanEndPendingAndActiveRelationships(t *testing.T) {
	for _, remover := range [][3]string{{"anna", "family-a", "lea"}, {"bruno", "family-b", "mia"}, {"albert", "family-a", "lea"}} {
		for _, active := range []bool{false, true} {
			f := setup(t)
			code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
			if err != nil {
				t.Fatal(err)
			}
			id, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, code)
			if err != nil {
				t.Fatal(err)
			}
			if active {
				if err := f.store.Confirm("anna", lea.TenantID, lea.ChildID, id); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.store.Remove(remover[0], remover[1], remover[2], id); err != nil {
				t.Fatalf("%s could not remove (active=%v): %v", remover[0], active, err)
			}
			if f.store.Allowed(lea, mia) || f.store.Allowed(mia, lea) {
				t.Fatal("contact still allowed after removal")
			}
			if err := f.store.Confirm("anna", lea.TenantID, lea.ChildID, id); !errors.Is(err, ErrNotFound) {
				t.Fatalf("removed request confirmed: %v", err)
			}
			if err := f.store.Remove(remover[0], remover[1], remover[2], id); !errors.Is(err, ErrNotFound) {
				t.Fatalf("second removal: %v", err)
			}
			// A fresh invitation is required to reconnect.
			again, err := f.store.Invite("bruno", mia.TenantID, mia.ChildID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.Redeem("anna", lea.TenantID, lea.ChildID, again); err != nil {
				t.Fatalf("reconnect after removal: %v", err)
			}
			if f.store.Allowed(lea, mia) {
				t.Fatal("reconnect skipped the second approval")
			}
		}
	}
}

func TestPendingRequestExpires(t *testing.T) {
	f := setup(t)
	code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, code)
	if err != nil {
		t.Fatal(err)
	}
	f.clock.advance(RequestTTL)
	if err := f.store.Confirm("anna", lea.TenantID, lea.ChildID, id); !errors.Is(err, ErrExpired) {
		t.Fatalf("stale request confirmed: %v", err)
	}
	if f.store.Allowed(lea, mia) {
		t.Fatal("stale request became a contact")
	}
	view, err := f.store.Overview("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Incoming) != 0 {
		t.Fatal("expired request still listed")
	}
	// An active contact does not expire.
	active := setup(t)
	active.connect(t)
	active.clock.advance(10 * RequestTTL)
	if !active.store.Allowed(lea, mia) {
		t.Fatal("active contact expired")
	}
}

func TestOverviewShowsOnlyOwnRelationshipsAndNoForeignIdentifiers(t *testing.T) {
	f := setup(t)
	code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Invite("anna", lea.TenantID, lea.ChildID); err != nil {
		t.Fatal(err)
	}
	id, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, code)
	if err != nil {
		t.Fatal(err)
	}
	inviter, err := f.store.Overview("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	if len(inviter.Incoming) != 1 || inviter.Incoming[0].ID != id || inviter.Incoming[0].Name != "Mia" || len(inviter.Outgoing) != 0 || len(inviter.Contacts) != 0 || inviter.OpenInvitations != 1 {
		t.Fatalf("inviter view: %+v", inviter)
	}
	invitee, err := f.store.Overview("bruno", mia.TenantID, mia.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	if len(invitee.Outgoing) != 1 || invitee.Outgoing[0].Name != "Lea" || len(invitee.Incoming) != 0 || invitee.OpenInvitations != 0 {
		t.Fatalf("invitee view: %+v", invitee)
	}
	for _, other := range [][3]string{{"anna", "family-a", "tim"}, {"carla", "family-c", "lea"}} {
		view, err := f.store.Overview(other[0], other[1], other[2])
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Incoming)+len(view.Outgoing)+len(view.Contacts) != 0 || view.OpenInvitations != 0 {
			t.Fatalf("relationship leaked to %v: %+v", other, view)
		}
	}
	if err := f.store.Confirm("anna", lea.TenantID, lea.ChildID, id); err != nil {
		t.Fatal(err)
	}
	inviter, _ = f.store.Overview("anna", lea.TenantID, lea.ChildID)
	if len(inviter.Contacts) != 1 || inviter.Contacts[0].Name != "Mia" || len(inviter.Incoming) != 0 {
		t.Fatalf("after confirmation: %+v", inviter)
	}
	// Renames in the other family show up; no snapshot of the name is kept.
	if err := f.families.RenameChild("bruno", mia.TenantID, mia.ChildID, "Mia K."); err != nil {
		t.Fatal(err)
	}
	inviter, _ = f.store.Overview("anna", lea.TenantID, lea.ChildID)
	if inviter.Contacts[0].Name != "Mia K." {
		t.Fatalf("stale name: %+v", inviter.Contacts)
	}
}

func TestOpenInvitationLimitPerChild(t *testing.T) {
	f := setup(t)
	for i := 0; i < MaxOpenInvitations; i++ {
		if _, err := f.store.Invite("anna", lea.TenantID, lea.ChildID); err != nil {
			t.Fatalf("invitation %d: %v", i, err)
		}
	}
	if _, err := f.store.Invite("anna", lea.TenantID, lea.ChildID); !errors.Is(err, ErrLimit) {
		t.Fatalf("limit not enforced: %v", err)
	}
	if _, err := f.store.Invite("anna", tim.TenantID, tim.ChildID); err != nil {
		t.Fatalf("limit must be per child: %v", err)
	}
	f.clock.advance(InvitationTTL)
	if _, err := f.store.Invite("anna", lea.TenantID, lea.ChildID); err != nil {
		t.Fatalf("expired invitations still count: %v", err)
	}
}

func TestPersistenceAndPrivateFile(t *testing.T) {
	f := setup(t)
	f.connect(t)
	info, err := os.Stat(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("store file is not private: %v", info.Mode().Perm())
	}
	reopened, err := Open(f.path, f.families, f.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Allowed(lea, mia) || reopened.Allowed(tim, mia) {
		t.Fatal("contact state not persisted exactly")
	}
}

func TestOpenFailsClosed(t *testing.T) {
	f := setup(t)
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	link := `{"id":"x","inviter":{"tenant_id":"family-a","child_id":"lea"},"invitee":{"tenant_id":"family-b","child_id":"mia"},"active":true,"expires":"2026-12-01T00:00:00Z"}`
	for name, path := range map[string]string{
		"corrupt":        write("corrupt.json", "{not json", 0600),
		"world readable": write("shared.json", `{"invitations":[],"links":[]}`, 0644),
		"unknown field":  write("unknown.json", `{"invitations":[],"links":[],"admin":true}`, 0600),
		"duplicate pair": write("dup.json", `{"links":[`+link+`,`+strings.Replace(link, `"x"`, `"y"`, 1)+`]}`, 0600),
		"self link":      write("self.json", `{"links":[`+strings.Replace(link, `"family-b","child_id":"mia"`, `"family-a","child_id":"lea"`, 1)+`]}`, 0600),
		"missing party":  write("party.json", `{"links":[`+strings.Replace(link, `"child_id":"mia"`, `"child_id":""`, 1)+`]}`, 0600),
	} {
		if _, err := Open(path, f.families, f.clock.now); err == nil {
			t.Errorf("%s: store opened", name)
		}
	}
	if _, err := Open("", f.families, f.clock.now); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, err := Open(filepath.Join(dir, "new.json"), nil, f.clock.now); err == nil {
		t.Fatal("missing family directory accepted")
	}
}

// A removed child profile must end its contacts without any further action.
type vanishing struct {
	Families
	gone map[Party]bool
}

func (v vanishing) ChildName(tenantID, childID string) (string, bool) {
	if v.gone[Party{TenantID: tenantID, ChildID: childID}] {
		return "", false
	}
	return v.Families.ChildName(tenantID, childID)
}

func TestContactEndsWhenAChildProfileDisappears(t *testing.T) {
	f := setup(t)
	f.connect(t)
	gone := map[Party]bool{}
	s, err := Open(f.path, vanishing{Families: f.families, gone: gone}, f.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Allowed(lea, mia) {
		t.Fatal("precondition: contact should be active")
	}
	gone[mia] = true
	if s.Allowed(lea, mia) || s.Allowed(mia, lea) {
		t.Fatal("contact with a removed child still allowed")
	}
	view, err := s.Overview("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Contacts) != 0 {
		t.Fatal("removed child still listed")
	}
}

func TestConcurrentRedeemHasExactlyOneWinner(t *testing.T) {
	f := setup(t)
	code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	redeemers := [][3]string{{"bruno", "family-b", "mia"}, {"carla", "family-c", "lea"}, {"anna", "family-a", "tim"}}
	results := make([]error, len(redeemers))
	var wg sync.WaitGroup
	for i, r := range redeemers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = f.store.Redeem(r[0], r[1], r[2], code)
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrConsumed) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d redeemers won", wins)
	}
}

func TestConcurrentConfirmAndRemoveEndsRemoved(t *testing.T) {
	for i := 0; i < 20; i++ {
		f := setup(t)
		code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
		if err != nil {
			t.Fatal(err)
		}
		id, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, code)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _ = f.store.Confirm("anna", lea.TenantID, lea.ChildID, id) }()
		go func() { defer wg.Done(); _ = f.store.Remove("bruno", mia.TenantID, mia.ChildID, id) }()
		wg.Wait()
		if f.store.Allowed(lea, mia) {
			t.Fatal("removal lost against a concurrent confirmation")
		}
	}
}

// Naming an involved child is not enough: the acting guardian must hold a
// current grant for exactly that child.
func TestInvolvedPartyNamedByUnauthorizedGuardian(t *testing.T) {
	f := setup(t)
	code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, code)
	if err != nil {
		t.Fatal(err)
	}
	impostors := [][3]string{
		{"bruno", lea.TenantID, lea.ChildID},   // other family's guardian names the inviter
		{"carla", mia.TenantID, mia.ChildID},   // stranger names the invitee
		{"mallory", lea.TenantID, lea.ChildID}, // unknown guardian
		{"", lea.TenantID, lea.ChildID},
	}
	for _, who := range impostors {
		if err := f.store.Confirm(who[0], who[1], who[2], id); !errors.Is(err, ErrForbidden) {
			t.Errorf("%v confirmed: %v", who, err)
		}
		if err := f.store.Remove(who[0], who[1], who[2], id); !errors.Is(err, ErrForbidden) {
			t.Errorf("%v removed: %v", who, err)
		}
	}
	if err := f.store.Confirm("anna", lea.TenantID, lea.ChildID, id); err != nil {
		t.Fatalf("request was damaged by rejected attempts: %v", err)
	}
	for _, who := range impostors {
		if err := f.store.Remove(who[0], who[1], who[2], id); !errors.Is(err, ErrForbidden) {
			t.Errorf("%v removed active contact: %v", who, err)
		}
	}
	if !f.store.Allowed(lea, mia) {
		t.Fatal("active contact was removed by an unauthorized guardian")
	}
}

// Guardian authority is read at the moment of each action, so a revoked
// guardian loses every contact operation immediately.
type revoking struct {
	Families
	revoked map[string]bool
}

func (r revoking) Cards(guardianID, tenantID string) []familydashboard.ChildCard {
	if r.revoked[guardianID] {
		return nil
	}
	return r.Families.Cards(guardianID, tenantID)
}

func TestRevokedGuardianLosesAllContactOperations(t *testing.T) {
	f := setup(t)
	revoked := map[string]bool{}
	s, err := Open(f.path, revoking{Families: f.families, revoked: revoked}, f.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	code, err := s.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Redeem("bruno", mia.TenantID, mia.ChildID, code)
	if err != nil {
		t.Fatal(err)
	}
	revoked["anna"] = true
	if _, err := s.Invite("anna", lea.TenantID, lea.ChildID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked guardian invited: %v", err)
	}
	if err := s.Confirm("anna", lea.TenantID, lea.ChildID, id); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked guardian confirmed: %v", err)
	}
	if err := s.Remove("anna", lea.TenantID, lea.ChildID, id); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked guardian removed: %v", err)
	}
	if _, err := s.Overview("anna", lea.TenantID, lea.ChildID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked guardian read overview: %v", err)
	}
	other, err := s.Invite("bruno", mia.TenantID, mia.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Redeem("anna", tim.TenantID, tim.ChildID, other); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked guardian redeemed: %v", err)
	}
	// The remaining equal guardian is unaffected.
	if err := s.Confirm("albert", lea.TenantID, lea.ChildID, id); err != nil {
		t.Fatalf("equal guardian blocked: %v", err)
	}
}

// ContactsOf serves an authenticated child session: only active contacts of
// exactly that child, by display name.
func TestContactsOfListsOnlyActiveContactsOfThatChild(t *testing.T) {
	f := setup(t)
	f.connect(t) // lea <-> mia, active
	code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Redeem("carla", noah.TenantID, noah.ChildID, code); err != nil {
		t.Fatal(err) // lea <-> noah, pending only
	}
	names := func(p Party) []string {
		var out []string
		for _, e := range f.store.ContactsOf(p) {
			out = append(out, e.Name)
		}
		return out
	}
	if got := names(lea); len(got) != 1 || got[0] != "Mia" {
		t.Fatalf("lea: %v", got)
	}
	if got := names(mia); len(got) != 1 || got[0] != "Lea" {
		t.Fatalf("mia: %v", got)
	}
	for _, p := range []Party{tim, noah, {}, {TenantID: "family-b", ChildID: "lea"}, {TenantID: "family-a"}} {
		if got := names(p); len(got) != 0 {
			t.Fatalf("%+v sees %v", p, got)
		}
	}
}

// Grant feeds the messaging predicate: it exposes the stored consent record
// of one relationship, but only to a party that is part of it.
func TestGrantIsOnlyVisibleToItsTwoParties(t *testing.T) {
	f := setup(t)
	code, err := f.store.Invite("anna", lea.TenantID, lea.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, code)
	if err != nil {
		t.Fatal(err)
	}
	peer, grant, ok := f.store.Grant(id, lea)
	if !ok || peer != mia || grant.Active || authz.CanContact(grant, lea.Participant(), mia.Participant()) {
		t.Fatalf("pending request must yield an inactive grant: %+v %v", grant, ok)
	}
	if err := f.store.Confirm("anna", lea.TenantID, lea.ChildID, id); err != nil {
		t.Fatal(err)
	}
	for self, want := range map[Party]Party{lea: mia, mia: lea} {
		peer, grant, ok := f.store.Grant(id, self)
		if !ok || peer != want || !authz.CanContact(grant, self.Participant(), peer.Participant()) {
			t.Fatalf("%+v: peer %+v grant %+v ok %v", self, peer, grant, ok)
		}
	}
	for _, outsider := range []Party{tim, noah, {}, {TenantID: "family-b", ChildID: "lea"}} {
		if _, _, ok := f.store.Grant(id, outsider); ok {
			t.Fatalf("grant visible to %+v", outsider)
		}
	}
	for _, bad := range []string{"", "unknown", id + "0"} {
		if _, _, ok := f.store.Grant(bad, lea); ok {
			t.Fatalf("grant found for id %q", bad)
		}
	}
	if err := f.store.Remove("bruno", mia.TenantID, mia.ChildID, id); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := f.store.Grant(id, lea); ok {
		t.Fatal("grant survives removal")
	}
}

func TestExistsFollowsTheRelationshipLifecycle(t *testing.T) {
	f := setup(t)
	id := f.connect(t)
	if !f.store.Exists(id) || f.store.Exists("") || f.store.Exists("unknown") {
		t.Fatal("unexpected existence result")
	}
	if err := f.store.Remove("anna", lea.TenantID, lea.ChildID, id); err != nil {
		t.Fatal(err)
	}
	if f.store.Exists(id) {
		t.Fatal("removed relationship still exists")
	}
}
