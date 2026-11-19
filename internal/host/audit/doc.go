// SPDX-License-Identifier: MIT

// Package audit is the host audit sink: it receives the VM's hash-chained
// records as written, verifies them with internal/auditchain, and keeps what
// the host itself saw. It provides the audit command.
//
// Owned by work package 16 (host: gateway, budget, audit sink, web door).
package audit
