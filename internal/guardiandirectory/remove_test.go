package guardiandirectory

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

// When a family is deleted its guardians' identities go too: they can no
// longer sign in, and their addresses are free to register again.
func TestRemoveTenantDeletesTheIdentitiesOfThatFamilyOnly(t *testing.T) {
	families := grants{"family-a/anna": true, "family-a/albert": true, "family-b/bruno": true, "family-z/anna": true}
	d, path := writable(t, families)
	ctx := context.Background()
	for _, g := range [][3]string{{"albert@example.org", "albert", "family-a"}, {"bruno@example.org", "bruno", "family-b"}, {"zoe@example.org", "anna", "family-z"}} {
		if err := d.Register(g[0], g[1], g[2]); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := d.RemoveTenant("family-a")
	if err != nil || removed != 2 {
		t.Fatalf("removed %d: %v", removed, err)
	}
	reopened, err := New(path, families)
	if err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]*Directory{"running": d, "reopened": reopened} {
		for _, email := range []string{"anna@example.org", "albert@example.org"} {
			if dir.Has(email) {
				t.Errorf("%s: %s is still registered", name, email)
			}
			if p, _ := dir.LookupGuardian(ctx, email); p.Active {
				t.Errorf("%s: %s can still sign in", name, email)
			}
		}
		if dir.ActiveGuardian(ctx, "anna", "family-a") || dir.ActiveGuardian(ctx, "albert", "family-a") {
			t.Errorf("%s: a removed guardian keeps a session", name)
		}
		// Another family, and the same guardian id in another family, stay.
		if !dir.ActiveGuardian(ctx, "bruno", "family-b") || !dir.ActiveGuardian(ctx, "anna", "family-z") || !dir.Has("zoe@example.org") {
			t.Errorf("%s: an identity of another family was removed", name)
		}
	}
	raw, _ := os.ReadFile(path)
	info, _ := os.Lstat(path)
	if strings.Contains(string(raw), "anna@example.org") || strings.Contains(string(raw), "family-a") || info.Mode().Perm() != 0600 {
		t.Fatalf("file: %v %s", info.Mode(), raw)
	}
	// The address registers again, as a different guardian.
	if err := d.Register("anna@example.org", "anna-new", "family-n"); err != nil {
		t.Fatalf("freed address: %v", err)
	}
	if removed, err := d.RemoveTenant("family-a"); err != nil || removed != 0 {
		t.Fatalf("second removal: %d %v", removed, err)
	}
}

func TestRemoveTenantNeedsATargetAndFailsClosed(t *testing.T) {
	d, path := writable(t, grants{"family-a/anna": true})
	for _, bad := range []string{"", "a\nb"} {
		if _, err := d.RemoveTenant(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if !d.Has("anna@example.org") {
		t.Fatal("an invalid request removed an identity")
	}
	// A directory that cannot be trusted is not rewritten.
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if _, err := d.RemoveTenant("family-a"); err == nil {
		t.Fatal("an unsafe directory was rewritten")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("the unsafe file changed")
	}
}

func TestRemovalsAndRegistrationsDoNotLoseEachOther(t *testing.T) {
	d, _ := writable(t, grants{})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_ = d.Register("new"+string(rune('a'+i))+"@example.org", "g"+string(rune('a'+i)), "family-n")
		}(i)
		go func() {
			defer wg.Done()
			_, _ = d.RemoveTenant("family-a")
		}()
	}
	wg.Wait()
	if d.Has("anna@example.org") {
		t.Fatal("the removed identity is back")
	}
	for i := 0; i < 10; i++ {
		if !d.Has("new" + string(rune('a'+i)) + "@example.org") {
			t.Fatalf("registration %d was lost", i)
		}
	}
}
