// Command yapuno-provision creates a FIRST synthetic family in private files.
// It is an offline administrator tool, not a self-registration endpoint.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"

	"github.com/pblumer/yapuno/internal/authz"
	"github.com/pblumer/yapuno/internal/familydashboard"
	"github.com/pblumer/yapuno/internal/familystore"
	"github.com/pblumer/yapuno/internal/guardiandirectory"
)

func validID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func provision(storePath, identityPath string, child familydashboard.Child, guardianID, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || parsed.Name != "" || len(email) > 254 || !validID(child.ID) || !validID(child.TenantID) || !validID(guardianID) || child.DisplayName == "" || storePath == "" || identityPath == "" || storePath == identityPath {
		return errors.New("invalid provisioning fields")
	}
	for _, path := range []string{storePath, identityPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("refusing to overwrite existing family or guardian file")
		}
		dir := filepath.Dir(path)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("directory %s must exist and have mode 0700", dir)
		}
	}
	// Create the directory allowlist first. An incomplete attempt cannot
	// log in until the child/grant is also durably committed to the store.
	file, err := os.OpenFile(identityPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	entry := struct {
		Guardians []guardiandirectory.Entry `json:"guardians"`
	}{
		Guardians: []guardiandirectory.Entry{{Email: email, GuardianID: guardianID, TenantID: child.TenantID, Active: true}},
	}
	if err := json.NewEncoder(file).Encode(entry); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	store, err := familystore.Open(storePath)
	if err != nil {
		return err
	}
	grant := authz.GuardianGrant{GuardianID: guardianID, ChildID: child.ID, TenantID: child.TenantID, Active: true, CanManageDevices: true, CanApproveContacts: true}
	return store.Bootstrap(child, grant)
}

func main() {
	storePath := flag.String("families", "", "new family store path")
	identityPath := flag.String("guardians", "", "new guardian allowlist path")
	guardian := flag.String("guardian", "", "pre-verified guardian ID")
	email := flag.String("email", "", "pre-verified guardian email")
	tenant := flag.String("tenant", "", "tenant ID")
	childID := flag.String("child", "", "synthetic child ID")
	childName := flag.String("name", "", "synthetic child display name")
	verified := flag.Bool("verified", false, "operator confirms independent identity check and synthetic-only test")
	flag.Parse()
	if !*verified {
		fmt.Fprintln(os.Stderr, "manual verification required: use -verified only for explicitly checked, synthetic preview data")
		os.Exit(2)
	}
	child := familydashboard.Child{ID: *childID, TenantID: *tenant, DisplayName: *childName}
	if err := provision(*storePath, *identityPath, child, *guardian, *email); err != nil {
		fmt.Fprintln(os.Stderr, "provisioning failed:", err)
		os.Exit(1)
	}
	fmt.Println("Synthetic family provisioned. The login email remains restricted to this guardian and tenant.")
}
