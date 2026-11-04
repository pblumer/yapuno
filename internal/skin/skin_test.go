package skin

import "testing"

func defaultSkin() Definition {
	return Definition{Version: SchemaVersion, ID: "classic", Name: "Classic", Palette: Palette{Background: "#ffffff", Foreground: "#111111", Accent: "#3355aa"}, Density: "comfortable", Shape: "rounded"}
}

func TestValidate(t *testing.T) {
	base := defaultSkin()
	if err := Validate(base); err != nil {
		t.Fatalf("valid skin rejected: %v", err)
	}
	cases := map[string]func(*Definition){
		"version":                func(s *Definition) { s.Version = 2 },
		"id":                     func(s *Definition) { s.ID = "../escape" },
		"name":                   func(s *Definition) { s.Name = "" },
		"css injection":          func(s *Definition) { s.Palette.Accent = "red; background:url(https://example.org)" },
		"missing contrast token": func(s *Definition) { s.Palette.Foreground = "" },
		"unknown density":        func(s *Definition) { s.Density = "hidden" },
		"unknown shape":          func(s *Definition) { s.Shape = "script" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := base
			mutate(&s)
			if Validate(s) == nil {
				t.Fatal("invalid skin accepted")
			}
		})
	}
}

func TestResolve(t *testing.T) {
	fallback := defaultSkin()
	alternate := fallback
	alternate.ID = "playful"
	alternate.Name = "Playful"
	alternate.Shape = "square"
	catalog := map[string]Definition{"playful": alternate, "broken": {ID: "broken"}}
	if got := Resolve("playful", catalog, fallback); got.ID != "playful" {
		t.Fatal("valid preference ignored")
	}
	for _, preferred := range []string{"missing", "broken", ""} {
		if got := Resolve(preferred, catalog, fallback); got.ID != "classic" {
			t.Fatalf("unsafe fallback for %q", preferred)
		}
	}
}
