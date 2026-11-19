// SPDX-License-Identifier: MIT

// Package httpadapter is the machine MCP's HTTP side: the Streamable HTTP
// MCP server at /v1/mcp (built on the official Go SDK), and the in-process
// dispatcher that runs each tool call through the daemon's own API handler
// as the MCP session's caller.
package httpadapter
