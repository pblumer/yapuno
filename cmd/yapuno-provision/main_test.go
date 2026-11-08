package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
)

func TestOfflineProvisioningCreatesPrivateBoundFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(root, "family.json")
	directoryPath := filepath.Join(root, "guardians.json")
	child := familydashboard.Child{ID: "child-a", TenantID: "tenant-a", DisplayName: "Alex"}
	if err := provision(storePath, directoryPath, child, "guardian-a", "parent@example.org"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{storePath, directoryPath} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("insecure file %s: %v", path, err)
		}
	}
	s, err := familystore.Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	d, err := guardiandirectory.New(directoryPath, s)
	if err != nil {
		t.Fatal(err)
	}
	p, err := d.LookupGuardian(context.Background(), "parent@example.org")
	if err != nil || !p.Active || p.GuardianID != "guardian-a" {
		t.Fatalf("not provisioned: %+v %v", p, err)
	}
	if err := provision(storePath, directoryPath, child, "guardian-a", "parent@example.org"); err == nil {
		t.Fatal("overwrite allowed")
	}
}
func TestRejectInvalidOrNonPrivateProvision(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	child := familydashboard.Child{ID: "child-a", TenantID: "tenant-a", DisplayName: "Alex"}
	if err := provision(filepath.Join(root, "family.json"), filepath.Join(root, "guardians.json"), child, "guardian-a", "parent@example.org"); err == nil {
		t.Fatal("world-readable parent directory accepted")
	}
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"bad", "Attacker <attacker@example.org>", "a@example.org\nBcc:evil@example.org"} {
		if err := provision(filepath.Join(private, "family.json"), filepath.Join(private, "guardians.json"), child, "guardian-a", email); err == nil {
			t.Fatalf("bad email accepted: %q", email)
		}
	}
}
