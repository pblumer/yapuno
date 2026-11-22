package sameorigin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"
)

func request(headers map[string][]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "https://app.example.org/pair/claim", nil)
	for name, values := range headers {
		for _, v := range values {
			r.Header.Add(name, v)
		}
	}
	return r
}

func TestARequestFromAPageOfThisOriginIsAccepted(t *testing.T) {
	for name, headers := range map[string]map[string][]string{
		"origin only (older browsers)": {"Origin": {"https://app.example.org"}},
		"origin and fetch metadata":    {"Origin": {"https://app.example.org"}, "Sec-Fetch-Site": {"same-origin"}},
		// WebKit and Firefox follow the Fetch standard: a same-origin request
		// that is not in CORS mode, from a page with Referrer-Policy
		// no-referrer, carries "Origin: null". Fetch metadata still says
		// where it came from, and a page cannot set that header.
		"null origin with fetch metadata": {"Origin": {"null"}, "Sec-Fetch-Site": {"same-origin"}},
		"no origin with fetch metadata":   {"Sec-Fetch-Site": {"same-origin"}},
	} {
		if !Request(request(headers)) {
			t.Errorf("%s: refused", name)
		}
	}
}

func TestEverythingElseIsRefused(t *testing.T) {
	for name, headers := range map[string]map[string][]string{
		"nothing":           {},
		"null origin alone": {"Origin": {"null"}},
		"other origin":      {"Origin": {"https://evil.example"}},
		"other origin, forged-looking metadata is still checked": {"Origin": {"https://evil.example"}, "Sec-Fetch-Site": {"same-origin"}},
		"http instead of https":                                  {"Origin": {"http://app.example.org"}},
		"other port":                                             {"Origin": {"https://app.example.org:8443"}},
		"subdomain":                                              {"Origin": {"https://x.app.example.org"}},
		"suffix":                                                 {"Origin": {"https://app.example.org.evil.example"}},
		"origin with path":                                       {"Origin": {"https://app.example.org/"}},
		"right origin but cross-site":                            {"Origin": {"https://app.example.org"}, "Sec-Fetch-Site": {"cross-site"}},
		"right origin but same-site only":                        {"Origin": {"https://app.example.org"}, "Sec-Fetch-Site": {"same-site"}},
		"null origin, cross-site":                                {"Origin": {"null"}, "Sec-Fetch-Site": {"cross-site"}},
		"null origin, same-site":                                 {"Origin": {"null"}, "Sec-Fetch-Site": {"same-site"}},
		"null origin, none":                                      {"Origin": {"null"}, "Sec-Fetch-Site": {"none"}},
		"no origin, cross-site":                                  {"Sec-Fetch-Site": {"cross-site"}},
		"two origins":                                            {"Origin": {"https://app.example.org", "https://evil.example"}},
		"two metadata values":                                    {"Origin": {"null"}, "Sec-Fetch-Site": {"same-origin", "cross-site"}},
		// A second value must not hide a cross-site one behind a good origin.
		"right origin, two metadata values": {"Origin": {"https://app.example.org"}, "Sec-Fetch-Site": {"cross-site", "same-origin"}},
		"metadata in other case":            {"Origin": {"null"}, "Sec-Fetch-Site": {"Same-Origin"}},
		"empty metadata":                    {"Origin": {"null"}, "Sec-Fetch-Site": {""}},
	} {
		if Request(request(headers)) {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTheOriginIsComparedWithTheHostOfTheRequest(t *testing.T) {
	r := request(map[string][]string{"Origin": {"https://app.example.org"}})
	r.Host = "other.example.org"
	if Request(r) {
		t.Fatal("origin accepted for another host")
	}
	r.Host = ""
	if Request(r) {
		t.Fatal("accepted without a host")
	}
	// Without a host there is nothing to compare with, whatever the header says.
	r.Header.Set("Origin", "https://")
	if Request(r) {
		t.Fatal("an origin without a host matched a request without a host")
	}
}

// The pages set Referrer-Policy no-referrer. A POST in "same-origin" mode
// from such a page carries "Origin: null" in WebKit and Firefox, which broke
// pairing on an iPhone. The scripts therefore post in the default mode.
func TestScriptsDoNotPostInSameOriginMode(t *testing.T) {
	post := regexp.MustCompile(`(?s)fetch\([^)]*?method:\s*"POST"[^)]*?\)`)
	for _, path := range []string{"../pairingweb/pair.js", "../childweb/app.js", "../chatpage/chat.js"} {
		script, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		calls := post.FindAll(script, -1)
		if len(calls) == 0 {
			t.Errorf("%s: no POST found; the check no longer looks at anything", path)
		}
		for _, call := range calls {
			if regexp.MustCompile(`mode:\s*"same-origin"`).Match(call) {
				t.Errorf("%s posts in same-origin mode: %s", path, call)
			}
			if !regexp.MustCompile(`redirect:\s*"error"`).Match(call) {
				t.Errorf("%s: a POST may follow redirects: %s", path, call)
			}
		}
	}
}
