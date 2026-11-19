// SPDX-License-Identifier: MIT

// Package claude is the Claude runner. It drives one long-lived Claude Code
// engine per agent, in stream-json mode: a wake is a user message, the
// engine's text deltas stream out as output, and its result ends the turn
// with a usage report. Messages delivered during a turn go to the engine
// after its result, in the same turn. A cancel interrupts the engine and
// kills it when it does not stop in time; the next wake resumes the
// session. Sleep asks the engine for a summary and starts the next session
// with it in the system prompt.
//
// The engine starts on the first wake, not at Start, so a machine whose
// engine is not downloaded yet still runs the runner; each turn then fails
// with a plain licence message.
//
// The engine never holds the gateway token or the MCP token. It calls a
// loopback proxy with a per-process secret, and the proxy forwards each call
// with the agent's latest real token. This is a soft wall: see the proxy.
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
