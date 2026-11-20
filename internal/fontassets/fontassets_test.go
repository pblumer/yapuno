package fontassets

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/yapuno/internal/skin"
)

// Checksums of the files taken from @fontsource-variable 5.3.0 (see SOURCE.md).
// A changed or corrupted file must fail here, not in a browser.
var expected = map[string]string{
	"bricolage-grotesque-latin-ext-wght-normal.woff2": "e808f16b56f84dc7933e498cc8a41d4a5cd853adc6eef154584baa5de1008b15",
	"bricolage-grotesque-latin-wght-normal.woff2":     "a97804dc9fbe5fc972a08018c5eda4dab7ef2346f64c57e61419d05e6de4ea1c",
	"jetbrains-mono-latin-ext-wght-normal.woff2":      "79bfdab9ba467e26eea4122e6f2567e188dd8a09a8c730d501fc487c4ab99c6e",
	"jetbrains-mono-latin-wght-normal.woff2":          "18be452724bfdc236c074ca94a249a7f41a86752c7d04ab258ce9ed5651f6a7e",
	"nunito-latin-ext-wght-normal.woff2":              "2c8d792869818ecb253a46bc3c63c7013df7aac2f69291c3c85e5cdc94160960",
	"nunito-latin-wght-normal.woff2":                  "ba344451eab25b217a165363b1982048a5e5830a0daf36577973955a04cac793",
	"plus-jakarta-sans-latin-ext-wght-normal.woff2":   "38e3b8fd8045048eb311d90170a4429ed2c8f405852dc3d91b5af8452758703f",
	"plus-jakarta-sans-latin-wght-normal.woff2":       "153fc85b70298beeb1d61a5f723331649e7f23bb77302a66e61cb3e2fbdb5e79",
}

var fontURL = regexp.MustCompile(`url\((/assets/fonts/[^)]+)\)`)

func get(t *testing.T, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(method, "https://app.example.org"+target, nil))
	return rec
}

func TestEmbeddedFilesAreExactlyTheVettedOnes(t *testing.T) {
	entries, err := files.ReadDir("files")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(expected) {
		t.Fatalf("embedded %d files, expected %d", len(entries), len(expected))
	}
	total := 0
	for _, e := range entries {
		data, err := files.ReadFile("files/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if expected[e.Name()] != hex.EncodeToString(sum[:]) {
			t.Errorf("%s: unexpected content", e.Name())
		}
		if string(data[:4]) != "wOF2" {
			t.Errorf("%s is not a WOFF2 file", e.Name())
		}
		total += len(data)
	}
	// Measured payload of all eight files; a page loads only what its skin uses.
	if total != 239404 {
		t.Fatalf("total font payload changed: %d bytes", total)
	}
}

func TestEveryFontComesWithItsLicense(t *testing.T) {
	text := Licenses()
	for _, family := range []string{"Plus Jakarta Sans", "Bricolage Grotesque", "Nunito", "JetBrains Mono"} {
		if !strings.Contains(text, "The "+family+" Project Authors") {
			t.Errorf("no copyright notice for %s", family)
		}
	}
	if strings.Count(text, "SIL OPEN FONT LICENSE Version 1.1") != 4 {
		t.Fatal("expected four OFL 1.1 texts")
	}
	// The OFL forbids using a declared Reserved Font Name for modified
	// versions such as subsets. None of these fonts declares one: the phrase
	// appears only inside the licence's own definitions and conditions.
	for _, part := range strings.Split(text, "SIL OPEN FONT LICENSE Version 1.1")[:4] {
		if i := strings.LastIndex(part, "Copyright"); i >= 0 && strings.Contains(part[i:], "Reserved Font Name") {
			t.Fatal("a font declares a Reserved Font Name; subsetting needs review")
		}
	}
	rec := get(t, http.MethodGet, "/assets/fonts/LICENSES.txt")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") || rec.Body.String() != text {
		t.Fatalf("licences not served: %d", rec.Code)
	}
}

