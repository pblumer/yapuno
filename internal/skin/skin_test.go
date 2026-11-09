package skin

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// validSkin is an independent fixture so tests do not depend on catalog values.
func validSkin() Definition {
	return Definition{
		Version: SchemaVersion,
		ID:      "fixture",
		Name:    "Fixture",
		Colors: Colors{
			Background: "#ffffff", Surface: "#f3f5fa", Text: "#141a2e", TextMuted: "#5c6378", Line: "#dde1ea",
			Accent: "#3355aa", OnAccent: "#ffffff", AccentText: "#3355aa",
			OwnBubble: "#3355aa", OnOwnBubble: "#ffffff", OtherBubble: "#eef1f7", OnOtherBubble: "#141a2e",
			Badge: "#3355aa", OnBadge: "#ffffff", DangerText: "#b3261e", Focus: "#3355aa",
		},
		Typography: Typography{Heading: "system", Body: "system", BaseSize: 16},
		Shape:      Shape{Radius: "soft", Bubble: "flat"},
		Wallpaper:  "none",
		Avatar:     "initials",
		Density:    "comfortable",
		Motion:     "calm",
	}
}

func TestValidateAcceptsFixture(t *testing.T) {
	if err := Validate(validSkin()); err != nil {
		t.Fatalf("valid skin rejected: %v", err)
	}
}

func TestValidateRejectsInvalidDefinitions(t *testing.T) {
	cases := map[string]func(*Definition){
		"old schema version":     func(s *Definition) { s.Version = 1 },
		"future schema version":  func(s *Definition) { s.Version = SchemaVersion + 1 },
		"path-like id":           func(s *Definition) { s.ID = "../escape" },
		"uppercase id":           func(s *Definition) { s.ID = "Classic" },
		"empty name":             func(s *Definition) { s.Name = "" },
		"overlong name":          func(s *Definition) { s.Name = strings.Repeat("a", 81) },
		"control char in name":   func(s *Definition) { s.Name = "Bad\nName" },
		"css injection in color": func(s *Definition) { s.Colors.Accent = "red; background:url(https://example.org)" },
		"short hex color":        func(s *Definition) { s.Colors.Text = "#000" },
		"missing color":          func(s *Definition) { s.Colors.Focus = "" },
		"unknown heading font":   func(s *Definition) { s.Typography.Heading = "Comic Sans" },
		"unknown body font":      func(s *Definition) { s.Typography.Body = "https://fonts.invalid/x.woff2" },
		"base size too small":    func(s *Definition) { s.Typography.BaseSize = 14 },
		"base size too large":    func(s *Definition) { s.Typography.BaseSize = 24 },
		"unknown radius":         func(s *Definition) { s.Shape.Radius = "blob" },
		"unknown bubble style":   func(s *Definition) { s.Shape.Bubble = "script" },
		"unknown wallpaper":      func(s *Definition) { s.Wallpaper = "url(x)" },
		"unknown avatar":         func(s *Definition) { s.Avatar = "photo" },
		"unknown density":        func(s *Definition) { s.Density = "hidden" },
		"unknown motion":         func(s *Definition) { s.Motion = "wild" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := validSkin()
			mutate(&s)
			err := Validate(s)
			if !errors.Is(err, ErrInvalidSkin) {
				t.Fatalf("invalid skin accepted or wrong error: %v", err)
			}
		})
	}
}

func TestValidateEnforcesContrast(t *testing.T) {
	cases := map[string]func(*Definition){
		"text on background":        func(s *Definition) { s.Colors.Text = "#cccccc" },
		"muted text on background":  func(s *Definition) { s.Colors.TextMuted = "#9a9a9a" },
		"muted text on surface":     func(s *Definition) { s.Colors.Surface = "#8a8f9c" },
		"label on accent":           func(s *Definition) { s.Colors.OnAccent = "#7f8fc0" },
		"accent used as text":       func(s *Definition) { s.Colors.AccentText = "#a9b8e8" },
		"own bubble text":           func(s *Definition) { s.Colors.OwnBubble = "#bdece2" },
		"other bubble text":         func(s *Definition) { s.Colors.OnOtherBubble = "#c0c4cc" },
		"badge text":                func(s *Definition) { s.Colors.Badge = "#d9f692" },
		"danger text on background": func(s *Definition) { s.Colors.DangerText = "#ff9b93" },
		"focus indicator":           func(s *Definition) { s.Colors.Focus = "#e0e4ff" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := validSkin()
			mutate(&s)
			if err := Validate(s); !errors.Is(err, ErrInvalidSkin) {
				t.Fatalf("low-contrast skin accepted: %v", err)
			}
		})
	}
}

