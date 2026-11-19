// SPDX-License-Identifier: MIT

package domain

import (
	"strings"
	"unicode"
)

// Exclusion says why an operation is not offered as a tool or a command.
// The empty value means it is offered.
type Exclusion string

// Exclusion reasons. They are rules about the contract, not a list of operations.
const (
	NotExcluded Exclusion = ""
	// ExcludedProtocol is the mcp tag: the transport itself is not a tool.
	ExcludedProtocol Exclusion = "mcp-protocol"
	// ExcludedHostDoor is the host tag: reachable only on the vsock door.
	ExcludedHostDoor Exclusion = "host-door"
	// ExcludedStream is a server-sent event stream; a tool call cannot follow one.
	ExcludedStream Exclusion = "stream"
	// ExcludedWebSocket is an upgrade to a WebSocket, such as a terminal.
	ExcludedWebSocket Exclusion = "websocket"
)

// Tags with special meaning.
const (
	TagMCP  = "mcp"
	TagHost = "host"
)

// ExcludeFromTools reports why op is not an MCP tool, or NotExcluded.
func ExcludeFromTools(op Operation) Exclusion {
	switch {
	case op.Tag == TagMCP:
		return ExcludedProtocol
	case op.Tag == TagHost:
		return ExcludedHostDoor
	case op.Stream:
		return ExcludedStream
	case op.WebSocket:
		return ExcludedWebSocket
	}
	return NotExcluded
}

// ExcludeFromCommands reports why op is not an agentctl command, or
// NotExcluded. Streams are commands: they follow the stream.
func ExcludeFromCommands(op Operation) Exclusion {
	switch {
	case op.Tag == TagMCP:
		return ExcludedProtocol
	case op.Tag == TagHost:
		return ExcludedHostDoor
	case op.WebSocket:
		return ExcludedWebSocket
	}
	return NotExcluded
}

// ToolName is the MCP tool name for an operation: its operationId in snake_case.
func ToolName(operationID string) string {
	return strings.Join(splitWords(operationID), "_")
}

// splitWords cuts a camelCase identifier into lower-case words. A run of
// capitals stays together ("APIEvents" is "api", "events"), and digits stick
// to the word before them.
func splitWords(id string) []string {
	runes := []rune(id)
	var words []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		boundary := false
		switch {
		case unicode.IsUpper(cur) && (unicode.IsLower(prev) || unicode.IsDigit(prev)):
			boundary = true
		case unicode.IsUpper(cur) && unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1]):
			boundary = true
		}
		if boundary {
			words = append(words, strings.ToLower(string(runes[start:i])))
			start = i
		}
	}
	return append(words, strings.ToLower(string(runes[start:])))
}
