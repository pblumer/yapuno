package appearance

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestPurgeRemovesTheLookOfExactlyThatChildOrFamily(t *testing.T) {
	s, path := openStore(t)
	night := Preference{Skin: "night", Figure: Figure{Shape: "ghost", Color: "peach"}}
	child := Subject{Kind: KindChild, TenantID: "family-a", ID: "lea"}
	others := []Subject{
		{Kind: KindChild, TenantID: "family-a", ID: "tim"},
		// A guardian may carry the same id as a child; they are different subjects.
		{Kind: KindGuardian, TenantID: "family-a", ID: "lea"},
		{Kind: KindGuardian, TenantID: "family-a", ID: "anna"},
		{Kind: KindChild, TenantID: "family-b", ID: "lea"},
		{Kind: KindGuardian, TenantID: "family-b", ID: "bruno"},
	}
	for _, subject := range append([]Subject{child}, others...) {
		if err := s.Set(subject, night); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PurgeChild("family-a", "lea"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, store := range map[string]*Store{"running": s, "reopened": reopened} {
		if got := store.Get(child); got != Default() {
			t.Errorf("%s: look of the purged child: %+v", name, got)
		}
		for _, other := range others {
			if got := store.Get(other); got != night {
				t.Errorf("%s: look of %+v was removed", name, other)
			}
		}
	}
	if err := s.PurgeChild("family-a", "lea"); err != nil {
		t.Fatalf("second purge: %v", err)
	}
	if err := s.PurgeTenant("family-a"); err != nil {
		t.Fatal(err)
	}
	reopened, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, subject := range others {
		want := Default()
		if subject.TenantID == "family-b" {
			want = night
		}
		if got := reopened.Get(subject); got != want {
			t.Errorf("after purging the family, %+v: %+v", subject, got)
		}
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "family-a") {
		t.Fatalf("the file still names the purged family: %s", raw)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("file mode %v", info.Mode())
	}
}

func TestPurgeNeedsACompleteTarget(t *testing.T) {
	s, _ := openStore(t)
	subject := Subject{Kind: KindChild, TenantID: "family-a", ID: "lea"}
	night := Preference{Skin: "night", Figure: Figure{Shape: "ghost", Color: "peach"}}
	if err := s.Set(subject, night); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"no tenant":     s.PurgeChild("", "lea"),
		"no child":      s.PurgeChild("family-a", ""),
		"tenant, no id": s.PurgeTenant(""),
	} {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if s.Get(subject) != night {
		t.Fatal("an incomplete purge removed a look")
	}
}
