package guardiandirectory

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/yapuno/internal/familydashboard"
)

type cards struct{ allowed bool }

func (c *cards) Cards(_, _ string) []familydashboard.ChildCard {
	if !c.allowed {
		return nil
	}
	return []familydashboard.ChildCard{{ID: "child-1"}}
}

func TestDirectoryActiveAndRevocable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guardians.json")
	data := []byte(`{"guardians":[{"email":"parent@example.org","guardian_id":"g-1","tenant_id":"t-1","active":true}]}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	families := &cards{allowed: true}
	dir, err := New(path, families)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := dir.LookupGuardian(context.Background(), "parent@example.org")
	if err != nil || !principal.Active || principal.GuardianID != "g-1" || principal.TenantID != "t-1" {
		t.Fatalf("principal = %+v; err = %v", principal, err)
	}
	if !dir.ActiveGuardian(context.Background(), "g-1", "t-1") || dir.ActiveGuardian(context.Background(), "g-1", "t-2") {
		t.Fatal("guardian access leak")
	}
	families.allowed = false
	if dir.ActiveGuardian(context.Background(), "g-1", "t-1") {
		t.Fatal("grant revocation ignored")
	}
	families.allowed = true
	if err := os.WriteFile(path, []byte(`{"guardians":[{"email":"parent@example.org","guardian_id":"g-1","tenant_id":"t-1","active":false}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if dir.ActiveGuardian(context.Background(), "g-1", "t-1") {
		t.Fatal("identity revocation ignored")
	}
	p, err := dir.LookupGuardian(context.Background(), "parent@example.org")
	if err != nil || p.Active {
		t.Fatalf("revoked principal = %+v; err = %v", p, err)
	}
}

func TestDirectoryDeniesUnsafeFiles(t *testing.T) {
	temp := t.TempDir()
	path := filepath.Join(temp, "guardians.json")
	if err := os.WriteFile(path, []byte(`{"guardians":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path, &cards{}); err == nil {
		t.Fatal("world-readable identity registry accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(temp, "symlink")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := New(link, &cards{}); err == nil {
		t.Fatal("symlink accepted")
	}
	for _, content := range []string{
		`{"guardians":[{"email":"PARENT@EXAMPLE.ORG","guardian_id":"g","tenant_id":"t","active":true}]}`,
		`{"guardians":[{"email":"parent@example.org","guardian_id":"g","tenant_id":"t","active":true},{"email":"parent@example.org","guardian_id":"g2","tenant_id":"t","active":true}]}`,
		`{"guardians":[{"email":"parent@example.org","guardian_id":"g","tenant_id":"t","active":true}],"unexpected":1}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := New(path, &cards{}); err == nil {
			t.Fatalf("unsafe directory accepted: %q", content)
		}
	}
}
