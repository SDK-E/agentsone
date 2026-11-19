// SPDX-License-Identifier: MIT

// Package net is the edge internet filter. The machine container sits on an inner network with no
// route out; its DNS points at the edge. The edge answers allowed names with its own inner
// address, refuses every other name, and proxies the machine's connections (HTTP by Host header,
// TLS by server name, and CONNECT on port 3128) only to addresses it resolved itself for an allowed
// name. One switch, the freeze, cuts all egress. It provides the net command for the allow list.
//
// Every listener binds the inner address only, so the proxies are never open on the outer network.
//
// Owned by work package 15 (edge: network, freeze).
package net
