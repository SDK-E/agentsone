// SPDX-License-Identifier: MIT

package domain_test

import (
	"net/http"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/door/domain"
)

func TestStripped_RemovesForwardingAndIdentityHeaders(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"Forwarded", "X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "X-Forwarded-Port",
		"X-Forwarded-User", "X-Forwarded-Email", "X-Forwarded-Groups", "X-Forwarded-Preferred-Username",
		"X-Forwarded-Access-Token", "X-Real-IP", "X-Remote-User", "Remote-User", "X-Auth-Request-User",
		"X-Auth-Request-Email", "X-Auth-User", "X-Authenticated-User", "X-Client-IP", "True-Client-IP",
		"CF-Connecting-IP", "X-Original-Forwarded-For", "x-forwarded-for", "X-Remote-Email",
		"Remote-Email", "Remote-Groups", "Remote-Name", "X-Webauth-User", "X-Ms-Client-Principal-Name",
	} {
		if !domain.Stripped(name) {
			t.Errorf("Stripped(%q) = false, want true", name)
		}
	}
}

func TestStripped_KeepsOrdinaryHeaders(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"Cookie", "Authorization", "Accept", "Content-Type", "Last-Event-ID", "Origin",
		"Sec-WebSocket-Key", "Sec-WebSocket-Protocol", "User-Agent", "X-Requested-With",
	} {
		if domain.Stripped(name) {
			t.Errorf("Stripped(%q) = true, want false", name)
		}
	}
}

func TestStrip_DeletesEveryStrippedHeader(t *testing.T) {
	t.Parallel()
	h := http.Header{}
	h.Set("X-Forwarded-For", "1.2.3.4")
	h.Set("Remote-User", "root")
	h.Set("Cookie", "s=1")
	domain.Strip(h)
	if len(h) != 1 || h.Get("Cookie") != "s=1" {
		t.Fatalf("after Strip = %v, want only Cookie", h)
	}
}

func TestProto_IsHTTPSOnTLS(t *testing.T) {
	t.Parallel()
	if got := domain.Proto(true, nil); got != "https" {
		t.Fatalf("Proto(tls) = %q, want https", got)
	}
}

func TestProto_UpgradesOnlyToHTTPS(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		claimed []string
		want    string
	}{
		{nil, "http"},
		{[]string{"https"}, "https"},
		{[]string{"HTTPS"}, "https"},
		{[]string{"http"}, "http"},
		{[]string{"https, http"}, "http"},
		{[]string{"https", "http"}, "http"},
		{[]string{"gopher"}, "http"},
	} {
		if got := domain.Proto(false, tc.claimed); got != tc.want {
			t.Errorf("Proto(false, %q) = %q, want %q", tc.claimed, got, tc.want)
		}
	}
}

func TestClientIP_TakesTheHostOfTheRemoteAddress(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"203.0.113.7:51000":   "203.0.113.7",
		"[2001:db8::1]:443":   "2001:db8::1",
		"not an address":      "",
		"[::ffff:10.0.0.1]:1": "10.0.0.1",
	} {
		if got := domain.ClientIP(in); got != want {
			t.Errorf("ClientIP(%q) = %q, want %q", in, got, want)
		}
	}
}
