// SPDX-License-Identifier: MIT

// Package claude holds the parts of the Claude runner that are built so far:
// the local token proxy and the engine command. It is a library only. No
// runner binary or catalog entry uses it yet; the runner itself (the engine
// process, the stream-json mapping and the conformance run) comes next.
//
// The engine never holds the gateway token or the MCP token. It calls a
// loopback proxy with a per-process secret, and the proxy forwards each call
// with the agent's latest real token.
//
// A custom agent's template sets extra permission rules in runner.config,
// in Claude Code permission rule syntax:
//
//	{"allowedTools": ["Bash(git *)", "Read"], "disallowedTools": ["Bash(rm *)"]}
//
// Both keys are optional and no other key is accepted. The machine MCP tools
// (mcp__machine__*) are always allowed and WebFetch is always denied. Root
// agents run with bypassPermissions in the machine container, which is their
// sandbox, and ignore the rules.
package claude
