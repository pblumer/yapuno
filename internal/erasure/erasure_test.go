package erasure

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/yapuno/internal/familystore"
)

// journal records the order in which the parts are asked to act.
type journal struct {
	mu    sync.Mutex
	steps []string
	fail  map[string]bool
}

func (j *journal) note(step string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.steps = append(j.steps, step)
	if j.fail[step] {
		return errors.New("disk full")
	}
	return nil
}

func (j *journal) list() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return strings.Join(j.steps, " > ")
}

type fakeFamilies struct {
	j         *journal
	refuse    error
	guardians []string
}

func (f fakeFamilies) MayRemoveChild(g, t, c string) error {
	_ = f.j.note("may-child " + g + "/" + t + "/" + c)
	return f.refuse
}

func (f fakeFamilies) RemoveChild(g, t, c string) error {
	if err := f.j.note("remove-child " + g + "/" + t + "/" + c); err != nil {
		return err
	}
	return f.refuse
}

func (f fakeFamilies) MayRemoveFamily(g, t string) error {
	_ = f.j.note("may-family " + g + "/" + t)
	return f.refuse
}

func (f fakeFamilies) RemoveFamily(g, t string) ([]string, error) {
	if err := f.j.note("remove-family " + g + "/" + t); err != nil {
		return nil, err
	}
	if f.refuse != nil {
		return nil, f.refuse
	}
	return f.guardians, nil
}

type fakeStore struct {
	j    *journal
	name string
}

func (s fakeStore) PurgeChild(t, c string) error { return s.j.note(s.name + "-child " + t + "/" + c) }
func (s fakeStore) PurgeTenant(t string) error   { return s.j.note(s.name + "-tenant " + t) }
func (s fakeStore) Compact() error               { return s.j.note(s.name + "-compact") }
func (s fakeStore) RemoveTenant(t string) (int, error) {
	return 2, s.j.note(s.name + "-remove " + t)
}

