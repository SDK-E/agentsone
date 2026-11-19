// SPDX-License-Identifier: MIT

// Package api is the API layer: it composes every module's groups and
// routes into one handler, answers 501 for operations no module serves,
// sets the caller on each request (socket peer credentials and cgroup,
// session cookie or token), and serves the host tag the host tool uses
// through the door.
//
// Owned by work package 2 (API and sessions); compose.go by work package 0.
// Shape: domain/, app/, adapters/{http,sqlite}/, wired in module.go. Copy
// internal/audit, the reference context.
package api
