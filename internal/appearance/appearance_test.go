package appearance

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/skin"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Validate(Default()); err != nil {
		t.Fatalf("default preference invalid: %v", err)
	}
}

func TestValidateRejectsUnknownValues(t *testing.T) {
	cases := map[string]Preference{
		"unknown skin":     {Skin: "neon", Figure: Figure{Shape: "ball", Color: "mint"}},
		"injected skin":    {Skin: `classic" onload="x`, Figure: Figure{Shape: "ball", Color: "mint"}},
		"unknown shape":    {Skin: "classic", Figure: Figure{Shape: "photo", Color: "mint"}},
		"path as shape":    {Skin: "classic", Figure: Figure{Shape: "M0 0L10 10", Color: "mint"}},
		"hex as color":     {Skin: "classic", Figure: Figure{Shape: "ball", Color: "#ff0000"}},
		"empty everything": {},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Validate(p); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted invalid preference: %v", err)
			}
		})
	}
}

func TestEveryCatalogSkinIsSelectable(t *testing.T) {
	for id := range skin.Catalog() {
		if err := Validate(Preference{Skin: id, Figure: Default().Figure}); err != nil {
			t.Fatalf("catalog skin %q not selectable: %v", id, err)
		}
	}
}

// The figure's face (pupils and mouth) is drawn in a fixed dark ink, so every
// selectable figure color must keep the face readable.
func TestFigureColorsKeepFaceVisible(t *testing.T) {
	if len(Colors()) < 4 || len(Shapes()) < 4 {
		t.Fatal("picker needs at least four shapes and four colors")
	}
	for _, c := range Colors() {
		ratio, err := skin.ContrastRatio(faceInk, c.Hex)
		if err != nil {
			t.Fatal(err)
		}
		if ratio < skin.MinNonTextContrast {
			t.Fatalf("face on %s has contrast %.2f", c.ID, ratio)
		}
	}
}

func TestFigureSVG(t *testing.T) {
	for _, shape := range Shapes() {
		for _, color := range Colors() {
			svg, err := FigureSVG(Figure{Shape: shape.ID, Color: color.ID}, 48)
			if err != nil {
				t.Fatalf("%s/%s: %v", shape.ID, color.ID, err)
			}
			out := string(svg)
			for _, want := range []string{`<svg `, `aria-hidden="true"`, `focusable="false"`, `width="48"`, color.Hex} {
				if !strings.Contains(out, want) {
					t.Fatalf("%s/%s: missing %q in %s", shape.ID, color.ID, want, out)
				}
			}
			for _, bad := range []string{"<script", "href", "url(", "<image", "<foreignObject", "on"} {
				if bad == "on" {
					if strings.Contains(out, " on") {
						t.Fatalf("event handler attribute in %s", out)
					}
					continue
				}
				if strings.Contains(out, bad) {
					t.Fatalf("forbidden %q in %s", bad, out)
				}
			}
		}
	}
	for _, bad := range []Figure{{Shape: "photo", Color: "mint"}, {Shape: "ball", Color: "#000000"}} {
		if _, err := FigureSVG(bad, 48); !errors.Is(err, ErrInvalid) {
			t.Fatalf("rendered invalid figure %+v", bad)
		}
	}
	for _, size := range []int{0, 8, 1000} {
		if _, err := FigureSVG(Default().Figure, size); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted size %d", size)
		}
	}
}

func openStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "appearance.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestStoreReturnsDefaultForUnknownSubject(t *testing.T) {
	s, _ := openStore(t)
	if got := s.Get(Subject{Kind: KindGuardian, TenantID: "t", ID: "g"}); got != Default() {
		t.Fatalf("unexpected preference %+v", got)
	}
}

func TestStorePersistsAndIsolatesSubjects(t *testing.T) {
	s, path := openStore(t)
	guardian := Subject{Kind: KindGuardian, TenantID: "tenant-a", ID: "same-id"}
	night := Preference{Skin: "night", Figure: Figure{Shape: "ghost", Color: "peach"}}
	if err := s.Set(guardian, night); err != nil {
		t.Fatal(err)
	}
	others := []Subject{
		{Kind: KindGuardian, TenantID: "tenant-b", ID: "same-id"},
		{Kind: KindChild, TenantID: "tenant-a", ID: "same-id"},
		{Kind: KindGuardian, TenantID: "tenant-a", ID: "other"},
	}
	for _, other := range others {
		if got := s.Get(other); got != Default() {
			t.Fatalf("preference leaked to %+v: %+v", other, got)
		}
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Get(guardian); got != night {
		t.Fatalf("not persisted: %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("store file is not private: %v", info.Mode().Perm())
	}
}

func TestStoreRejectsInvalidInputWithoutWriting(t *testing.T) {
	s, path := openStore(t)
	valid := Subject{Kind: KindChild, TenantID: "t", ID: "c"}
	if err := s.Set(valid, Preference{Skin: "neon", Figure: Default().Figure}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid preference accepted: %v", err)
	}
	for _, bad := range []Subject{
		{Kind: "admin", TenantID: "t", ID: "c"},
		{Kind: KindGuardian, TenantID: "", ID: "c"},
		{Kind: KindGuardian, TenantID: "t", ID: ""},
		{Kind: KindGuardian, TenantID: "t", ID: "bad\nid"},
		{Kind: KindGuardian, TenantID: "t", ID: strings.Repeat("x", 200)},
	} {
		if err := s.Set(bad, Default()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid subject accepted: %+v", bad)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected writes must not create the store file")
	}
}

func TestOpenFailsClosedOnCorruptOrInsecureFile(t *testing.T) {
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(corrupt); err == nil {
		t.Fatal("opened corrupt store")
	}
	shared := filepath.Join(dir, "shared.json")
	if err := os.WriteFile(shared, []byte(`{"preferences":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(shared); err == nil {
		t.Fatal("opened world-readable store")
	}
	badSubject := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badSubject, []byte(`{"preferences":[{"kind":"admin","tenant_id":"t","id":"x","skin":"classic","figure":{"shape":"ball","color":"mint"}}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(badSubject); err == nil {
		t.Fatal("opened store with invalid subject")
	}
}

// A skin may be retired in a later release; stored preferences must then fall
// back to the default skin instead of preventing the service from starting.
func TestRetiredSkinFallsBackOnRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "appearance.json")
	if err := os.WriteFile(path, []byte(`{"preferences":[{"kind":"guardian","tenant_id":"t","id":"g","skin":"retired-skin","figure":{"shape":"ghost","color":"lime"}}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("retired skin prevented startup: %v", err)
	}
	got := s.Get(Subject{Kind: KindGuardian, TenantID: "t", ID: "g"})
	if got.Skin != Default().Skin || got.Figure != (Figure{Shape: "ghost", Color: "lime"}) {
		t.Fatalf("unexpected fallback: %+v", got)
	}
}
