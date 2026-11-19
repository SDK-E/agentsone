// SPDX-License-Identifier: MIT

package domain

import "net/url"

// OriginCheck is what a browser tells us about where an unsafe request
// came from.
type OriginCheck struct {
	Method string
	// Origin is the Origin header.
	Origin string
	// Scheme, when set, is the scheme the Origin must have too: the
	// configured public URL's, or the one the edge says the browser used.
	Scheme string
	// FetchSite is the Sec-Fetch-Site header.
	FetchSite string
	// Host is the Host the request was sent to.
	Host string
}

// SameOrigin is the CSRF rule for cookie sessions, on top of
// SameSite=Strict: an unsafe method must come from our own origin, shown by
// the Origin header or, when a browser omits it, by Sec-Fetch-Site.
func SameOrigin(c OriginCheck) bool {
	switch c.Method {
	case "GET", "HEAD", "OPTIONS":
		return true
	}
	if c.Origin == "" {
		return c.FetchSite == "same-origin"
	}
	return OriginMatches(c.Origin, c.Host, c.Scheme)
}

// OriginMatches reports whether an Origin header names the host the request
// was sent to, and the scheme too when scheme is set. A malformed or
// opaque ("null") origin never matches.
func OriginMatches(origin, host, scheme string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.RawQuery != "" {
		return false
	}
	if scheme != "" && u.Scheme != scheme {
		return false
	}
	return u.Host == host
}
