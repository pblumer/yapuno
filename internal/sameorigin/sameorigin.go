// Package sameorigin decides whether a scripted, state-changing request
// provably comes from a page of this very origin. It is the cross-site
// request check of the JSON endpoints; it is not authentication.
package sameorigin

import "net/http"

// Request reports whether the browser says the request was made by a page of
// the request's own HTTPS origin.
//
// The Origin header must name exactly "https://" + Host. There is one case in
// which an honest browser does not send it: under the Fetch standard a
// same-origin request that is not in CORS mode, made by a page with
// Referrer-Policy "no-referrer", carries "Origin: null". WebKit (every
// browser on an iPhone) and Firefox do that; Chromium does not. For that case
// the Sec-Fetch-Site header decides. The browser sets it, a page cannot, and
// "same-origin" means scheme, host and port all match.
//
// If Sec-Fetch-Site is present it must say "same-origin" in every case, so a
// browser that sends it can only make the check stricter.
func Request(r *http.Request) bool {
	if r.Host == "" {
		return false
	}
	origins, sites := r.Header.Values("Origin"), r.Header.Values("Sec-Fetch-Site")
	if len(origins) > 1 || len(sites) > 1 {
		return false
	}
	sameSite := len(sites) == 1 && sites[0] == "same-origin"
	if len(sites) == 1 && !sameSite {
		return false
	}
	if len(origins) == 1 && origins[0] == "https://"+r.Host {
		return true
	}
	// No usable Origin: only fetch metadata can vouch for the request.
	return sameSite && (len(origins) == 0 || origins[0] == "null")
}
