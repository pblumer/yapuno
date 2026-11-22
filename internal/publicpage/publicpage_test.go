package publicpage

import (
	"html/template"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestTheShellIsAGermanMobilePageInTheClassicSkin(t *testing.T) {
	css, err := Styles()
	if err != nil {
		t.Fatal(err)
	}
	page := template.Must(template.New("p").Parse(Open + `<h1>{{.Title}}</h1>` + Close))
	var out strings.Builder
	if err := page.Execute(&out, struct {
		Title string
		CSS   template.CSS
	}{"Probe <b>", css}); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{`<html lang="de">`, "width=device-width", `<meta name="referrer" content="no-referrer">`, "<title>Yapuno – Probe &lt;b&gt;</title>",
		`data-yp-skin="classic"`, "synthetischen Profilen", "</main></body></html>"} {
		if !strings.Contains(body, want) {
			t.Errorf("shell lacks %q", want)
		}
	}
	if strings.Contains(body, "<script") {
		t.Error("shell carries script")
	}
	style := body[strings.Index(body, "<style>")+7 : strings.Index(body, "</style>")]
	// Colours come from the skin: the component rules name tokens only.
	component := style[strings.Index(style, "body{margin:0}"):]
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).MatchString(component) || !strings.Contains(component, "var(--yp-") {
		t.Error("component styles must use skin tokens only")
	}
	if !strings.HasPrefix(style, ".yp-page{--yp-bg:") {
		t.Error("skin tokens missing")
	}
	// Controls are large enough for a thumb and show keyboard focus.
	for _, want := range []string{"min-height:48px", "min-height:44px", ":focus-visible"} {
		if !strings.Contains(component, want) {
			t.Errorf("styles lack %q", want)
		}
	}
}

func TestHeadersForbidCachingFramingAndForeignContent(t *testing.T) {
	w := httptest.NewRecorder()
	Headers(w)
	h := w.Header()
	if h.Get("Content-Type") != "text/html; charset=utf-8" || h.Get("Cache-Control") != "no-store" || h.Get("Referrer-Policy") != "no-referrer" ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("headers: %v", h)
	}
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "style-src 'unsafe-inline'", "font-src 'self'", "form-action 'self'", "base-uri 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("policy lacks %q", want)
		}
	}
	if strings.Contains(csp, "script-src") || strings.Contains(csp, "https:") || strings.Contains(csp, "*") {
		t.Errorf("policy too wide: %q", csp)
	}
}
