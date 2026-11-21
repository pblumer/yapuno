package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
)

type provisioned struct {
	t                      *testing.T
	root, store, guardians string
}

// firstFamily provisions family-a with guardian anna and child Alex.
func firstFamily(t *testing.T) provisioned {
	t.Helper()
	root := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	p := provisioned{t: t, root: root, store: filepath.Join(root, "families.json"), guardians: filepath.Join(root, "guardians.json")}
	if err := provision(p.store, p.guardians, familydashboard.Child{ID: "alex", TenantID: "family-a", DisplayName: "Alex"}, "anna", "anna@example.org"); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p provisioned) add(tenant, guardian, email, child, name string) (change, error) {
	return extend(p.store, p.guardians, familydashboard.Child{ID: child, TenantID: tenant, DisplayName: name}, guardian, email, false)
}

// children returns the names a guardian sees after signing in with this address.
func (p provisioned) children(email string) string {
	p.t.Helper()
	s, err := familystore.Open(p.store)
	if err != nil {
		p.t.Fatal(err)
	}
	d, err := guardiandirectory.New(p.guardians, s)
	if err != nil {
		p.t.Fatal(err)
	}
	principal, err := d.LookupGuardian(context.Background(), email)
	if err != nil {
		p.t.Fatal(err)
	}
	if !principal.Active {
		return "(no login)"
	}
	var names []string
	for _, c := range s.Cards(principal.GuardianID, principal.TenantID) {
		if !c.Permissions.ManageDevices || !c.Permissions.ApproveContacts {
			p.t.Fatalf("guardian %s lacks equal rights for %s", email, c.ID)
		}
		names = append(names, c.DisplayName)
	}
	return strings.Join(names, ",")
}

func (p provisioned) snapshot() string {
	a, _ := os.ReadFile(p.store)
	b, _ := os.ReadFile(p.guardians)
	return string(a) + "\x00" + string(b)
}

