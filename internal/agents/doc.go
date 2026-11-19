// SPDX-License-Identifier: MIT

// Package agents is the society context: root and custom agents, the org
// chart and teams, charters and goals, and each agent's budget allocation.
// It runs one runner per agent through agent@<name>.service and owns the
// Agents, Waker and Budget ports.
//
// Owned by work package 1 (daemon core).
// Shape: domain/, app/, adapters/{http,sqlite}/, wired in module.go. Copy
// internal/audit, the reference context.
package agents
