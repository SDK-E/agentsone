// SPDX-License-Identifier: MIT

// Package auth is the identity context: human accounts, sign-in sessions,
// tokens and first-boot setup. It answers who a caller is; internal/api
// turns that answer into a core.Caller on every request.
//
// Owned by work package 2 (API and sessions).
// Shape: domain/, app/, adapters/{http,sqlite}/, wired in module.go. Copy
// internal/audit, the reference context.
package auth