// Every token except the display name must be constrained to a hex color or a
// closed vocabulary. This guards future fields against free-form CSS.
func TestValidateRejectsInjectionInEveryTokenField(t *testing.T) {
	const payload = "#ffffff;background:url(https://attacker.invalid/x)"
	var paths []string
	collectStringFields(reflect.TypeOf(Definition{}), nil, &paths)
	if len(paths) < 20 {
		t.Fatalf("expected the full token set, found only %d string fields", len(paths))
	}
	for _, path := range paths {
		if path == "Name" {
			continue
		}
		t.Run(path, func(t *testing.T) {
			s := validSkin()
			field := reflect.ValueOf(&s).Elem().FieldByName(strings.Split(path, ".")[0])
			for _, part := range strings.Split(path, ".")[1:] {
				field = field.FieldByName(part)
			}
			field.SetString(payload)
			if err := Validate(s); !errors.Is(err, ErrInvalidSkin) {
				t.Fatalf("payload accepted in %s", path)
			}
		})
	}
}

func collectStringFields(typ reflect.Type, prefix []string, out *[]string) {
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		path := append(append([]string{}, prefix...), f.Name)
		switch f.Type.Kind() {
		case reflect.String:
			*out = append(*out, strings.Join(path, "."))
		case reflect.Struct:
			collectStringFields(f.Type, path, out)
		}
	}
}

func TestCatalogSkinsAreValid(t *testing.T) {
	catalog := Catalog()
	if len(catalog) < 4 {
		t.Fatalf("expected at least four first-party skins, got %d", len(catalog))
	}
	for id, s := range catalog {
		if s.ID != id {
			t.Fatalf("catalog key %q does not match skin id %q", id, s.ID)
		}
		if err := Validate(s); err != nil {
			t.Fatalf("first-party skin %q invalid: %v", id, err)
		}
	}
	for _, required := range []string{"classic", "playful"} {
		if _, ok := catalog[required]; !ok {
			t.Fatalf("existing skin id %q removed; stored preferences would silently change", required)
		}
	}
}

// Issue #16 requires radically different skins, not color swaps. The catalog
// must vary typography, shape, bubble treatment, wallpaper and avatar style.
func TestCatalogSkinsDifferBeyondColor(t *testing.T) {
	fonts, radii, bubbles, walls, avatars := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, s := range Catalog() {
		fonts[s.Typography.Body] = true
		radii[s.Shape.Radius] = true
		bubbles[s.Shape.Bubble] = true
		walls[s.Wallpaper] = true
		avatars[s.Avatar] = true
	}
	for name, got := range map[string]int{"body fonts": len(fonts), "radii": len(radii), "bubble styles": len(bubbles), "wallpapers": len(walls)} {
		if got < 3 {
			t.Fatalf("only %d distinct %s across catalog", got, name)
		}
	}
	if len(avatars) < 2 {
		t.Fatal("catalog does not exercise both avatar styles")
	}
}

func TestResolve(t *testing.T) {
	fallback := validSkin()
	alternate := validSkin()
	alternate.ID = "alternate"
	catalog := map[string]Definition{"alternate": alternate, "broken": {ID: "broken"}, "mismatch": alternate}
	if got := Resolve("alternate", catalog, fallback); got.ID != "alternate" {
		t.Fatal("valid preference ignored")
	}
	for _, preferred := range []string{"missing", "broken", "mismatch", ""} {
		if got := Resolve(preferred, catalog, fallback); got.ID != "fixture" {
			t.Fatalf("unsafe fallback for %q: %q", preferred, got.ID)
		}
	}
	if got := Resolve("alternate", catalog, Definition{}); got.ID != "" {
		t.Fatal("invalid fallback must not be returned as usable skin")
	}
}
