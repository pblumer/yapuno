// Command yapuno-provision creates a FIRST synthetic family in private files
// and, with -add, adds further families, children and guardians to them.
// It is an offline administrator tool, not a self-registration endpoint.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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

// change says what an addition wrote, or would write in a dry run.
type change struct {
	guardian bool // a new entry in the guardian directory
	child    bool // a new child profile with this guardian's grant
	grant    bool // a grant for an existing child (an additional, equal guardian)
}

func (c change) words() string {
	var parts []string
	if c.guardian {
		parts = append(parts, "guardian")
	}
	if c.child {
		parts = append(parts, "child")
	}
	if c.grant {
		parts = append(parts, "grant")
	}
	return strings.Join(parts, " ")
}

// privateFile requires an existing regular file readable only by its owner,
// in a directory only its owner can enter.
func privateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("%s must be an existing private regular file (mode 0600)", filepath.Base(path))
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil || !dir.IsDir() || dir.Mode().Perm()&0077 != 0 {
		return errors.New("the data directory must have mode 0700")
	}
	return nil
}

func readDirectory(path string) ([]guardiandirectory.Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc struct {
		Guardians []guardiandirectory.Entry `json:"guardians"`
	}
	if err := dec.Decode(&doc); err != nil {
		return nil, errors.New("guardian directory is not readable")
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, errors.New("guardian directory is not readable")
	}
	return doc.Guardians, nil
}

func writeDirectory(path string, entries []guardiandirectory.Entry) error {
	data, err := json.Marshal(struct {
		Guardians []guardiandirectory.Entry `json:"guardians"`
	}{entries})
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".yapuno-guardians-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	if handle, err := os.Open(dir); err == nil {
		defer handle.Close()
		return handle.Sync()
	}
	return nil
}

// extend adds to an existing installation: a new family, a further child of
// an existing guardian, or a further, equal guardian for an existing child.
// It never changes or removes what is there, and repeating it changes
// nothing. The server must not be running: it keeps the family store in
// memory and would neither see the addition nor preserve it.
func extend(storePath, identityPath string, child familydashboard.Child, guardianID, email string, dryRun bool) (change, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || parsed.Name != "" || len(email) > 254 || strings.ContainsAny(email, "\r\n\x00") ||
		!validID(child.ID) || !validID(child.TenantID) || !validID(guardianID) || storePath == "" || identityPath == "" || storePath == identityPath {
		return change{}, errors.New("invalid provisioning fields")
	}
	for _, path := range []string{storePath, identityPath} {
		if err := privateFile(path); err != nil {
			return change{}, err
		}
	}
	entries, err := readDirectory(identityPath)
	if err != nil {
		return change{}, err
	}
	var result change
	known := false
	for _, e := range entries {
		switch {
		case e.TenantID == child.TenantID && e.GuardianID == guardianID:
			if e.Email != email || !e.Active {
				return change{}, errors.New("this guardian exists with another address or is inactive; change that by hand")
			}
			known = true
		case e.Email == email:
			return change{}, errors.New("this address already belongs to another guardian")
		}
	}
	result.guardian = !known
	store, err := familystore.Open(storePath)
	if err != nil {
		return change{}, errors.New("family store is not readable")
	}
	_, exists := store.ChildName(child.TenantID, child.ID)
	granted := false
	for _, card := range store.Cards(guardianID, child.TenantID) {
		if card.ID == child.ID {
			granted = true
		}
	}
	result.child = !exists
	result.grant = exists && !granted
	if !exists {
		// Validate the name before anything is written.
		probe := familystore.State{Children: []familydashboard.Child{child}}
		if err := familystore.Validate(probe); err != nil {
			return change{}, errors.New("invalid child name")
		}
	}
	if dryRun || result == (change{}) {
		return result, nil
	}
	// Directory first: an incomplete attempt cannot sign in until the grant
	// is also committed, because a guardian without a child is not admitted.
	if result.guardian {
		entries = append(entries, guardiandirectory.Entry{Email: email, GuardianID: guardianID, TenantID: child.TenantID, Active: true})
		if err := writeDirectory(identityPath, entries); err != nil {
			return change{}, err
		}
	}
	grant := authz.GuardianGrant{GuardianID: guardianID, ChildID: child.ID, TenantID: child.TenantID, Active: true, CanManageDevices: true, CanApproveContacts: true}
	switch {
	case result.child:
		err = store.Bootstrap(child, grant)
	case result.grant:
		err = store.BootstrapGuardian(grant)
	}
	if err != nil {
		return change{}, err
	}
	return result, nil
}

func main() { os.Exit(runProvision(os.Args[1:], os.Stdout, os.Stderr)) }

// runProvision is main without the process: it returns the exit code.
func runProvision(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("yapuno-provision", flag.ContinueOnError)
	flags.SetOutput(stderr)
	storePath := flags.String("families", "", "family store path")
	identityPath := flags.String("guardians", "", "guardian allowlist path")
	guardian := flags.String("guardian", "", "pre-verified guardian ID")
	email := flags.String("email", "", "pre-verified guardian email")
	tenant := flags.String("tenant", "", "tenant ID")
	childID := flags.String("child", "", "synthetic child ID")
	childName := flags.String("name", "", "synthetic child display name")
	verified := flags.Bool("verified", false, "operator confirms independent identity check and synthetic-only test")
	add := flags.Bool("add", false, "add to existing files instead of creating them; run only while the server is stopped")
	dryRun := flags.Bool("dry-run", false, "with -add: report what would change without writing")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if !*verified {
		fmt.Fprintln(stderr, "manual verification required: use -verified only for explicitly checked, synthetic preview data")
		return 2
	}
	if *dryRun && !*add {
		fmt.Fprintln(stderr, "-dry-run is only available together with -add")
		return 2
	}
	child := familydashboard.Child{ID: *childID, TenantID: *tenant, DisplayName: *childName}
	if *add {
		// Messages never repeat the address: it is personal data.
		result, err := extend(*storePath, *identityPath, child, *guardian, *email, *dryRun)
		switch {
		case err != nil:
			fmt.Fprintln(stderr, "provisioning failed:", err)
			return 1
		case result == (change{}):
			fmt.Fprintln(stdout, "unchanged")
		case *dryRun:
			fmt.Fprintln(stdout, "would change:", result.words())
		default:
			fmt.Fprintln(stdout, "changed:", result.words())
		}
		return 0
	}
	if err := provision(*storePath, *identityPath, child, *guardian, *email); err != nil {
		fmt.Fprintln(stderr, "provisioning failed:", err)
		return 1
	}
	fmt.Fprintln(stdout, "Synthetic family provisioned. The login email remains restricted to this guardian and tenant.")
	return 0
}
