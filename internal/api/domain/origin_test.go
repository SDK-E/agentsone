// SPDX-License-Identifier: MIT

package domain_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/api/domain"
)

func TestSameOrigin_GuardsUnsafeCookieRequests(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		req     domain.OriginCheck
		allowed bool
	}{
		{"safe method needs nothing", domain.OriginCheck{Method: "GET", Host: "127.0.0.1:7700"}, true},
		{"head is safe", domain.OriginCheck{Method: "HEAD", Host: "127.0.0.1:7700"}, true},
		{"same origin post", domain.OriginCheck{Method: "POST", Origin: "http://127.0.0.1:7700", Host: "127.0.0.1:7700"}, true},
		{"same origin over https door", domain.OriginCheck{Method: "DELETE", Origin: "https://box.local", Host: "box.local"}, true},
		{"cross origin post", domain.OriginCheck{Method: "POST", Origin: "http://evil.example", Host: "127.0.0.1:7700"}, false},
		{"null origin", domain.OriginCheck{Method: "POST", Origin: "null", Host: "127.0.0.1:7700"}, false},
		{"origin with a path is malformed", domain.OriginCheck{Method: "POST", Origin: "http://127.0.0.1:7700/x", Host: "127.0.0.1:7700"}, false},
		{"missing origin, fetch metadata same-origin", domain.OriginCheck{Method: "PATCH", FetchSite: "same-origin", Host: "127.0.0.1:7700"}, true},
		{"missing origin, cross-site fetch", domain.OriginCheck{Method: "PUT", FetchSite: "cross-site", Host: "127.0.0.1:7700"}, false},
		{"missing origin and metadata", domain.OriginCheck{Method: "POST", Host: "127.0.0.1:7700"}, false},
		{"public URL scheme matches", domain.OriginCheck{Method: "POST", Origin: "https://box.example", Host: "box.example", Scheme: "https"}, true},
		{"public URL scheme differs", domain.OriginCheck{Method: "POST", Origin: "http://box.example", Host: "box.example", Scheme: "https"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := domain.SameOrigin(c.req); got != c.allowed {
				t.Fatalf("SameOrigin(%+v) = %v, want %v", c.req, got, c.allowed)
			}
		})
	}
}
