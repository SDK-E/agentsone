// SPDX-License-Identifier: MIT

// Package domain holds the rules of the edge internet filter, with no input or output: which
// names may be reached (the allow list), which addresses the edge resolved for them and until
// when (the ledger), how DNS queries and egress requests are decided (the policy), and how many
// records reach the audit sink (the audit limiter).
package domain
