// SPDX-License-Identifier: MIT

// Package cli is agentctl: one command per API operation, grouped by tag, all
// derived from the embedded contract through the mcp catalog. It sends the
// requests to the daemon over the Unix socket (or a TCP address with a
// token), prints tables or raw JSON, maps problems to exit codes, follows
// event streams, and bridges stdio to the daemon's /v1/mcp for MCP clients
// that only speak stdio.
package cli
