package familydashboard

import (
	"strings"
	"testing"
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
	for _, want := range []string{"&lt;script&gt;", "data-yp-skin=\"classic\"", "aria-labelledby", "min-width:640px", "Devices", "Contacts"} {
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
