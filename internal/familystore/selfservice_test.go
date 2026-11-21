package familystore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
)

func selfService(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "families.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Bootstrap(familydashboard.Child{ID: "mia", TenantID: "family-a", DisplayName: "Mia"},
		authz.GuardianGrant{GuardianID: "anna", ChildID: "mia", TenantID: "family-a", Active: true, CanManageDevices: true, CanApproveContacts: true}); err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestCreateFamilyStartsANewTenantWithOneFullGuardian(t *testing.T) {
	s, path := selfService(t)
	if err := s.CreateFamily("family-b", "bruno", "leo", "  Leo  "); err != nil {
		t.Fatal(err)
	}
	cards := s.Cards("bruno", "family-b")
	if len(cards) != 1 || cards[0].ID != "leo" || cards[0].DisplayName != "Leo" || !cards[0].Permissions.ManageDevices || !cards[0].Permissions.ApproveContacts {
		t.Fatalf("new family: %+v", cards)
	}
	// Nothing crosses tenants: the new guardian sees no other family and vice versa.
	if len(s.Cards("bruno", "family-a")) != 0 || len(s.Cards("anna", "family-b")) != 0 || len(s.Cards("anna", "family-a")) != 1 {
		t.Fatal("a new family touched another tenant")
	}
	reopened, err := Open(path)
	if err != nil || len(reopened.Cards("bruno", "family-b")) != 1 {
		t.Fatalf("new family not durable: %v", err)
	}
}

func TestCreateFamilyNeverJoinsAnExistingTenant(t *testing.T) {
	s, path := selfService(t)
	before, _ := os.ReadFile(path)
	// A fresh guardian must not be able to attach to a tenant that exists.
	if err := s.CreateFamily("family-a", "mallory", "new", "New"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("existing tenant: %v", err)
	}
	for name, c := range map[string][4]string{
		"no tenant":   {"", "g", "c", "Name"},
		"no guardian": {"t", "", "c", "Name"},
		"no child":    {"t", "g", "", "Name"},
		"no name":     {"t", "g", "c", "  "},
		"markup":      {"t", "g", "c", "<b>x</b>"},
		"long name":   {"t", "g", "c", "12345678901234567890123456789012345678901"},
	} {
		if err := s.CreateFamily(c[0], c[1], c[2], c[3]); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("a refused family changed the file")
	}
	if len(s.Cards("mallory", "family-a")) != 0 {
		t.Fatal("refused guardian holds a grant")
	}
}

