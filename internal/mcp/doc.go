// SPDX-License-Identifier: MIT

// Package mcp is the machine MCP: the daemon API offered as MCP tools over
// the Unix socket and through agentctl mcp. It is the only connected MCP
// server on day one.
//
// Owned by work package 8 (machine MCP and agentctl).
// Shape: domain/, app/, adapters/{http,sqlite}/, wired in module.go. Copy
// internal/audit, the reference context.
package mcp
