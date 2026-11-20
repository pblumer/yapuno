package guardiandirectory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type locks map[string]bool

func (l locks) Locked(guardianID, tenantID string) bool { return l[tenantID+"/"+guardianID] }

const twoParents = `{"guardians":[{"email":"anna@example.org","guardian_id":"anna","tenant_id":"family-1","active":true},{"email":"ben@example.org","guardian_id":"ben","tenant_id":"family-1","active":true},{"email":"old@example.org","guardian_id":"old","tenant_id":"family-1","active":false}]}`

func lockedDirectory(t *testing.T, l LockReader, families FamilyReader) *Directory {
	t.Helper()
	path := filepath.Join(t.TempDir(), "guardians.json")
	if err := os.WriteFile(path, []byte(twoParents), 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := NewWithLocks(path, families, l)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLockedGuardianCanNeitherSignInNorKeepASession(t *testing.T) {
	state := locks{}
	dir := lockedDirectory(t, state, &cards{allowed: true})
	ctx := context.Background()
	if p, _ := dir.LookupGuardian(ctx, "anna@example.org"); !p.Active {
		t.Fatal("precondition: anna can sign in")
	}
	state["family-1/anna"] = true
	if p, err := dir.LookupGuardian(ctx, "anna@example.org"); err != nil || p.Active || p.GuardianID != "" {
		t.Fatalf("locked guardian still resolves for sign-in: %+v %v", p, err)
	}
	if dir.ActiveGuardian(ctx, "anna", "family-1") {
		t.Fatal("locked guardian keeps an existing session")
	}
	// The partner is unaffected.
	if p, _ := dir.LookupGuardian(ctx, "ben@example.org"); !p.Active || !dir.ActiveGuardian(ctx, "ben", "family-1") {
		t.Fatal("lock leaked to the other guardian")
	}
	// Lifting the lock restores access without a restart.
	delete(state, "family-1/anna")
	if p, _ := dir.LookupGuardian(ctx, "anna@example.org"); !p.Active || !dir.ActiveGuardian(ctx, "anna", "family-1") {
		t.Fatal("access not restored after the lock was lifted")
	}
}

func TestEmailResolvesOnlyActiveUnlockedGuardiansWithChildren(t *testing.T) {
	state := locks{}
	families := &cards{allowed: true}
	dir := lockedDirectory(t, state, families)
	ctx := context.Background()
	if address, ok := dir.Email(ctx, "ben", "family-1"); !ok || address != "ben@example.org" {
		t.Fatalf("active guardian: %q %v", address, ok)
	}
	for name, id := range map[string][2]string{"inactive": {"old", "family-1"}, "unknown": {"nobody", "family-1"}, "other tenant": {"ben", "family-2"}, "empty guardian": {"", "family-1"}, "empty tenant": {"ben", ""}} {
		if address, ok := dir.Email(ctx, id[0], id[1]); ok || address != "" {
			t.Errorf("%s: resolved %q", name, address)
		}
	}
	state["family-1/ben"] = true
	if address, ok := dir.Email(ctx, "ben", "family-1"); ok || address != "" {
		t.Fatalf("locked guardian's address resolved: %q", address)
	}
	delete(state, "family-1/ben")
	families.allowed = false
	if _, ok := dir.Email(ctx, "ben", "family-1"); ok {
		t.Fatal("guardian without any child grant resolved")
	}
	families.allowed = true
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, ok := dir.Email(cancelled, "ben", "family-1"); ok {
		t.Fatal("cancelled context resolved an address")
	}
}

func TestDirectoryWithoutLocksBehavesAsBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guardians.json")
	if err := os.WriteFile(path, []byte(twoParents), 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := New(path, &cards{allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := dir.LookupGuardian(context.Background(), "anna@example.org"); !p.Active || !dir.ActiveGuardian(context.Background(), "anna", "family-1") {
		t.Fatal("directory without lock store denies")
	}
	if address, ok := dir.Email(context.Background(), "anna", "family-1"); !ok || address != "anna@example.org" {
		t.Fatal("email without lock store")
	}
	if _, err := NewWithLocks(path, &cards{allowed: true}, nil); err == nil {
		t.Fatal("NewWithLocks accepted a missing lock reader")
	}
}
