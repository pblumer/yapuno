package contacts

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/authz"
)

func names(entries []Entry) string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return strings.Join(out, ",")
}

func TestChildBlocksAndOnlyOwnGuardianLifts(t *testing.T) {
	f := setup(t)
	id := f.connect(t)
	if !f.store.Allowed(lea, mia) {
		t.Fatal("precondition: contact active")
	}
	if err := f.store.Block(lea, id); err != nil {
		t.Fatal(err)
	}
	if f.store.Allowed(lea, mia) || f.store.Allowed(mia, lea) {
		t.Fatal("blocked contact still allowed")
	}
	_, grant, ok := f.store.Grant(id, mia)
	if !ok || !grant.Blocked || authz.CanContact(grant, mia.Participant(), lea.Participant()) {
		t.Fatalf("grant must carry the block: %+v %v", grant, ok)
	}
	// Neither child lists the other as a contact; only the blocker sees whom they blocked.
	if names(f.store.ContactsOf(lea)) != "" || names(f.store.ContactsOf(mia)) != "" {
		t.Fatal("blocked contact still in a crew list")
	}
	if names(f.store.BlockedBy(lea)) != "Mia" || names(f.store.BlockedBy(mia)) != "" {
		t.Fatalf("blocked lists: %q %q", names(f.store.BlockedBy(lea)), names(f.store.BlockedBy(mia)))
	}
	// Guardians: the blocker's family sees "blocked", the other family a neutral pause.
	own, err := f.store.Overview("anna", lea.TenantID, lea.ChildID)
	if err != nil || names(own.Blocked) != "Mia" || names(own.Paused) != "" || names(own.Contacts) != "" {
		t.Fatalf("blocker's guardian view: %+v %v", own, err)
	}
	other, err := f.store.Overview("bruno", mia.TenantID, mia.ChildID)
	if err != nil || names(other.Paused) != "Lea" || names(other.Blocked) != "" || names(other.Contacts) != "" {
		t.Fatalf("other guardian's view: %+v %v", other, err)
	}
	// The relationship still exists, so messages are kept for a later release.
	if !f.store.Exists(id) {
		t.Fatal("blocking must not delete the relationship")
	}
	// The other family, strangers and the blocked child's own guardian cannot lift it.
	for name, attempt := range map[string]func() error{
		"other family":     func() error { return f.store.Unblock("bruno", mia.TenantID, mia.ChildID, id) },
		"other as blocker": func() error { return f.store.Unblock("bruno", lea.TenantID, lea.ChildID, id) },
		"stranger":         func() error { return f.store.Unblock("carla", noah.TenantID, noah.ChildID, id) },
		"sibling's name":   func() error { return f.store.Unblock("anna", tim.TenantID, tim.ChildID, id) },
		"unknown guardian": func() error { return f.store.Unblock("nobody", lea.TenantID, lea.ChildID, id) },
		"empty guardian":   func() error { return f.store.Unblock("", lea.TenantID, lea.ChildID, id) },
		"unknown link":     func() error { return f.store.Unblock("anna", lea.TenantID, lea.ChildID, strings.Repeat("0", 32)) },
	} {
		if err := attempt(); err == nil {
			t.Errorf("%s lifted the block", name)
		}
		if f.store.Allowed(lea, mia) {
			t.Fatalf("%s: contact allowed again", name)
		}
	}
	// Either equal guardian of the blocking child lifts it.
	if err := f.store.Unblock("albert", lea.TenantID, lea.ChildID, id); err != nil {
		t.Fatal(err)
	}
	if !f.store.Allowed(lea, mia) || names(f.store.ContactsOf(mia)) != "Lea" || names(f.store.BlockedBy(lea)) != "" {
		t.Fatal("contact not restored after the guardian lifted the block")
	}
	if err := f.store.Unblock("anna", lea.TenantID, lea.ChildID, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lifting a block that is not there: %v", err)
	}
}

