// SPDX-License-Identifier: MIT

package domain

import "net/url"

// OriginCheck is what a browser tells us about where an unsafe request
// came from.
type OriginCheck struct {
	Method string
	// Origin is the Origin header.
	Origin string
	// Scheme, when set, is the scheme the Origin must have too (the
	// configured public URL's).
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
	u, err := url.Parse(c.Origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.RawQuery != "" {
		return false
	}
	if c.Scheme != "" && u.Scheme != c.Scheme {
		return false
	}
	return u.Host == c.Host
}
