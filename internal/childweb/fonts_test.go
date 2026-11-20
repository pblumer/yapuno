package childweb

import (
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/appearance"
)

func TestChildPagesAllowAndDeclareOnlySelfHostedFonts(t *testing.T) {
	cw := newChatWorld(t)
	targets := map[string]string{"home": "/child", "chat": "/child/chat/" + cw.chat, "look": "/child/look"}
	for name, target := range map[string]string{"home": targets["home"], "chat": targets["chat"]} {
		if body := cw.get(target, cw.alex).Body.String(); strings.Contains(body, "@font-face") {
			t.Errorf("%s declares fonts for the classic skin", name)
		}
	}
	if err := cw.looks.Set(childSubject("tenant-a", "child-a"), appearance.Preference{Skin: "cloud", Figure: appearance.Default().Figure}); err != nil {
		t.Fatal(err)
	}
	// Bea's theme uses another font; it must not show up on Alex's pages.
	if err := cw.looks.Set(childSubject("tenant-b", "child-b"), appearance.Preference{Skin: "terminal", Figure: appearance.Default().Figure}); err != nil {
		t.Fatal(err)
	}
	for name, target := range targets {
		rec := cw.get(target, cw.alex)
		body, csp := rec.Body.String(), rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "font-src 'self'") || !strings.Contains(csp, "default-src 'none'") {
			t.Errorf("%s: CSP does not allow self-hosted fonts only: %q", name, csp)
		}
		if !strings.Contains(body, `font-family:"Nunito"`) {
			t.Errorf("%s: missing the skin's font", name)
		}
		if name != "look" && (strings.Count(body, "@font-face") != 2 || strings.Contains(body, "JetBrains")) {
			t.Errorf("%s: declares fonts beyond its own skin", name)
		}
		if strings.Contains(body, "googleapis") || strings.Contains(body, "gstatic") {
			t.Errorf("%s: external font reference", name)
		}
	}
	// The signed-out shell knows no skin and uses system fonts.
	if shell := cw.get("/child", nil); strings.Contains(shell.Body.String(), "@font-face") {
		t.Fatal("login shell declares fonts")
	}
}
