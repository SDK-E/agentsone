// SPDX-License-Identifier: MIT

// Package domain holds the web door's header rules: what it never passes on to the machine and
// what it says about the client instead.
//
// The daemon trusts X-Forwarded-For and X-Forwarded-Proto only from the edge's exact inner
// address, so these two are the door's own word. Everything a client or the platform in front
// says about forwarding or identity is removed: a human's identity is never asserted by a header,
// the daemon's own session decides who is calling.
package domain

import (
	"net/http"
	"net/netip"
	"strings"
)

// strippedExact are removed by exact (canonical) name.
var strippedExact = map[string]bool{
	"Forwarded":                true,
	"X-Real-Ip":                true,
	"X-Client-Ip":              true,
	"X-Cluster-Client-Ip":      true,
	"True-Client-Ip":           true,
	"Cf-Connecting-Ip":         true,
	"Fastly-Client-Ip":         true,
	"X-Original-Forwarded-For": true,
}

// strippedPrefixes are removed by (canonical) name prefix: forwarding and identity families.
var strippedPrefixes = []string{
	"X-Forwarded-",
	"X-Remote-",
	"Remote-",
	"X-Auth-",
	"X-Authenticated-",
	"X-Webauth-",
	"X-Ms-Client-Principal",
	"X-Original-",
}

// Stripped reports whether the door removes a header the client sent.
func Stripped(name string) bool {
	name = http.CanonicalHeaderKey(name)
	if strippedExact[name] {
		return true
	}
	for _, p := range strippedPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// Strip removes every stripped header from h.
func Strip(h http.Header) {
	for name := range h {
		if Stripped(name) {
			delete(h, name)
		}
	}
}

// Proto is the X-Forwarded-Proto the door sends: https when the client reached the door over TLS
// or when it says, with exactly one value, that it is on https; otherwise http. A false https can
// only make the daemon mark its cookies Secure, never the reverse, so the claim is safe to take
// in that one direction (the platform in front terminates TLS and says https).
func Proto(tls bool, claimed []string) string {
	if tls {
		return "https"
	}
	if len(claimed) == 1 && strings.EqualFold(strings.TrimSpace(claimed[0]), "https") {
		return "https"
	}
	return "http"
}

// ClientIP is the X-Forwarded-For the door sends: the address of the peer that connected to the
// door, and nothing it claimed. Empty when the address cannot be read.
func ClientIP(remoteAddr string) string {
	ap, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return ""
	}
	return ap.Addr().Unmap().String()
}
