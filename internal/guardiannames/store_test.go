package guardiannames

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "guardian-names.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestAGuardianHasNoNameUntilOneIsSet(t *testing.T) {
	s, path := open(t)
	if got := s.Get("family-a", "anna"); got != (Names{}) {
		t.Fatalf("names before any were set: %+v", got)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("file: %v %v", info, err)
	}
}

func TestNamesAreStoredPerGuardianAndFamily(t *testing.T) {
	s, path := open(t)
	if err := s.Set("family-a", "anna", Names{ForChildren: "  Mami ", ForAdults: " Anna Muster  "}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("family-b", "bruno", Names{ForAdults: "Bruno"}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, store := range map[string]*Store{"running": s, "reopened": reopened} {
		if got := store.Get("family-a", "anna"); got != (Names{ForChildren: "Mami", ForAdults: "Anna Muster"}) {
			t.Errorf("%s: %+v", name, got)
		}
		if got := store.Get("family-b", "bruno"); got != (Names{ForAdults: "Bruno"}) {
			t.Errorf("%s: %+v", name, got)
		}
		// The same guardian id in another family, and another guardian of
		// the same family, are other people.
		for _, who := range [][2]string{{"family-b", "anna"}, {"family-a", "bruno"}, {"family-a", ""}, {"", "anna"}} {
			if got := store.Get(who[0], who[1]); got != (Names{}) {
				t.Errorf("%s: names leaked to %v: %+v", name, who, got)
			}
		}
	}
	info, _ := os.Lstat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("file mode %v", info.Mode())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".yapuno-*")); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestEachNameIsChangedAndRemovedOnItsOwn(t *testing.T) {
	s, path := open(t)
	if err := s.Set("family-a", "anna", Names{ForChildren: "Mami", ForAdults: "Anna"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("family-a", "anna", Names{ForChildren: "Mama", ForAdults: ""}); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("family-a", "anna"); got != (Names{ForChildren: "Mama"}) {
		t.Fatalf("after change: %+v", got)
	}
	// Removing both names removes the record, not just its content.
	if err := s.Set("family-a", "anna", Names{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "anna") || strings.Contains(string(raw), "Mama") {
		t.Fatalf("an empty record is left: %s", raw)
	}
}

func TestNamesThatCannotBeShownAreRefused(t *testing.T) {
	s, path := open(t)
	if err := s.Set("family-a", "anna", Names{ForChildren: "Mami"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	long := strings.Repeat("x", 41)
	for name, bad := range map[string]Names{
		"markup for children": {ForChildren: "<b>Mami</b>"},
		"markup for adults":   {ForAdults: "Anna<script>"},
		"line break":          {ForAdults: "Anna\nMuster"},
		"tab":                 {ForChildren: "Ma\tmi"},
		"escape":              {ForChildren: "Ma\x1bmi"},
		"nul":                 {ForAdults: "An\x00na"},
		"too long":            {ForAdults: long},
		"too long for child":  {ForChildren: long},
		"invalid utf-8":       {ForAdults: "An\xffna"},
		"direction override":  {ForAdults: "Anna‮retsuM"},
	} {
		if err := s.Set("family-a", "anna", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, who := range map[string][2]string{"no family": {"", "anna"}, "no guardian": {"family-a", ""}, "odd family": {"a\nb", "anna"}} {
		if err := s.Set(who[0], who[1], Names{ForAdults: "Anna"}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || s.Get("family-a", "anna") != (Names{ForChildren: "Mami"}) {
		t.Fatal("a refused name changed the store")
	}
	// Forty characters, letters of other scripts and emoji are fine.
	for _, good := range []string{strings.Repeat("x", 40), "Zoë", "Søren Åse", "אמא", "Mami 🌻", "O'Neill-Müller"} {
		if err := s.Set("family-a", "anna", Names{ForAdults: good}); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
}

func TestPurgeTenantRemovesTheNamesOfThatFamilyOnly(t *testing.T) {
	s, path := open(t)
	for _, who := range [][2]string{{"family-a", "anna"}, {"family-a", "albert"}, {"family-b", "bruno"}, {"family-z", "anna"}} {
		if err := s.Set(who[0], who[1], Names{ForAdults: "Name von " + who[1]}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PurgeTenant("family-a"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, store := range map[string]*Store{"running": s, "reopened": reopened} {
		if store.Get("family-a", "anna") != (Names{}) || store.Get("family-a", "albert") != (Names{}) {
			t.Errorf("%s: a name of the purged family is left", name)
		}
		if store.Get("family-b", "bruno").ForAdults == "" || store.Get("family-z", "anna").ForAdults == "" {
			t.Errorf("%s: a name of another family was removed", name)
		}
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "family-a") {
		t.Fatalf("file: %s", raw)
	}
	if err := s.PurgeTenant("family-a"); err != nil {
		t.Fatalf("second purge: %v", err)
	}
	if err := s.PurgeTenant(""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no family: %v", err)
	}
}

func TestAFailedWriteChangesNothing(t *testing.T) {
	s, _ := open(t)
	if err := s.Set("family-a", "anna", Names{ForAdults: "Anna"}); err != nil {
		t.Fatal(err)
	}
	s.failWrite = true
	if err := s.Set("family-a", "anna", Names{ForAdults: "Anders"}); err == nil {
		t.Fatal("a name was acknowledged without a durable write")
	}
	if err := s.PurgeTenant("family-a"); err == nil {
		t.Fatal("a purge was acknowledged without a durable write")
	}
	s.failWrite = false
	if got := s.Get("family-a", "anna"); got != (Names{ForAdults: "Anna"}) {
		t.Fatalf("after failed writes: %+v", got)
	}
}

func TestOpenFailsClosed(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("empty path accepted")
	}
	entry := func(tenant, guardian, children, adults string) string {
		return `{"tenant_id":"` + tenant + `","guardian_id":"` + guardian + `","for_children":"` + children + `","for_adults":"` + adults + `"}`
	}
	for name, content := range map[string]string{
		"corrupt":       `{"guardians":[`,
		"unknown field": `{"guardians":[],"extra":1}`,
		"trailing data": `{"guardians":[]} {}`,
		"no family":     `{"guardians":[` + entry("", "anna", "Mami", "") + `]}`,
		"no guardian":   `{"guardians":[` + entry("family-a", "", "Mami", "") + `]}`,
		"no name":       `{"guardians":[` + entry("family-a", "anna", "", "") + `]}`,
		"markup":        `{"guardians":[` + entry("family-a", "anna", "", "<b>") + `]}`,
		"duplicate":     `{"guardians":[` + entry("family-a", "anna", "Mami", "") + `,` + entry("family-a", "anna", "", "Anna") + `]}`,
	} {
		path := filepath.Join(t.TempDir(), "names.json")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(path); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	open := filepath.Join(t.TempDir(), "names.json")
	_ = os.WriteFile(open, []byte(`{"guardians":[]}`), 0644)
	if _, err := Open(open); err == nil {
		t.Fatal("world-readable file opened")
	}
	real := filepath.Join(t.TempDir(), "real.json")
	_ = os.WriteFile(real, []byte(`{"guardians":[]}`), 0600)
	link := filepath.Join(t.TempDir(), "link.json")
	_ = os.Symlink(real, link)
	if _, err := Open(link); err == nil {
		t.Fatal("symlink opened")
	}
	if _, err := Open(filepath.Join(t.TempDir(), "missing", "names.json")); err == nil {
		t.Fatal("file in a missing directory accepted")
	}
}

func TestConcurrentChangesKeepEveryName(t *testing.T) {
	s, path := open(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = s.Set("family-a", "guardian-"+string(rune('a'+i)), Names{ForAdults: "Name " + string(rune('A'+i))})
			_ = s.Get("family-a", "guardian-a")
		}(i)
	}
	wg.Wait()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		if reopened.Get("family-a", "guardian-"+string(rune('a'+i))).ForAdults != "Name "+string(rune('A'+i)) {
			t.Fatalf("name %d was lost", i)
		}
	}
}