func TestAddChildNeedsAGuardianOfThatFamily(t *testing.T) {
	s, _ := selfService(t)
	if err := s.AddChild("anna", "family-a", "tim", " Tim "); err != nil {
		t.Fatal(err)
	}
	cards := s.Cards("anna", "family-a")
	if len(cards) != 2 || cards[1].ID != "tim" || cards[1].DisplayName != "Tim" || !cards[1].Permissions.ManageDevices || !cards[1].Permissions.ApproveContacts {
		t.Fatalf("added child: %+v", cards)
	}
	for name, c := range map[string][4]string{
		"stranger":           {"mallory", "family-a", "x1", "X"},
		"guardian elsewhere": {"anna", "family-b", "x2", "X"},
		"existing child id":  {"anna", "family-a", "mia", "X"},
		"invalid name":       {"anna", "family-a", "x3", "a\nb"},
		"empty child id":     {"anna", "family-a", "", "X"},
	} {
		if err := s.AddChild(c[0], c[1], c[2], c[3]); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := s.AddChild("mallory", "family-a", "x1", "X"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger: %v", err)
	}
	if err := s.AddChild("anna", "family-a", "mia", "X"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("existing child id: %v", err)
	}
	if len(s.Cards("anna", "family-a")) != 2 || len(s.Cards("mallory", "family-a")) != 0 {
		t.Fatal("a refused child was stored")
	}
}

func TestAddChildNeedsFullAuthorityAndIgnoresInactiveGrants(t *testing.T) {
	s, _ := selfService(t)
	// A guardian limited to device management may not enlarge the family.
	if err := s.BootstrapGuardian(authz.GuardianGrant{GuardianID: "full", ChildID: "mia", TenantID: "family-a", Active: true, CanManageDevices: true, CanApproveContacts: true}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.state.Grants = append(s.state.Grants,
		authz.GuardianGrant{GuardianID: "devices-only", ChildID: "mia", TenantID: "family-a", Active: true, CanManageDevices: true},
		authz.GuardianGrant{GuardianID: "former", ChildID: "mia", TenantID: "family-a", CanManageDevices: true, CanApproveContacts: true})
	s.mu.Unlock()
	for _, g := range []string{"devices-only", "former"} {
		if err := s.AddChild(g, "family-a", "c-"+g, "X"); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s: %v", g, err)
		}
	}
	// The grant goes to the guardian who adds the child, and to nobody else.
	if err := s.AddChild("full", "family-a", "tim", "Tim"); err != nil {
		t.Fatal(err)
	}
	if len(s.Cards("full", "family-a")) != 2 || len(s.Cards("anna", "family-a")) != 1 {
		t.Fatal("the new child was granted to someone else")
	}
}

func TestAddChildIsLimitedPerFamily(t *testing.T) {
	s, _ := selfService(t)
	for i := 1; i < MaxChildrenPerFamily; i++ {
		if err := s.AddChild("anna", "family-a", fmt.Sprintf("c%d", i), "Kind"); err != nil {
			t.Fatalf("child %d: %v", i, err)
		}
	}
	if err := s.AddChild("anna", "family-a", "one-more", "Kind"); !errors.Is(err, ErrLimit) {
		t.Fatalf("over the limit: %v", err)
	}
	// The limit counts the family, not what one guardian happens to see.
	if err := s.CreateFamily("family-b", "bruno", "leo", "Leo"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddChild("bruno", "family-b", "lia", "Lia"); err != nil {
		t.Fatalf("another family is not affected: %v", err)
	}
}

func TestDiscardFamilyUndoesOnlyAFamilyThatIsStillEmpty(t *testing.T) {
	s, path := selfService(t)
	if err := s.CreateFamily("family-b", "bruno", "leo", "Leo"); err != nil {
		t.Fatal(err)
	}
	// Wrong names never remove anything.
	for _, c := range [][3]string{{"family-b", "anna", "leo"}, {"family-b", "bruno", "mia"}, {"family-a", "bruno", "leo"}, {"", "bruno", "leo"}, {"family-unknown", "bruno", "leo"}} {
		if err := s.DiscardFamily(c[0], c[1], c[2]); err == nil {
			t.Errorf("%v: discarded", c)
		}
	}
	if err := s.DiscardFamily("family-b", "bruno", "leo"); err != nil {
		t.Fatal(err)
	}
	if len(s.Cards("bruno", "family-b")) != 0 || len(s.Cards("anna", "family-a")) != 1 {
		t.Fatal("wrong family discarded")
	}
	if _, ok := s.ChildName("family-b", "leo"); ok {
		t.Fatal("child record left behind")
	}
	reopened, _ := Open(path)
	if _, ok := reopened.ChildName("family-b", "leo"); ok {
		t.Fatal("discard not durable")
	}
	// A second child without any grant still makes the family more than the
	// one fresh registration, so it is kept.
	if err := s.CreateFamily("family-c", "carla", "mo", "Mo"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.state.Children = append(s.state.Children, familydashboard.Child{ID: "extra", TenantID: "family-c", DisplayName: "Extra"})
	s.mu.Unlock()
	if err := s.DiscardFamily("family-c", "carla", "mo"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("family with a second child: %v", err)
	}
	// A family that has grown is real data and is never discarded.
	if err := s.AddChild("anna", "family-a", "tim", "Tim"); err != nil {
		t.Fatal(err)
	}
	if err := s.DiscardFamily("family-a", "anna", "mia"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("grown family: %v", err)
	}
	if len(s.Cards("anna", "family-a")) != 2 {
		t.Fatal("a grown family lost data")
	}
}
