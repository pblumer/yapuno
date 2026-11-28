package webpush

import (
	"net"
	"net/url"
	"strings"
)

const maxEndpoint = 2048

// pushHosts are the push services of the browsers Yapuno supports. The
// server sends requests only to these hosts, so a subscription cannot turn it
// into a client for arbitrary addresses. A leading dot means any subdomain.
var pushHosts = []string{
	".push.apple.com",            // Safari on iOS, iPadOS and macOS
	"fcm.googleapis.com",         // Chrome, Samsung Internet, Edge on Android
	".push.services.mozilla.com", // Firefox
	".notify.windows.com",        // Edge on Windows
}

func knownHost(host string) bool {
	for _, h := range pushHosts {
		if strings.HasPrefix(h, ".") {
			if strings.HasSuffix(host, h) && len(host) > len(h) {
				return true
			}
		} else if host == h {
			return true
		}
	}
	return false
}

// ValidEndpoint reports whether a push endpoint may be stored and contacted:
// HTTPS on the default port of a known push service, lower-case host, no user
// information, no fragment, a non-empty path and no query, except the single
// token parameter of the Windows service.
func ValidEndpoint(raw string) bool {
	if raw == "" || len(raw) > maxEndpoint {
		return false
	}
	for _, c := range raw {
		if c <= ' ' || c == 0x7f || c > 0x7e {
			return false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") {
		return false
	}
	host := u.Host
	if host == "" || host != strings.ToLower(host) || strings.Contains(host, ":") || net.ParseIP(strings.Trim(host, "[]")) != nil || !knownHost(host) {
		return false
	}
	if len(u.EscapedPath()) < 2 || !strings.HasPrefix(u.EscapedPath(), "/") {
		return false
	}
	if raw[len(raw)-1] == '?' {
		return false
	}
	if u.RawQuery != "" {
		if !strings.HasSuffix(host, ".notify.windows.com") {
			return false
		}
		values, err := url.ParseQuery(u.RawQuery)
		if err != nil || len(values) != 1 || len(values["token"]) != 1 || values.Get("token") == "" {
			return false
		}
	}
	return true
}
