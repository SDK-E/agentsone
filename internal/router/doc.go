// SPDX-License-Identifier: MIT

// Package router is the human entry: it keeps the live list of one-lines
// and assigns a human request to the best agent's chat, possibly through a
// more specialised router. It owns the Live port.
//
// Owned by work package 4 (router and live list).
// Shape: domain/, app/, adapters/{http,sqlite}/, wired in module.go. Copy
// internal/audit, the reference context.
package router