func TestFaceCSSDeclaresOnlyRequestedKnownFonts(t *testing.T) {
	if css := FaceCSS("system", "", "unknown", "../etc"); css != "" {
		t.Fatalf("faces emitted for fonts without files: %q", css)
	}
	css := FaceCSS("nunito", "jakarta", "nunito", "system")
	if strings.Count(css, "@font-face") != 4 || strings.Count(css, `font-family:"Nunito"`) != 2 || strings.Count(css, `font-family:"Plus Jakarta Sans"`) != 2 {
		t.Fatalf("unexpected faces: %s", css)
	}
	if FaceCSS("jakarta", "nunito") != css {
		t.Fatal("output must not depend on argument order or repetition")
	}
	for _, want := range []string{"font-display:swap", `format("woff2")`, "font-weight:200 1000", "font-weight:200 800", "unicode-range:U+0000-00FF", "unicode-range:U+0100-02BA", "font-style:normal"} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %q", want)
		}
	}
	lower := strings.ToLower(css)
	for _, bad := range []string{"http://", "https://", "//fonts", "googleapis", "gstatic", "@import", "</style", "expression("} {
		if strings.Contains(lower, bad) {
			t.Errorf("forbidden %q in font CSS", bad)
		}
	}
	all := FaceCSS(Keys()...)
	if strings.Count(all, "@font-face") != 2*len(Keys()) || len(Keys()) != 4 {
		t.Fatalf("expected two faces for each of four fonts, got %d", strings.Count(all, "@font-face"))
	}
}

func TestEveryDeclaredURLIsServedAsImmutableFont(t *testing.T) {
	urls := fontURL.FindAllStringSubmatch(FaceCSS(Keys()...), -1)
	if len(urls) != len(expected) {
		t.Fatalf("declared %d URLs for %d files", len(urls), len(expected))
	}
	seen := map[string]bool{}
	for _, m := range urls {
		if seen[m[1]] {
			t.Fatalf("URL declared twice: %s", m[1])
		}
		seen[m[1]] = true
		rec := get(t, http.MethodGet, m[1])
		sum := sha256.Sum256(rec.Body.Bytes())
		found := false
		for _, want := range expected {
			found = found || want == hex.EncodeToString(sum[:])
		}
		if rec.Code != http.StatusOK || !found {
			t.Fatalf("%s: %d, vetted content: %v", m[1], rec.Code, found)
		}
		h := rec.Header()
		if h.Get("Content-Type") != "font/woff2" || h.Get("Cache-Control") != "public, max-age=31536000, immutable" ||
			h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cross-Origin-Resource-Policy") != "same-origin" || h.Get("Set-Cookie") != "" {
			t.Fatalf("%s headers: %v", m[1], h)
		}
		head := get(t, http.MethodHead, m[1])
		if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") == "" {
			t.Fatalf("HEAD %s: %d", m[1], head.Code)
		}
	}
}

func TestHandlerServesNothingElse(t *testing.T) {
	valid := fontURL.FindStringSubmatch(FaceCSS("nunito"))[1]
	for _, target := range []string{
		"/assets/fonts/", "/assets/fonts", "/assets/fonts/nunito-latin-wght-normal.woff2",
		"/assets/fonts/files/nunito-latin-wght-normal.woff2", "/assets/fonts/../fontassets.go", "/assets/fonts/%2e%2e/%2e%2e/go.mod",
		"/assets/fonts/licenses/nunito-OFL.txt", "/assets/fonts/SOURCE.md", valid + "x", strings.Replace(valid, ".woff2", ".WOFF2", 1),
		strings.Replace(valid, "/assets/fonts/", "/assets/fonts//", 1), "/assets/other",
	} {
		if rec := get(t, http.MethodGet, target); rec.Code == http.StatusOK {
			body, _ := io.ReadAll(rec.Body)
			t.Errorf("%s served (%d bytes)", target, len(body))
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if rec := get(t, method, valid); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", method, rec.Code)
		}
	}
}

// The skin contract names fonts by key. Every font a first-party skin uses
// must either be the system stack or have files here under the family name
// that the skin's CSS asks for.
func TestEverySkinFontHasFilesUnderTheNameTheSkinUses(t *testing.T) {
	for id, def := range skin.Catalog() {
		decls, err := skin.Declarations(def)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{def.Typography.Heading, def.Typography.Body} {
			if key == "system" {
				continue
			}
			css := FaceCSS(key)
			m := regexp.MustCompile(`font-family:("[^"]+")`).FindStringSubmatch(css)
			if m == nil {
				t.Fatalf("skin %s uses font %q without files", id, key)
			}
			if !strings.Contains(decls, m[1]) && !strings.Contains(decls, strings.Trim(m[1], `"`)+",") {
				t.Errorf("skin %s: family %s is not what its CSS requests", id, m[1])
			}
		}
	}
}
