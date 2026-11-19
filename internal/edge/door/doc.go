// SPDX-License-Identifier: MIT

// Package door is the web door, the humans' way in: a reverse proxy on the outer side of the
// edge to the machine's daemon (http://machine:7700). The platform in front adds TLS and any
// sign-in. It passes HTTP, event streams and WebSocket upgrades (terminals, the desktop VNC
// bridge).
//
// Guards:
//   - It never listens on the inner address the machine reaches, nor on every interface, and it
//     drops any connection that arrives on the inner address.
//   - It removes every forwarding and identity header a client or the platform sent (Forwarded,
//     X-Forwarded-*, X-Real-IP, Remote-User, X-Remote-*, X-Auth-* and the like) and sets only
//     X-Forwarded-For (the peer that connected) and X-Forwarded-Proto. It never sends a human's
//     identity: the daemon's own session decides who is calling.
//
// Shape: domain (header rules), adapters/listen (guarded listeners), adapters/proxy (the reverse
// proxy). component.go wires them as an edge component; `agentsone edge --door-addr` opens it.
//
// Owned by work package 16 (edge: gateway, budget, audit sink, web door).
package door
