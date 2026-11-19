// SPDX-License-Identifier: MIT

// Package audit is the edge audit sink: it receives the machine's hash-chained records as written,
// verifies them with internal/auditchain, and keeps what the edge itself saw. It provides the
// audit command.
//
// Owned by work package 16 (edge: gateway, budget, audit sink, web door).
package audit
