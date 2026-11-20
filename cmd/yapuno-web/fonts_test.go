package main

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// Fonts are public static files of this origin: every URL a page declares
// must resolve here, without a session, and nowhere else.
func TestDeclaredFontsAreServedByThisOrigin(t *testing.T) {
	s := newContactServer(t, true)
	page := get(s.handler, "/family?skin=vivid", s.cookieA)
	if page.Code != http.StatusOK || !strings.Contains(page.Header().Get("Content-Security-Policy"), "font-src 'self'") {
		t.Fatalf("family page: %d %q", page.Code, page.Header().Get("Content-Security-Policy"))
	}
	urls := regexp.MustCompile(`url\((/assets/fonts/[^)]+)\)`).FindAllStringSubmatch(page.Body.String(), -1)
	if len(urls) != 4 {
		t.Fatalf("expected four font URLs for the vivid skin, got %d", len(urls))
	}
	for _, m := range urls {
		font := get(s.handler, m[1], nil)
		if font.Code != http.StatusOK || font.Header().Get("Content-Type") != "font/woff2" || !strings.Contains(font.Header().Get("Cache-Control"), "immutable") || len(font.Header().Values("Set-Cookie")) != 0 {
			t.Fatalf("%s: %d %v", m[1], font.Code, font.Header())
		}
	}
	if w := get(s.handler, "/assets/fonts/LICENSES.txt", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "SIL OPEN FONT LICENSE") {
		t.Fatalf("licences: %d", w.Code)
	}
	for _, missing := range []string{"/assets/fonts/", "/assets/fonts/nope.woff2", "/assets/", "/assets/fonts/../../family"} {
		if w := get(s.handler, missing, s.cookieA); w.Code == http.StatusOK {
			t.Fatalf("%s served", missing)
		}
	}
}
