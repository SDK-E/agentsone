// SPDX-License-Identifier: MIT

// Package plugins is the plugin context: installing plugins from the three
// sources (Claude Code format, own format, Agent Skills and the MCP
// registry) and the kinds they carry: MCP servers, skills, agent templates,
// runners, interface panels and themes. It owns the Runners port.
//
// Owned by work package 6 (plugin manager and source adapters).
// Shape: domain/, app/, adapters/{http,sqlite}/, wired in module.go. Copy
// internal/audit, the reference context.
package plugins
