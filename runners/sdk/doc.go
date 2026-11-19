// SPDX-License-Identifier: MIT

// Package sdk is the library every Agents One runner is built on.
//
// A runner is the program that thinks for one agent. The daemon starts one
// runner process per agent and talks to it over the runner protocol (see
// contract/schemas/runner/PROTOCOL.md): JSON-RPC 2.0, one message per line,
// over a Unix socket. This package owns everything that is the same for all
// runners, so a runner only writes the part that is its own:
//
//   - connecting to AGENTSONE_RUNNER_SOCKET and the line framing with its size
//     limit (rules F1 to F8),
//   - the hello and welcome handshake and version negotiation (H1, H2, H7),
//   - request and response id handling in both directions (F5, F6),
//   - the turn life cycle: acknowledging wake, working and idle status, one turn
//     at a time, queued wakes (T1 to T8),
//   - cancel, sleep and shutdown (C1, C2, S1, X1, X2),
//   - the heartbeat loop (B1),
//   - pausing a turn for budget and resuming it (R1 to R3),
//   - token renewal: on protocol 1.1 the daemon's credentials.update swaps
//     both tokens in Welcome.Credentials while turns run (K2); a 1.0
//     connection keeps the welcome's tokens (K3).
//
// A runner takes its gateway and MCP tokens from Welcome.Credentials right
// before each call, never from Welcome.Model.Token or Welcome.MCP.Token, so a
// renewed token serves the next call without a restart.
//
// A runner implements Handler and, if it can resume a paused turn, Resumer.
// Inside a turn it talks to the daemon only through *Turn, which stamps every
// message with the turn id and keeps the rules (output chunk numbers, the
// final chunk, the one-line profile) so a handler cannot break them by
// accident.
//
// The reusable conformance suite is in package runners/conformance.
package sdk
