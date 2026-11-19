// SPDX-License-Identifier: MIT

// Package core holds the frozen seams of the daemon: the Module interface every
// daemon package implements, the Deps every module receives, the small ports
// modules use to reach each other, caller identity, configuration, paths and
// RFC 9457 problem errors.
//
// Owned by work package 0 (contract). Frozen: see contract/FREEZE.md. Ask the
// orchestrator before changing anything here.
//
// Lifecycle run by cmd/agentsoned, in this order, one module at a time:
//
//  1. construct every module with New(deps)
//  2. Provide: each module puts the ports it owns into deps.Ports
//  3. Migrate: each module migrates its own tables
//  4. API: the API layer collects every module's groups and routes
//  5. Start, then Stop in reverse order on shutdown
//
// Modules must read ports from deps.Ports when they use them, not in New,
// because other modules fill them in step 2.
package core
