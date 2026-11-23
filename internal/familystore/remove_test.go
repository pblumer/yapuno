package familystore

import (
	"errors"
	"os"
	"testing"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
)

// removal builds two families. Family a: Mia (guardians Anna and Albert) and
// Tim (guardian Anna only). Family b: Leo (guardian Bruno).
func removal(t *testing.T) (*Store, string) {
	t.Helper()
	s, path := selfService(t)
	if err := s.AddChild("anna", "family-a", "tim", "Tim"); err != nil {
		t.Fatal(err)
	}
	if err := s.BootstrapGuardian(authz.GuardianGrant{GuardianID: "albert", ChildID: "mia", TenantID: "family-a", Active: true, CanManageDevices: true, CanApproveContacts: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFamily("family-b", "bruno", "leo", "Leo"); err != nil {
		t.Fatal(err)
	}
	return s, path
}

func names(cards []familydashboard.ChildCard) string {
	out := ""
	for _, c := range cards {
		out += c.DisplayName + " "
	}
	return out
}

func TestRemoveChildDeletesTheProfileAndEveryGrantForIt(t *testing.T) {
	s, path := removal(t)
	if err := s.MayRemoveChild("anna", "family-a", "mia"); err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := s.RemoveChild("anna", "family-a", "mia"); err != nil {
		t.Fatal(err)
	}
	for _, store := range []*Store{s, reopen(t, path)} {
		if _, ok := store.ChildName("family-a", "mia"); ok {
			t.Fatal("profile still exists")
		}
		if got := names(store.Cards("anna", "family-a")); got != "Tim " {
			t.Fatalf("Anna's children: %q", got)
		}
		// Albert was guardian of Mia only: nothing is left for him.
		if len(store.Cards("albert", "family-a")) != 0 || len(store.GuardiansOf("family-a", "mia")) != 0 {
			t.Fatal("a grant for the removed child is left")
		}
		if got := names(store.Cards("bruno", "family-b")); got != "Leo " {
			t.Fatalf("another family changed: %q", got)
		}
	}
	// The id can be used again without inheriting anything.
	if err := s.AddChild("anna", "family-a", "mia", "Mia zwei"); err != nil {
		t.Fatal(err)
	}
	if len(s.Cards("albert", "family-a")) != 0 {
		t.Fatal("an old grant came back with a reused id")
	}
}

func reopen(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRemoveChildNeedsFullAuthorityForExactlyThatChild(t *testing.T) {
	s, path := removal(t)
	before, _ := os.ReadFile(path)
	for name, who := range map[string][3]string{
		"guardian of another family":   {"bruno", "family-a", "mia"},
		"guardian naming own tenant":   {"bruno", "family-b", "mia"},
		"guardian of the sibling only": {"albert", "family-a", "tim"},
		"unknown guardian":             {"mallory", "family-a", "mia"},
		"empty guardian":               {"", "family-a", "mia"},
		"unknown child":                {"anna", "family-a", "ghost"},
		"empty child":                  {"anna", "family-a", ""},
		"child of another family":      {"anna", "family-b", "leo"},
		"empty tenant":                 {"anna", "", "mia"},
	} {
		if err := s.MayRemoveChild(who[0], who[1], who[2]); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s, check: %v", name, err)
		}
		if err := s.RemoveChild(who[0], who[1], who[2]); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s: %v", name, err)
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("a refused removal changed the store")
	}
	// An equal guardian of that child may remove it.
	if err := s.RemoveChild("albert", "family-a", "mia"); err != nil {
		t.Fatalf("equal guardian: %v", err)
	}
}

// Without a child no guardian of the family can sign in or add one, so the
// last child only goes together with the family.
func TestTheLastChildOfAFamilyIsNotRemovedOnItsOwn(t *testing.T) {
	s, _ := removal(t)
	if err := s.MayRemoveChild("bruno", "family-b", "leo"); !errors.Is(err, ErrLastChild) {
		t.Fatalf("check: %v", err)
	}
	if err := s.RemoveChild("bruno", "family-b", "leo"); !errors.Is(err, ErrLastChild) {
		t.Fatalf("remove: %v", err)
	}
	if err := s.RemoveChild("anna", "family-a", "tim"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveChild("anna", "family-a", "mia"); !errors.Is(err, ErrLastChild) {
		t.Fatalf("last of two: %v", err)
	}
	if got := names(s.Cards("anna", "family-a")); got != "Mia " {
		t.Fatalf("children: %q", got)
	}
}

func TestRemoveFamilyDeletesAllProfilesAndGrantsOfThatFamilyOnly(t *testing.T) {
	s, path := removal(t)
	if err := s.MayRemoveFamily("anna", "family-a"); err != nil {
		t.Fatalf("check: %v", err)
	}
	guardians, err := s.RemoveFamily("anna", "family-a")
	if err != nil {
		t.Fatal(err)
	}
	// Everybody who held a grant in the family is reported, so that their
	// identities and sessions can be removed.
	if len(guardians) != 2 || guardians[0] != "albert" || guardians[1] != "anna" {
		t.Fatalf("guardians: %v", guardians)
	}
	for _, store := range []*Store{s, reopen(t, path)} {
		if len(store.Cards("anna", "family-a")) != 0 || len(store.Cards("albert", "family-a")) != 0 {
			t.Fatal("a grant of the removed family is left")
		}
		for _, child := range []string{"mia", "tim"} {
			if _, ok := store.ChildName("family-a", child); ok {
				t.Fatalf("profile %s still exists", child)
			}
		}
		if got := names(store.Cards("bruno", "family-b")); got != "Leo " {
			t.Fatalf("another family changed: %q", got)
		}
	}
	// The tenant id is free again and starts empty.
	if err := s.CreateFamily("family-a", "zoe", "new", "Neu"); err != nil {
		t.Fatal(err)
	}
	if len(s.Cards("anna", "family-a")) != 0 {
		t.Fatal("an old grant came back with a reused tenant")
	}
}

func TestRemoveFamilyNeedsFullAuthorityForEveryChild(t *testing.T) {
	s, path := removal(t)
	before, _ := os.ReadFile(path)
	for name, who := range map[string][2]string{
		// Albert is an equal guardian of Mia, but Tim is not his to delete.
		"guardian of one child only": {"albert", "family-a"},
		"guardian of another family": {"bruno", "family-a"},
		"own guardian, wrong tenant": {"anna", "family-b"},
		"unknown guardian":           {"mallory", "family-a"},
		"empty guardian":             {"", "family-a"},
		"unknown tenant":             {"anna", "family-z"},
		"empty tenant":               {"anna", ""},
	} {
		if err := s.MayRemoveFamily(who[0], who[1]); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s, check: %v", name, err)
		}
		if guardians, err := s.RemoveFamily(who[0], who[1]); !errors.Is(err, ErrForbidden) || guardians != nil {
			t.Errorf("%s: %v %v", name, guardians, err)
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("a refused removal changed the store")
	}
}

// Somebody has to be left who can sign in and invite.
func TestTheLastFamilyIsNotRemoved(t *testing.T) {
	s, _ := removal(t)
	if _, err := s.RemoveFamily("bruno", "family-b"); err != nil {
		t.Fatal(err)
	}
	if err := s.MayRemoveFamily("anna", "family-a"); !errors.Is(err, ErrLastFamily) {
		t.Fatalf("check: %v", err)
	}
	if guardians, err := s.RemoveFamily("anna", "family-a"); !errors.Is(err, ErrLastFamily) || guardians != nil {
		t.Fatalf("remove: %v %v", guardians, err)
	}
	if got := names(s.Cards("anna", "family-a")); got != "Mia Tim " {
		t.Fatalf("children: %q", got)
	}
}

// Authority that is switched off or partial deletes nothing.
func TestInactiveOrPartialGrantsRemoveNothing(t *testing.T) {
	s, _ := removal(t)
	s.mu.Lock()
	s.state.Grants = append(s.state.Grants,
		authz.GuardianGrant{GuardianID: "devices-only", ChildID: "mia", TenantID: "family-a", Active: true, CanManageDevices: true},
		authz.GuardianGrant{GuardianID: "devices-only", ChildID: "tim", TenantID: "family-a", Active: true, CanManageDevices: true},
		authz.GuardianGrant{GuardianID: "contacts-only", ChildID: "mia", TenantID: "family-a", Active: true, CanApproveContacts: true},
		authz.GuardianGrant{GuardianID: "contacts-only", ChildID: "tim", TenantID: "family-a", Active: true, CanApproveContacts: true},
		authz.GuardianGrant{GuardianID: "former", ChildID: "mia", TenantID: "family-a", CanManageDevices: true, CanApproveContacts: true},
		authz.GuardianGrant{GuardianID: "former", ChildID: "tim", TenantID: "family-a", CanManageDevices: true, CanApproveContacts: true})
	s.mu.Unlock()
	for _, g := range []string{"devices-only", "contacts-only", "former"} {
		if err := s.RemoveChild(g, "family-a", "mia"); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s, child: %v", g, err)
		}
		if err := s.MayRemoveFamily(g, "family-a"); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s, family check: %v", g, err)
		}
		if _, err := s.RemoveFamily(g, "family-a"); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s, family: %v", g, err)
		}
	}
	if got := names(s.Cards("anna", "family-a")); got != "Mia Tim " {
		t.Fatalf("children: %q", got)
	}
	// Everybody with any grant in the family is reported when it goes.
	guardians, err := s.RemoveFamily("anna", "family-a")
	if err != nil || len(guardians) != 5 {
		t.Fatalf("guardians: %v %v", guardians, err)
	}
}

// Child ids are only unique within a family.
func TestRemovingAChildLeavesAChildWithTheSameIdInAnotherFamily(t *testing.T) {
	s, path := removal(t)
	if err := s.CreateFamily("family-c", "carla", "mia", "Mia C"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveChild("anna", "family-a", "mia"); err != nil {
		t.Fatal(err)
	}
	for _, store := range []*Store{s, reopen(t, path)} {
		if name, ok := store.ChildName("family-c", "mia"); !ok || name != "Mia C" {
			t.Fatal("a child of another family was removed")
		}
		if got := names(store.Cards("carla", "family-c")); got != "Mia C " {
			t.Fatalf("the other family's grant: %q", got)
		}
		if _, ok := store.ChildName("family-a", "mia"); ok {
			t.Fatal("the child was not removed")
		}
	}
}
