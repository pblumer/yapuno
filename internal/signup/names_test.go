package signup

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/yapuno/internal/guardiannames"
)

type storedName struct {
	tenant, guardian string
	names            guardiannames.Names
}

type fakeNames struct {
	mu   sync.Mutex
	set  []storedName
	fail bool
}

func (n *fakeNames) Set(tenantID, guardianID string, names guardiannames.Names) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.fail {
		return errors.New("disk full")
	}
	n.set = append(n.set, storedName{tenantID, guardianID, names})
	return nil
}

// namingRig is an installation on which guardians can choose names.
func namingRig(t *testing.T) (*rig, *fakeNames) {
	t.Helper()
	r := newRig(t)
	names := &fakeNames{}
	cfg := r.h.cfg
	cfg.Names = names
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.h, r.routes = h, h.Routes()
	return r, names
}

func named(code, email, child, guardianName string) url.Values {
	form := request(code, email, child)
	form.Set("guardian_name", guardianName)
	return form
}

func TestTheJoinPageOffersAnOptionalNameForOtherParents(t *testing.T) {
	r, names := namingRig(t)
	code, _ := r.invite(t)
	body := r.get("/join?code=" + code).Body.String()
	for _, want := range []string{`name="guardian_name"`, `maxlength="40"`, "freiwillig", "andere Eltern"} {
		if !strings.Contains(body, want) {
			t.Errorf("join page lacks %q", want)
		}
	}
	// The field is optional: it must not block the form.
	field := regexp.MustCompile(`<input[^>]*name="guardian_name"[^>]*>`).FindString(body)
	if field == "" || strings.Contains(field, "required") {
		t.Fatalf("name field: %q", field)
	}
	if rec := r.post("/join/request", named(code, "bruno@example.org", "Leo", "  Bruno Beispiel ")); rec.Code != http.StatusOK {
		t.Fatalf("request: %d", rec.Code)
	}
	if len(names.set) != 0 {
		t.Fatal("a name was stored before the address was confirmed")
	}
	done := r.post("/join/verify", url.Values{"token": {tokenOf(t, r.mailer.last(t).link)}})
	if done.Code != http.StatusSeeOther || len(r.families.created) != 1 {
		t.Fatalf("verify: %d", done.Code)
	}
	f := r.families.created[0]
	if len(names.set) != 1 || names.set[0] != (storedName{f.tenant, f.guardian, guardiannames.Names{ForAdults: "Bruno Beispiel"}}) {
		t.Fatalf("stored names: %+v for family %+v", names.set, f)
	}
}

func TestWithoutANameNothingIsStored(t *testing.T) {
	for name, form := range map[string]func(code string) url.Values{
		"empty field":  func(code string) url.Values { return named(code, "bruno@example.org", "Leo", "") },
		"only spaces":  func(code string) url.Values { return named(code, "bruno@example.org", "Leo", "   ") },
		"field absent": func(code string) url.Values { return request(code, "bruno@example.org", "Leo") },
	} {
		r, names := namingRig(t)
		code, _ := r.invite(t)
		if rec := r.post("/join/request", form(code)); rec.Code != http.StatusOK {
			t.Fatalf("%s: request %d", name, rec.Code)
		}
		if done := r.post("/join/verify", url.Values{"token": {tokenOf(t, r.mailer.last(t).link)}}); done.Code != http.StatusSeeOther || len(r.families.created) != 1 {
			t.Fatalf("%s: verify %d", name, done.Code)
		}
		if len(names.set) != 0 {
			t.Errorf("%s: stored %+v", name, names.set)
		}
	}
}

func TestANameThatCannotBeShownStopsTheRegistrationBeforeAnyMail(t *testing.T) {
	r, names := namingRig(t)
	code, inv := r.invite(t)
	for name, bad := range map[string]string{"markup": "<b>Bruno</b>", "too long": strings.Repeat("x", 41), "line break": "Bruno\nBeispiel"} {
		if rec := r.post("/join/request", named(code, "bruno@example.org", "Leo", bad)); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	twice := named(code, "bruno@example.org", "Leo", "Bruno")
	twice.Add("guardian_name", "Bert")
	if rec := r.post("/join/request", twice); rec.Code != http.StatusBadRequest {
		t.Errorf("two names: %d", rec.Code)
	}
	if r.mailer.count() != 0 || len(names.set) != 0 {
		t.Fatal("a refused request sent mail or stored a name")
	}
	// Nothing was charged to the invitation.
	if got, err := r.store.Usable(inv.ID); err != nil || got.Mails != 0 {
		t.Fatalf("invitation after refusals: %+v %v", got, err)
	}
}

// The name is a courtesy: if it cannot be stored the family still exists.
func TestAFailedNameLeavesTheRegistrationInPlace(t *testing.T) {
	r, names := namingRig(t)
	names.fail = true
	code, _ := r.invite(t)
	r.post("/join/request", named(code, "bruno@example.org", "Leo", "Bruno"))
	done := r.post("/join/verify", url.Values{"token": {tokenOf(t, r.mailer.last(t).link)}})
	if done.Code != http.StatusSeeOther || done.Header().Get("Location") != "/family" {
		t.Fatalf("verify: %d %q", done.Code, done.Header().Get("Location"))
	}
	if len(r.families.created) != 1 || len(r.families.discarded) != 0 || len(r.sessions.issued) != 1 || !r.directory.Has("bruno@example.org") {
		t.Fatal("a failed name damaged the registration")
	}
}

// Where guardians have no names, nothing is asked and nothing is kept.
func TestWithoutANameStoreTheJoinPageAsksForNoName(t *testing.T) {
	r := newRig(t)
	code, _ := r.invite(t)
	if body := r.get("/join?code=" + code).Body.String(); strings.Contains(body, "guardian_name") {
		t.Fatal("a name is asked for although names are off")
	}
	if rec := r.post("/join/request", named(code, "bruno@example.org", "Leo", "Bruno")); rec.Code != http.StatusOK {
		t.Fatalf("request with a name nobody asked for: %d", rec.Code)
	}
	if done := r.post("/join/verify", url.Values{"token": {tokenOf(t, r.mailer.last(t).link)}}); done.Code != http.StatusSeeOther || len(r.families.created) != 1 {
		t.Fatalf("verify: %d", done.Code)
	}
}
