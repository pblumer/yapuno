// Package fontassets ships the web fonts named by the skin contract and
// serves them from the application's own origin. No page ever requests a
// font from a third party, so font loading tells nobody who uses Yapuno.
//
// The files are unmodified Latin and Latin Extended subsets from the
// @fontsource-variable packages, all under the SIL Open Font License 1.1.
// See SOURCE.md and the licenses directory.
package fontassets

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

//go:embed files/*.woff2
var files embed.FS

//go:embed licenses/*.txt
var licenseFiles embed.FS

// Prefix is where Handler must be mounted.
const Prefix = "/assets/fonts/"

// Unicode ranges of the two subsets, as published with the files. The
// browser downloads a subset only when a page shows a character from it.
const (
	latinRange    = "U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+20AC,U+2122,U+2191,U+2193,U+2212,U+2215,U+FEFF,U+FFFD"
	latinExtRange = "U+0100-02BA,U+02BD-02C5,U+02C7-02CC,U+02CE-02D7,U+02DD-02FF,U+0304,U+0308,U+0329,U+1D00-1DBF,U+1E00-1E9F,U+1EF2-1EFF,U+2020,U+20A0-20AB,U+20AD-20C0,U+2113,U+2C60-2C7F,U+A720-A7FF"
)

// font describes one family. key is the skin vocabulary value; family is the
// name the skin's font stack asks for; weights is the variable weight range.
type font struct {
	key, family, file, weights string
}

var fonts = []font{
	{"bricolage", "Bricolage Grotesque", "bricolage-grotesque", "200 800"},
	{"jakarta", "Plus Jakarta Sans", "plus-jakarta-sans", "200 800"},
	{"mono", "JetBrains Mono", "jetbrains-mono", "100 800"},
	{"nunito", "Nunito", "nunito", "200 1000"},
}

type asset struct {
	data []byte
}

var (
	// served maps the public file name, which contains a content hash, to
	// its bytes. Nothing outside this map is ever served.
	served = map[string]asset{}
	// faces holds the ready @font-face rules per font key.
	faces    = map[string]string{}
	licenses string
)

func init() {
	for _, f := range fonts {
		var css strings.Builder
		for _, subset := range []struct{ name, unicode string }{{"latin-ext", latinExtRange}, {"latin", latinRange}} {
			data, err := files.ReadFile("files/" + f.file + "-" + subset.name + "-wght-normal.woff2")
			if err != nil {
				panic("fontassets: missing embedded font: " + err.Error())
			}
			sum := sha256.Sum256(data)
			// The hash in the name lets browsers cache the file forever: a
			// changed file gets a new name.
			name := f.file + "-" + subset.name + "." + hex.EncodeToString(sum[:6]) + ".woff2"
			served[name] = asset{data: data}
			css.WriteString(`@font-face{font-family:"` + f.family + `";font-style:normal;font-display:swap;font-weight:` + f.weights +
				`;src:url(` + Prefix + name + `) format("woff2");unicode-range:` + subset.unicode + `}` + "\n")
		}
		faces[f.key] = css.String()
	}
	entries, err := licenseFiles.ReadDir("licenses")
	if err != nil {
		panic("fontassets: missing licences: " + err.Error())
	}
	var all strings.Builder
	all.WriteString("Fonts served by Yapuno and their licences.\n")
	for _, e := range entries {
		text, err := licenseFiles.ReadFile("licenses/" + e.Name())
		if err != nil {
			panic("fontassets: " + err.Error())
		}
		all.WriteString("\n==================== " + e.Name() + " ====================\n\n")
		all.Write(text)
		all.WriteString("\n")
	}
	licenses = all.String()
}

// Keys lists the skin font keys that have files, in a stable order.
func Keys() []string {
	keys := make([]string, 0, len(fonts))
	for _, f := range fonts {
		keys = append(keys, f.key)
	}
	sort.Strings(keys)
	return keys
}

// FaceCSS returns @font-face rules for the given skin font keys. Keys without
// files, such as "system", yield nothing. The result contains only fixed text
// and same-origin URLs; it never depends on user input beyond the key lookup.
func FaceCSS(keys ...string) string {
	wanted := map[string]bool{}
	for _, k := range keys {
		if _, ok := faces[k]; ok {
			wanted[k] = true
		}
	}
	var out strings.Builder
	for _, k := range Keys() {
		if wanted[k] {
			out.WriteString(faces[k])
		}
	}
	return out.String()
}

// Licenses returns the copyright notices and licence texts of all fonts.
func Licenses() string { return licenses }

// Handler serves the font files and LICENSES.txt under Prefix. Fonts are
// public, identical for everyone and named by content hash, so they are
// cached without revalidation. No cookie is read or set.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// r.URL.Path is compared literally: no cleaning, no directory access.
		name, ok := strings.CutPrefix(r.URL.Path, Prefix)
		if !ok {
			http.NotFound(w, r)
			return
		}
		var body []byte
		switch a, found := served[name]; {
		case found:
			body = a.data
			w.Header().Set("Content-Type", "font/woff2")
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case name == "LICENSES.txt":
			body = []byte(licenses)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=86400")
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(body)
	})
}