func TestAddSecondFamilySiblingAndEqualGuardian(t *testing.T) {
	p := firstFamily(t)
	// A second family: its guardian sees only its own child.
	got, err := p.add("family-b", "bruno", "bruno@example.org", "bea", "Bea")
	if err != nil || got != (change{guardian: true, child: true}) {
		t.Fatalf("second family: %+v %v", got, err)
	}
	if p.children("bruno@example.org") != "Bea" || p.children("anna@example.org") != "Alex" {
		t.Fatalf("families mixed: %q %q", p.children("bruno@example.org"), p.children("anna@example.org"))
	}
	// A sibling for an existing guardian.
	got, err = p.add("family-a", "anna", "anna@example.org", "ada", "Ada")
	if err != nil || got != (change{child: true}) {
		t.Fatalf("sibling: %+v %v", got, err)
	}
	if p.children("anna@example.org") != "Ada,Alex" {
		t.Fatalf("sibling not visible: %q", p.children("anna@example.org"))
	}
	// A second, equal guardian for one existing child only.
	got, err = p.add("family-a", "albert", "albert@example.org", "alex", "Alex")
	if err != nil || got != (change{guardian: true, grant: true}) {
		t.Fatalf("second guardian: %+v %v", got, err)
	}
	if p.children("albert@example.org") != "Alex" || p.children("anna@example.org") != "Ada,Alex" || p.children("bruno@example.org") != "Bea" {
		t.Fatal("second guardian sees the wrong children")
	}
	// The same child id in another family is a different child.
	got, err = p.add("family-c", "carla", "carla@example.org", "alex", "Alexa")
	if err != nil || got != (change{guardian: true, child: true}) {
		t.Fatalf("same child id elsewhere: %+v %v", got, err)
	}
	if p.children("carla@example.org") != "Alexa" || p.children("albert@example.org") != "Alex" {
		t.Fatal("child ids are not tenant-qualified")
	}
	// Guardian ids are tenant-qualified too: "anna" in another family is another person.
	got, err = p.add("family-z", "anna", "zoe@example.org", "zed", "Zed")
	if err != nil || got != (change{guardian: true, child: true}) {
		t.Fatalf("same guardian id elsewhere: %+v %v", got, err)
	}
	if p.children("zoe@example.org") != "Zed" || p.children("anna@example.org") != "Ada,Alex" {
		t.Fatal("guardian ids are not tenant-qualified")
	}
	for _, path := range []string{p.store, p.guardians} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != 0600 || !info.Mode().IsRegular() {
			t.Fatalf("file %s not private: %v", path, err)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(p.root, ".yapuno-*")); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestAddIsIdempotentAndKeepsARenamedChild(t *testing.T) {
	p := firstFamily(t)
	if _, err := p.add("family-b", "bruno", "bruno@example.org", "bea", "Bea"); err != nil {
		t.Fatal(err)
	}
	// A guardian renames the child in the application.
	s, err := familystore.Open(p.store)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RenameChild("bruno", "family-b", "bea", "Beatrice"); err != nil {
		t.Fatal(err)
	}
	before := p.snapshot()
	for i := 0; i < 3; i++ {
		got, err := p.add("family-b", "bruno", "bruno@example.org", "bea", "Bea")
		if err != nil || got != (change{}) {
			t.Fatalf("repeat %d: %+v %v", i, got, err)
		}
	}
	// Address case and surrounding blanks are not a difference.
	if got, err := p.add("family-b", "bruno", " Bruno@Example.org ", "bea", "Bea"); err != nil || got != (change{}) {
		t.Fatalf("normalised address: %+v %v", got, err)
	}
	if p.snapshot() != before {
		t.Fatal("a repeated run rewrote the files")
	}
	if p.children("bruno@example.org") != "Beatrice" {
		t.Fatal("a repeated run undid the rename")
	}
}

func TestDryRunReportsWithoutWriting(t *testing.T) {
	p := firstFamily(t)
	before := p.snapshot()
	got, err := extend(p.store, p.guardians, familydashboard.Child{ID: "bea", TenantID: "family-b", DisplayName: "Bea"}, "bruno", "bruno@example.org", true)
	if err != nil || got != (change{guardian: true, child: true}) {
		t.Fatalf("dry run: %+v %v", got, err)
	}
	if p.snapshot() != before {
		t.Fatal("dry run wrote")
	}
	// It also reports refusals.
	if _, err := extend(p.store, p.guardians, familydashboard.Child{ID: "bea", TenantID: "family-b", DisplayName: "Bea"}, "bruno", "anna@example.org", true); err == nil {
		t.Fatal("dry run accepted an address that is already in use")
	}
}

func TestAddRefusesConflictsAndUnsafeState(t *testing.T) {
	cases := map[string]struct {
		prepare                              func(p provisioned)
		tenant, guardian, email, child, name string
	}{
		"address of another guardian":       {nil, "family-b", "bruno", "anna@example.org", "bea", "Bea"},
		"address of another guardian, case": {nil, "family-b", "bruno", "ANNA@example.org", "bea", "Bea"},
		"guardian with a new address":       {nil, "family-a", "anna", "anna.new@example.org", "ada", "Ada"},
		"invalid address":                   {nil, "family-b", "bruno", "Bruno <bruno@example.org>", "bea", "Bea"},
		"address with line break":           {nil, "family-b", "bruno", "bruno@example.org\nBcc: x@example.org", "bea", "Bea"},
		"invalid tenant id":                 {nil, "family b", "bruno", "bruno@example.org", "bea", "Bea"},
		"invalid guardian id":               {nil, "family-b", "../bruno", "bruno@example.org", "bea", "Bea"},
		"invalid child id":                  {nil, "family-b", "bruno", "bruno@example.org", "", "Bea"},
		"empty name":                        {nil, "family-b", "bruno", "bruno@example.org", "bea", " "},
		"name with markup":                  {nil, "family-b", "bruno", "bruno@example.org", "bea", "<b>Bea</b>"},
		"inactive guardian entry": {func(p provisioned) {
			_ = os.WriteFile(p.guardians, []byte(`{"guardians":[{"email":"anna@example.org","guardian_id":"anna","tenant_id":"family-a","active":false}]}`), 0600)
		}, "family-a", "anna", "anna@example.org", "ada", "Ada"},
		"unknown field in directory": {func(p provisioned) {
			_ = os.WriteFile(p.guardians, []byte(`{"guardians":[],"admin":true}`), 0600)
		}, "family-b", "bruno", "bruno@example.org", "bea", "Bea"},
		"corrupt family store":    {func(p provisioned) { _ = os.WriteFile(p.store, []byte(`{"children":[`), 0600) }, "family-b", "bruno", "bruno@example.org", "bea", "Bea"},
		"readable directory file": {func(p provisioned) { _ = os.Chmod(p.guardians, 0644) }, "family-b", "bruno", "bruno@example.org", "bea", "Bea"},
		"readable family store":   {func(p provisioned) { _ = os.Chmod(p.store, 0640) }, "family-b", "bruno", "bruno@example.org", "bea", "Bea"},
		"open data directory":     {func(p provisioned) { _ = os.Chmod(p.root, 0755) }, "family-b", "bruno", "bruno@example.org", "bea", "Bea"},
		"symlinked directory file": {func(p provisioned) {
			_ = os.Rename(p.guardians, p.guardians+".real")
			_ = os.Symlink(p.guardians+".real", p.guardians)
		}, "family-b", "bruno", "bruno@example.org", "bea", "Bea"},
		"missing family store":   {func(p provisioned) { _ = os.Remove(p.store) }, "family-b", "bruno", "bruno@example.org", "bea", "Bea"},
		"missing directory file": {func(p provisioned) { _ = os.Remove(p.guardians) }, "family-b", "bruno", "bruno@example.org", "bea", "Bea"},
	}
	for name, c := range cases {
		p := firstFamily(t)
		if c.prepare != nil {
			c.prepare(p)
		}
		before := p.snapshot()
		got, err := p.add(c.tenant, c.guardian, c.email, c.child, c.name)
		if err == nil || got != (change{}) {
			t.Errorf("%s: accepted with %+v", name, got)
		}
		if p.snapshot() != before {
			t.Errorf("%s: a refused request changed the files", name)
		}
		_ = os.Chmod(p.root, 0700)
	}
}

func TestCommandLine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	base := []string{"-families", filepath.Join(root, "families.json"), "-guardians", filepath.Join(root, "guardians.json")}
	first := append(append([]string{}, base...), "-tenant", "family-a", "-guardian", "anna", "-email", "anna@example.org", "-child", "alex", "-name", "Alex")
	second := append(append([]string{}, base...), "-tenant", "family-b", "-guardian", "bruno", "-email", "bruno@example.org", "-child", "bea", "-name", "Bea")
	run := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := runProvision(args, &out, &errOut)
		return code, out.String(), errOut.String()
	}
	// Nothing happens without the operator's confirmation.
	if code, _, errOut := run(first...); code != 2 || !strings.Contains(errOut, "verification") {
		t.Fatalf("without -verified: %d %q", code, errOut)
	}
	if code, _, _ := run(append([]string{"-verified", "-add"}, second...)...); code != 1 {
		t.Fatalf("-add without existing files: %d", code)
	}
	if code, out, _ := run(append([]string{"-verified"}, first...)...); code != 0 || !strings.Contains(out, "provisioned") {
		t.Fatalf("first family: %d %q", code, out)
	}
	if code, _, _ := run(append([]string{"-verified"}, second...)...); code != 1 {
		t.Fatalf("second family without -add must refuse to overwrite: %d", code)
	}
	if code, _, errOut := run(append([]string{"-verified", "-dry-run"}, second...)...); code != 2 || !strings.Contains(errOut, "-add") {
		t.Fatalf("-dry-run without -add: %d %q", code, errOut)
	}
	// Dry run, real run, repeat: stable words for automation.
	if code, out, _ := run(append([]string{"-verified", "-add", "-dry-run"}, second...)...); code != 0 || strings.TrimSpace(out) != "would change: guardian child" {
		t.Fatalf("dry run: %d %q", code, out)
	}
	if code, out, _ := run(append([]string{"-verified", "-add"}, second...)...); code != 0 || strings.TrimSpace(out) != "changed: guardian child" {
		t.Fatalf("add: %d %q", code, out)
	}
	for _, flags := range [][]string{{"-verified", "-add"}, {"-verified", "-add", "-dry-run"}} {
		if code, out, _ := run(append(flags, second...)...); code != 0 || strings.TrimSpace(out) != "unchanged" {
			t.Fatalf("repeat %v: %d %q", flags, code, out)
		}
	}
	// The address never appears in the output.
	if code, out, errOut := run(append([]string{"-verified", "-add"}, append(append([]string{}, base...), "-tenant", "family-c", "-guardian", "carla", "-email", "anna@example.org", "-child", "cem", "-name", "Cem")...)...); code != 1 || strings.Contains(out+errOut, "example.org") {
		t.Fatalf("conflict: %d %q %q", code, out, errOut)
	}
	if code, _, _ := run("-verified", "-add", "-unknown-flag"); code != 2 {
		t.Fatalf("unknown flag: %d", code)
	}
}
