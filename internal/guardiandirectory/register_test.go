package guardiandirectory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/yapuno/internal/familydashboard"
)

// grants is a family reader in which named guardians have a child.
type grants map[string]bool

func (g grants) Cards(guardianID, tenantID string) []familydashboard.ChildCard {
	if g[tenantID+"/"+guardianID] {
		return []familydashboard.ChildCard{{ID: "child"}}
	}
	return nil
}

func writable(t *testing.T, families FamilyReader) (*Directory, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "guardians.json")
	if err := os.WriteFile(path, []byte(`{"guardians":[{"email":"anna@example.org","guardian_id":"anna","tenant_id":"family-a","active":true}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	d, err := New(path, families)
	if err != nil {
		t.Fatal(err)
	}
	return d, path
}

func TestRegisterAdmitsANewGuardianOnceItHasAChild(t *testing.T) {
	families := grants{"family-a/anna": true}
	d, path := writable(t, families)
	ctx := context.Background()
	if err := d.Register(" Bruno@Example.org ", "bruno", "family-b"); err != nil {
		t.Fatal(err)
	}
	if !d.Has("bruno@example.org") || !d.Has("BRUNO@example.org") || d.Has("nobody@example.org") {
		t.Fatal("Has does not follow the directory")
	}
	// Registered but without a child: not admitted yet.
	if p, _ := d.LookupGuardian(ctx, "bruno@example.org"); p.Active {
		t.Fatal("guardian without a child admitted")
	}
	families["family-b/bruno"] = true
	p, err := d.LookupGuardian(ctx, "bruno@example.org")
	if err != nil || !p.Active || p.GuardianID != "bruno" || p.TenantID != "family-b" || p.Email != "bruno@example.org" {
		t.Fatalf("registered guardian: %+v %v", p, err)
	}
	if !d.ActiveGuardian(ctx, "bruno", "family-b") {
		t.Fatal("registered guardian has no session access")
	}
	// The existing guardian is untouched, and the file stays private and well formed.
	if p, _ := d.LookupGuardian(ctx, "anna@example.org"); !p.Active {
		t.Fatal("existing guardian lost")
	}
	info, _ := os.Lstat(path)
	if info.Mode().Perm() != 0600 || !info.Mode().IsRegular() {
		t.Fatalf("file mode: %v", info.Mode())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".yapuno-*")); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
	reopened, err := New(path, families)
	if err != nil || !reopened.Has("bruno@example.org") {
		t.Fatalf("registration not durable: %v", err)
	}
}

func TestRegisterRefusesDuplicatesAndBadInput(t *testing.T) {
	d, path := writable(t, grants{"family-a/anna": true})
	before, _ := os.ReadFile(path)
	for name, c := range map[string][3]string{
		"address in use":          {"anna@example.org", "other", "family-x"},
		"address in use, case":    {"ANNA@example.org", "other", "family-x"},
		"guardian exists":         {"new@example.org", "anna", "family-a"},
		"address with name":       {"Anna <a@example.org>", "g", "t"},
		"address with line break": {"a@example.org\nBcc: x@example.org", "g", "t"},
		"empty address":           {"", "g", "t"},
		"empty guardian":          {"a@example.org", "", "t"},
		"empty tenant":            {"a@example.org", "g", ""},
		"control in id":           {"a@example.org", "g\n", "t"},
	} {
		if err := d.Register(c[0], c[1], c[2]); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := d.Register("anna@example.org", "other", "family-x"); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate address: %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("a refused registration changed the file")
	}
	// The same guardian id in another family is another person.
	if err := d.Register("anna.two@example.org", "anna", "family-z"); err != nil {
		t.Fatalf("same id in another tenant: %v", err)
	}
}

func TestRegisterFailsClosedOnAnUnsafeFile(t *testing.T) {
	for name, breakIt := range map[string]func(path string){
		"readable by group": func(p string) { _ = os.Chmod(p, 0640) },
		"corrupt":           func(p string) { _ = os.WriteFile(p, []byte(`{"guardians":[`), 0600) },
		"missing":           func(p string) { _ = os.Remove(p) },
		"symlink": func(p string) {
			_ = os.Rename(p, p+".real")
			_ = os.Symlink(p+".real", p)
		},
	} {
		d, path := writable(t, grants{})
		breakIt(path)
		if err := d.Register("bruno@example.org", "bruno", "family-b"); err == nil {
			t.Errorf("%s: registration accepted", name)
		}
		// Has must not report "free" when the file cannot be trusted.
		if !d.Has("bruno@example.org") {
			t.Errorf("%s: an unreadable directory reports the address as free", name)
		}
	}
}

func TestConcurrentRegistrationsKeepEveryEntryAndRejectTheSameAddress(t *testing.T) {
	d, _ := writable(t, grants{})
	var wg sync.WaitGroup
	wins := make(chan bool, 40)
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_ = d.Register("user"+string(rune('a'+i))+"@example.org", "g"+string(rune('a'+i)), "t"+string(rune('a'+i)))
		}(i)
		go func(i int) {
			defer wg.Done()
			wins <- d.Register("same@example.org", "same"+string(rune('a'+i)), "s"+string(rune('a'+i))) == nil
		}(i)
	}
	wg.Wait()
	close(wins)
	won := 0
	for ok := range wins {
		if ok {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("the same address was registered %d times", won)
	}
	for i := 0; i < 20; i++ {
		if !d.Has("user" + string(rune('a'+i)) + "@example.org") {
			t.Fatalf("entry %d lost", i)
		}
	}
	// A copy with lock awareness shares the same writer.
	aware, err := d.WithLocks(locks{})
	if err != nil {
		t.Fatal(err)
	}
	if err := aware.Register("same@example.org", "again", "tenant"); !errors.Is(err, ErrExists) {
		t.Fatalf("lock-aware copy: %v", err)
	}
}

func TestRegisterNeverWritesAFileTheDirectoryWouldRefuseToRead(t *testing.T) {
	d, path := writable(t, grants{})
	// Fill the file close to the size the directory accepts.
	var b strings.Builder
	b.WriteString(`{"guardians":[`)
	for i := 0; b.Len() < 1<<20-200; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"email":"user%06d@example.org","guardian_id":"g%06d","tenant_id":"t%06d","active":true}`, i, i, i)
	}
	b.WriteString("]}")
	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if !d.Has("user000000@example.org") {
		t.Fatal("precondition: the large file is readable")
	}
	err := d.Register("one-too-many-"+strings.Repeat("x", 150)+"@example.org", "new", "tenant-new")
	if err == nil {
		t.Fatal("registration accepted although the file would become unreadable")
	}
	// Everyone who was in the directory still is.
	if !d.Has("user000000@example.org") || d.Has("nobody@example.org") {
		t.Fatal("the directory became unreadable")
	}
}