func service(t *testing.T, j *journal, refuse error) *Service {
	t.Helper()
	s, err := New(Config{
		Families:   fakeFamilies{j: j, refuse: refuse, guardians: []string{"albert", "anna"}},
		Identities: fakeStore{j, "identities"},
		SignOut: []func(guardianID, tenantID string){
			func(g, tn string) { _ = j.note("sessions " + g + "/" + tn) },
			func(g, tn string) { _ = j.note("links " + g + "/" + tn) },
		},
		Before: []Dependent{{"devices", fakeStore{j, "devices"}}, {"contacts", fakeStore{j, "contacts"}}, {"looks", fakeStore{j, "looks"}}},
		After:  []TenantDependent{{"invitations", fakeStore{j, "invitations"}}, {"locks", fakeStore{j, "locks"}}},
		Logs:   []Log{{"messages", fakeStore{j, "messages"}}, {"guardian chat", fakeStore{j, "chat"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// What the child can act with goes first, the profile after it, and the
// logs are compacted once the profile no longer exists.
func TestDeleteChildRemovesDependentsThenTheProfileThenCompacts(t *testing.T) {
	j := &journal{}
	if err := service(t, j, nil).DeleteChild("anna", "family-a", "lea"); err != nil {
		t.Fatal(err)
	}
	want := "may-child anna/family-a/lea > devices-child family-a/lea > contacts-child family-a/lea > looks-child family-a/lea > " +
		"remove-child anna/family-a/lea > messages-compact > chat-compact"
	if got := j.list(); got != want {
		t.Fatalf("steps:\n got %s\nwant %s", got, want)
	}
}

func TestDeleteFamilyRemovesDependentsThenTheFamilyThenIdentitiesSessionsAndRecords(t *testing.T) {
	j := &journal{}
	if err := service(t, j, nil).DeleteFamily("anna", "family-a"); err != nil {
		t.Fatal(err)
	}
	want := "may-family anna/family-a > devices-tenant family-a > contacts-tenant family-a > looks-tenant family-a > " +
		"remove-family anna/family-a > identities-remove family-a > " +
		"sessions albert/family-a > links albert/family-a > sessions anna/family-a > links anna/family-a > " +
		"invitations-tenant family-a > locks-tenant family-a > messages-compact > chat-compact"
	if got := j.list(); got != want {
		t.Fatalf("steps:\n got %s\nwant %s", got, want)
	}
}

// Whoever may not delete causes nothing to be deleted, not even a dependent.
func TestARefusedDeletionTouchesNothing(t *testing.T) {
	for _, refusal := range []error{familystore.ErrForbidden, familystore.ErrLastChild, familystore.ErrLastFamily} {
		j := &journal{}
		s := service(t, j, refusal)
		if err := s.DeleteChild("mallory", "family-a", "lea"); !errors.Is(err, refusal) {
			t.Errorf("child: %v", err)
		}
		if err := s.DeleteFamily("mallory", "family-a"); !errors.Is(err, refusal) {
			t.Errorf("family: %v", err)
		}
		if got := j.list(); got != "may-child mallory/family-a/lea > may-family mallory/family-a" {
			t.Errorf("%v: steps %s", refusal, got)
		}
	}
	j := &journal{}
	s := service(t, j, nil)
	for name, err := range map[string]error{
		"child without guardian":  s.DeleteChild("", "family-a", "lea"),
		"child without tenant":    s.DeleteChild("anna", "", "lea"),
		"child without id":        s.DeleteChild("anna", "family-a", ""),
		"family without guardian": s.DeleteFamily("", "family-a"),
		"family without tenant":   s.DeleteFamily("anna", ""),
	} {
		if !errors.Is(err, familystore.ErrForbidden) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := j.list(); got != "" {
		t.Fatalf("an incomplete request reached a store: %s", got)
	}
}

// If something that depends on the profile cannot be removed, the profile
// stays, so that the guardian can try again.
func TestAFailedDependentKeepsTheProfile(t *testing.T) {
	j := &journal{fail: map[string]bool{"contacts-child family-a/lea": true, "contacts-tenant family-a": true}}
	s := service(t, j, nil)
	if err := s.DeleteChild("anna", "family-a", "lea"); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("child: %v", err)
	}
	if err := s.DeleteFamily("anna", "family-a"); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("family: %v", err)
	}
	got := j.list()
	for _, never := range []string{"remove-child", "remove-family", "identities", "sessions", "locks", "looks", "compact"} {
		if strings.Contains(got, never) {
			t.Errorf("after a failed dependent, %q still ran: %s", never, got)
		}
	}
	// The same holds if the profile itself cannot be removed.
	j = &journal{fail: map[string]bool{"remove-child anna/family-a/lea": true, "remove-family anna/family-a": true}}
	s = service(t, j, nil)
	if err := s.DeleteChild("anna", "family-a", "lea"); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("child, store failure: %v", err)
	}
	if err := s.DeleteFamily("anna", "family-a"); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("family, store failure: %v", err)
	}
	if got := j.list(); strings.Contains(got, "identities") || strings.Contains(got, "compact") || strings.Contains(got, "sessions") {
		t.Fatalf("steps after a failed removal: %s", got)
	}
}

// Once the family is gone the guardians are signed out whatever else fails,
// and the caller learns that something was left behind.
func TestFailuresAfterTheFamilyIsGoneAreReportedButDoNotStopTheRest(t *testing.T) {
	j := &journal{fail: map[string]bool{"invitations-tenant family-a": true, "messages-compact": true}}
	if err := service(t, j, nil).DeleteFamily("anna", "family-a"); !errors.Is(err, ErrLeftovers) {
		t.Fatalf("family: %v", err)
	}
	got := j.list()
	for _, must := range []string{"sessions anna/family-a", "links albert/family-a", "locks-tenant family-a", "chat-compact"} {
		if !strings.Contains(got, must) {
			t.Errorf("%q did not run: %s", must, got)
		}
	}
	j = &journal{fail: map[string]bool{"chat-compact": true}}
	if err := service(t, j, nil).DeleteChild("anna", "family-a", "lea"); !errors.Is(err, ErrLeftovers) {
		t.Fatalf("child: %v", err)
	}
}

// A lock protects against a guardian. If that guardian's identity could not
// be removed, the lock stays.
func TestLocksAndInvitationsStayWhenIdentitiesCouldNotBeRemoved(t *testing.T) {
	j := &journal{fail: map[string]bool{"identities-remove family-a": true}}
	if err := service(t, j, nil).DeleteFamily("anna", "family-a"); !errors.Is(err, ErrLeftovers) {
		t.Fatalf("family: %v", err)
	}
	got := j.list()
	if strings.Contains(got, "locks-tenant") || strings.Contains(got, "invitations-tenant") {
		t.Fatalf("records were purged although the identities are left: %s", got)
	}
	// The guardians are signed out all the same.
	if !strings.Contains(got, "sessions anna/family-a") || !strings.Contains(got, "sessions albert/family-a") || !strings.Contains(got, "messages-compact") {
		t.Fatalf("steps: %s", got)
	}
}

func TestDeletionsDoNotInterleave(t *testing.T) {
	j := &journal{}
	s := service(t, j, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.DeleteFamily("anna", "family-a") }()
	}
	wg.Wait()
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.steps)%14 != 0 {
		t.Fatalf("%d steps", len(j.steps))
	}
	for i := 0; i < len(j.steps); i += 14 {
		if j.steps[i] != "may-family anna/family-a" || j.steps[i+13] != "chat-compact" {
			t.Fatalf("deletions interleaved at %d: %v", i, j.steps[i:i+14])
		}
	}
}

func TestNewNeedsTheFamilyStoreAndTheIdentities(t *testing.T) {
	j := &journal{}
	if _, err := New(Config{Identities: fakeStore{j, "identities"}}); err == nil {
		t.Error("no family store accepted")
	}
	if _, err := New(Config{Families: fakeFamilies{j: j}}); err == nil {
		t.Error("no identities accepted")
	}
	if _, err := New(Config{Families: fakeFamilies{j: j}, Identities: fakeStore{j, "identities"}, Before: []Dependent{{"x", nil}}}); err == nil {
		t.Error("nil dependent accepted")
	}
	if _, err := New(Config{Families: fakeFamilies{j: j}, Identities: fakeStore{j, "identities"}, After: []TenantDependent{{"x", nil}}}); err == nil {
		t.Error("nil tenant dependent accepted")
	}
	if _, err := New(Config{Families: fakeFamilies{j: j}, Identities: fakeStore{j, "identities"}, Logs: []Log{{"x", nil}}}); err == nil {
		t.Error("nil log accepted")
	}
	if _, err := New(Config{Families: fakeFamilies{j: j}, Identities: fakeStore{j, "identities"}, SignOut: []func(string, string){nil}}); err == nil {
		t.Error("nil sign-out accepted")
	}
	// The optional parts really are optional.
	s, err := New(Config{Families: fakeFamilies{j: j, guardians: []string{"anna"}}, Identities: fakeStore{j, "identities"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFamily("anna", "family-a"); err != nil {
		t.Fatal(err)
	}
}

// Each kind of leftover is reported on its own.
func TestEveryFailureAfterTheFamilyIsGoneIsReported(t *testing.T) {
	for _, step := range []string{"identities-remove family-a", "invitations-tenant family-a", "locks-tenant family-a", "messages-compact", "chat-compact"} {
		j := &journal{fail: map[string]bool{step: true}}
		if err := service(t, j, nil).DeleteFamily("anna", "family-a"); !errors.Is(err, ErrLeftovers) {
			t.Errorf("%s failed: %v", step, err)
		}
		// Whatever failed, the family was removed and the guardians signed out.
		if got := j.list(); !strings.Contains(got, "remove-family") || !strings.Contains(got, "sessions anna/family-a") {
			t.Errorf("%s failed: steps %s", step, got)
		}
	}
	for _, step := range []string{"messages-compact", "chat-compact"} {
		j := &journal{fail: map[string]bool{step: true}}
		if err := service(t, j, nil).DeleteChild("anna", "family-a", "lea"); !errors.Is(err, ErrLeftovers) {
			t.Errorf("child, %s failed: %v", step, err)
		}
	}
}
