package play

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
)

func settingsWorld(t *testing.T) (*Settings, string, *familystore.Store) {
	t.Helper()
	dir := t.TempDir()
	families, err := familystore.Open(filepath.Join(dir, "families.json"))
	if err != nil {
		t.Fatal(err)
	}
	add := func(tenant, child, name, guardian string, contacts bool) {
		grant := authz.GuardianGrant{GuardianID: guardian, ChildID: child, TenantID: tenant, Active: true, CanManageDevices: true, CanApproveContacts: contacts}
		if err := families.Bootstrap(familydashboard.Child{ID: child, TenantID: tenant, DisplayName: name}, grant); err != nil {
			t.Fatal(err)
		}
	}
	add("family-a", "lea", "Lea", "anna", true)
	add("family-b", "mia", "Mia", "bruno", true)
	add("family-c", "lea", "Noah", "carla", false) // same child id, guardian without contact authority
	path := filepath.Join(dir, "play.json")
	s, err := OpenSettings(path, families)
	if err != nil {
		t.Fatal(err)
	}
	return s, path, families
}

func TestSettingsOffByDefaultAndScopedToTheGuardiansChild(t *testing.T) {
	s, path, families := settingsWorld(t)
	if s.Enabled("family-a", "lea", GameChess) {
		t.Fatal("chess must be off by default")
	}
	for _, tc := range []struct {
		name                    string
		guardian, tenant, child string
		game                    string
		want                    error
	}{
		{"guardian of another family", "bruno", "family-a", "lea", GameChess, ErrForbidden},
		{"guardian names own tenant but other child", "anna", "family-a", "mia", GameChess, ErrForbidden},
		{"same child id in another family", "anna", "family-c", "lea", GameChess, ErrForbidden},
		{"guardian without contact authority", "carla", "family-c", "lea", GameChess, ErrForbidden},
		{"no guardian", "", "family-a", "lea", GameChess, ErrForbidden},
		{"unknown game", "anna", "family-a", "lea", "poker", ErrUnknownGame},
	} {
		if err := s.SetEnabled(tc.guardian, tc.tenant, tc.child, tc.game, true); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	if s.Enabled("family-a", "lea", GameChess) || s.Enabled("family-c", "lea", GameChess) {
		t.Fatal("a refused change enabled chess")
	}
	if err := s.SetEnabled("anna", "family-a", "lea", GameChess, true); err != nil {
		t.Fatal(err)
	}
	if !s.Enabled("family-a", "lea", GameChess) || s.Enabled("family-c", "lea", GameChess) || s.Enabled("family-b", "mia", GameChess) {
		t.Fatal("enabled the wrong child")
	}
	if s.Enabled("family-a", "lea", "poker") {
		t.Fatal("unknown game enabled")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("settings file mode: %v %v", info, err)
	}
	// Survives a restart.
	again, err := OpenSettings(path, families)
	if err != nil || !again.Enabled("family-a", "lea", GameChess) {
		t.Fatalf("after reopen: %v", err)
	}
	// A guardian whose authority ends can no longer change it.
	if err := s.SetEnabled("anna", "family-a", "lea", GameChess, false); err != nil {
		t.Fatal(err)
	}
	if s.Enabled("family-a", "lea", GameChess) {
		t.Fatal("switching off failed")
	}
	// Setting the same value twice is not an error.
	if err := s.SetEnabled("anna", "family-a", "lea", GameChess, false); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsPurge(t *testing.T) {
	s, path, families := settingsWorld(t)
	for _, c := range []struct{ g, tnt, ch string }{{"anna", "family-a", "lea"}, {"bruno", "family-b", "mia"}} {
		if err := s.SetEnabled(c.g, c.tnt, c.ch, GameChess, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PurgeChild("family-a", "lea"); err != nil {
		t.Fatal(err)
	}
	if s.Enabled("family-a", "lea", GameChess) || !s.Enabled("family-b", "mia", GameChess) {
		t.Fatal("purge child")
	}
	if err := s.PurgeTenant("family-b"); err != nil {
		t.Fatal(err)
	}
	if s.Enabled("family-b", "mia", GameChess) {
		t.Fatal("purge tenant")
	}
	again, err := OpenSettings(path, families)
	if err != nil || again.Enabled("family-b", "mia", GameChess) {
		t.Fatalf("purge not durable: %v", err)
	}
	if err := s.PurgeChild("", "x"); err == nil {
		t.Fatal("empty tenant accepted")
	}
}

func TestOpenSettingsFailsClosed(t *testing.T) {
	_, _, families := settingsWorld(t)
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for name, path := range map[string]string{
		"world readable": write("a.json", `{"enabled":[]}`, 0644),
		"unknown field":  write("b.json", `{"enabled":[],"x":1}`, 0600),
		"unknown game":   write("c.json", `{"enabled":[{"tenant_id":"t","child_id":"c","game":"poker"}]}`, 0600),
		"empty child":    write("d.json", `{"enabled":[{"tenant_id":"t","child_id":"","game":"chess"}]}`, 0600),
		"duplicate":      write("e.json", `{"enabled":[{"tenant_id":"t","child_id":"c","game":"chess"},{"tenant_id":"t","child_id":"c","game":"chess"}]}`, 0600),
		"corrupt":        write("f.json", `{"enabled":`, 0600),
	} {
		if _, err := OpenSettings(path, families); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	if _, err := OpenSettings(filepath.Join(dir, "new.json"), nil); err == nil {
		t.Error("nil families accepted")
	}
}
