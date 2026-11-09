package familydashboard

import (
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/skin"
)

func TestRenderHTMLEscapesChildNamesAndFallsBackToKnownSkin(t *testing.T) {
	html, err := RenderHTML([]ChildCard{
		{ID: "c1", DisplayName: "<script>alert(1)</script>", Permissions: Permissions{ManageDevices: true}},
		{ID: "c2", DisplayName: "Second", Permissions: Permissions{ApproveContacts: true}},
	}, "unknown\" onclick=\"evil")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "<script>alert(1)</script>") || strings.Contains(html, "onclick=") {
		t.Fatal("untrusted input was not escaped or skin was not constrained")
	}
	for _, want := range []string{
		"&lt;script&gt;",
		"data-yp-skin=\"classic\"",
		"aria-labelledby",
		"min-width:640px",
		"Devices",
		"Contacts",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestRenderHTMLPlayfulAndEmptyState(t *testing.T) {
	html, err := RenderHTML(nil, "playful")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "data-yp-skin=\"playful\"") || !strings.Contains(html, "No children available.") {
		t.Fatal("missing skin or empty state")
	}
	if strings.Contains(html, "<article") {
		t.Fatal("unexpected child card")
	}
}

func TestRenderHTMLHidesUnauthorizedActions(t *testing.T) {
	html, err := RenderHTML([]ChildCard{{ID: "c", DisplayName: "Child"}}, "classic")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "yp-action\">Devices") || strings.Contains(html, "yp-action\">Contacts") {
		t.Fatal("unauthorized action rendered")
	}
}

// Every skin must render, and the dashboard may only reference CSS variables
// the skin contract actually declares. Component rules must not hard-code
// colors; appearance comes from skin tokens only (issue #16).
func TestRenderHTMLUsesOnlyDeclaredSkinTokens(t *testing.T) {
	usage := regexp.MustCompile(`var\((--yp-[a-z-]+)\)`)
	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	cards := []ChildCard{{ID: "c", DisplayName: "Child", Permissions: Permissions{ManageDevices: true, ApproveContacts: true}}}
	for id := range skin.Catalog() {
		t.Run(id, func(t *testing.T) {
			html, err := RenderHTML(cards, id)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(html, `data-yp-skin="`+id+`"`) {
				t.Fatalf("skin %q not applied", id)
			}
			start, end := strings.Index(html, "<style>"), strings.Index(html, "</style>")
			if start < 0 || end < start {
				t.Fatal("style block missing")
			}
			style := html[start:end]
			for _, m := range usage.FindAllStringSubmatch(style, -1) {
				if !strings.Contains(style, m[1]+":") {
					t.Errorf("dashboard uses undeclared token %s", m[1])
				}
			}
			tokens, err := skin.CSSVariables(skin.Catalog()[id])
			if err != nil {
				t.Fatal(err)
			}
			body := strings.TrimSuffix(strings.TrimPrefix(tokens, ":root{"), "}")
			if rest := strings.Replace(style, body, "", 1); hex.MatchString(rest) {
				t.Errorf("hard-coded color in component rules: %v", hex.FindAllString(rest, -1))
			}
		})
	}
}