func TestBlockNeedsTheChildsOwnActiveRelationship(t *testing.T) {
	f := setup(t)
	id := f.connect(t)
	for name, p := range map[string]Party{"stranger": noah, "sibling": tim, "empty": {}, "unknown child": {TenantID: "family-a", ChildID: "ghost"}} {
		if err := f.store.Block(p, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := f.store.Block(lea, strings.Repeat("f", 32)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown link: %v", err)
	}
	if !f.store.Allowed(lea, mia) {
		t.Fatal("a refused block changed the contact")
	}
	// A pending request is not a contact yet and cannot be blocked.
	code, err := f.store.Invite("anna", tim.TenantID, tim.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := f.store.Redeem("bruno", mia.TenantID, mia.ChildID, code)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Block(mia, pending); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending request blocked: %v", err)
	}
}

func TestBothChildrenCanBlockAndEachSideLiftsOnlyItsOwn(t *testing.T) {
	f := setup(t)
	id := f.connect(t)
	if err := f.store.Block(lea, id); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Block(lea, id); err != nil {
		t.Fatalf("blocking twice must be harmless: %v", err)
	}
	if err := f.store.Block(mia, id); err != nil {
		t.Fatal(err)
	}
	if names(f.store.BlockedBy(lea)) != "Mia" || names(f.store.BlockedBy(mia)) != "Lea" {
		t.Fatal("both blocks must be recorded")
	}
	// Each family sees its own block, not a pause.
	if v, _ := f.store.Overview("bruno", mia.TenantID, mia.ChildID); names(v.Blocked) != "Lea" || names(v.Paused) != "" {
		t.Fatalf("view with both blocks: %+v", v)
	}
	if err := f.store.Unblock("anna", lea.TenantID, lea.ChildID, id); err != nil {
		t.Fatal(err)
	}
	if f.store.Allowed(lea, mia) {
		t.Fatal("one family lifted the other family's block")
	}
	if v, _ := f.store.Overview("anna", lea.TenantID, lea.ChildID); names(v.Paused) != "Mia" || names(v.Blocked) != "" {
		t.Fatalf("after lifting own block the other block shows as pause: %+v", v)
	}
	if err := f.store.Unblock("bruno", mia.TenantID, mia.ChildID, id); err != nil {
		t.Fatal(err)
	}
	if !f.store.Allowed(lea, mia) {
		t.Fatal("contact not restored after both blocks were lifted")
	}
}

func TestBlockIsDurableAndEndsWithTheRelationship(t *testing.T) {
	f := setup(t)
	id := f.connect(t)
	if err := f.store.Block(mia, id); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(f.path, f.families, f.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Allowed(lea, mia) || names(reopened.BlockedBy(mia)) != "Lea" {
		t.Fatal("block lost on restart")
	}
	if info, _ := os.Stat(f.path); info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v", info.Mode().Perm())
	}
	// Either family may still end the relationship; a new one starts unblocked.
	if err := reopened.Remove("anna", lea.TenantID, lea.ChildID, id); err != nil {
		t.Fatal(err)
	}
	if names(reopened.BlockedBy(mia)) != "" || reopened.Exists(id) {
		t.Fatal("block survived the end of the relationship")
	}
	f.store = reopened
	if again := f.connect(t); !reopened.Allowed(lea, mia) || again == id {
		t.Fatal("a new, mutually approved contact must not inherit the old block")
	}
}

func TestRevokedGuardianCannotLiftABlock(t *testing.T) {
	f := setup(t)
	id := f.connect(t)
	if err := f.store.Block(lea, id); err != nil {
		t.Fatal(err)
	}
	revoked := map[string]bool{}
	s, err := Open(f.path, revoking{Families: f.families, revoked: revoked}, f.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	revoked["anna"] = true
	if err := s.Unblock("anna", lea.TenantID, lea.ChildID, id); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked guardian: %v", err)
	}
	if s.Allowed(lea, mia) {
		t.Fatal("revoked guardian lifted the block")
	}
}
