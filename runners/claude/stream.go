// SPDX-License-Identifier: MIT

package claude

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
)

// The engine speaks stream-json: one JSON object per line on stdin and
// stdout (Claude Code headless mode, --input-format and --output-format
// stream-json). Only the fields the runner uses are decoded; unknown types
// and fields are ignored, so a newer engine keeps working.

// engineLine is one stdout line of the engine.
type engineLine struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`

	// system/init
	MCPServers   []mcpServer `json:"mcp_servers"`
	Capabilities []string    `json:"capabilities"`

	// system/api_retry
	Attempt     int `json:"attempt"`
	ErrorStatus int `json:"error_status"`

	// system/api_retry (a message) and assistant (a category such as
	// billing_error); kept raw because its shape differs by type.
	Error json.RawMessage `json:"error"`

	// stream_event: a raw Messages API stream event.
	Event json.RawMessage `json:"event"`

	// assistant and user
	Message *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`

	// result
	IsError        bool   `json:"is_error"`
	APIErrorStatus int    `json:"api_error_status"`
	Result         string `json:"result"`
	Usage          *struct {
		InputTokens     int  `json:"input_tokens"`
		OutputTokens    int  `json:"output_tokens"`
		CacheReadTokens *int `json:"cache_read_input_tokens"`
	} `json:"usage"`
	ModelUsage map[string]json.RawMessage `json:"modelUsage"`
}

// errorText is the error field as text: a string as it is, anything else
// as its JSON.
func (l engineLine) errorText() string {
	if len(l.Error) == 0 || string(l.Error) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(l.Error, &s) == nil {
		return s
	}
	return string(l.Error)
}

// machineConnected says whether system/init lists the machine MCP server as
// connected.
func (l engineLine) machineConnected() bool {
	return slices.ContainsFunc(l.MCPServers, func(s mcpServer) bool { return s.Name == "machine" && s.Status == "connected" })
}

// mcpServer is one entry of system/init's mcp_servers.
type mcpServer struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// textDelta is the text of a stream_event that carries a text delta.
func (l engineLine) textDelta() (string, bool) {
	var ev struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	}
	if json.Unmarshal(l.Event, &ev) != nil || ev.Type != "content_block_delta" || ev.Delta.Type != "text_delta" {
		return "", false
	}
	return ev.Delta.Text, true
}

// toolUses are the tool names an assistant message calls.
func (l engineLine) toolUses() []string {
	if l.Message == nil {
		return nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return nil
	}
	var names []string
	for _, b := range blocks {
		if b.Type == "tool_use" && b.Name != "" {
			names = append(names, b.Name)
		}
	}
	return names
}

// model is the model that served the result: the first modelUsage key.
func (l engineLine) model() string {
	keys := make([]string, 0, len(l.ModelUsage))
	for k := range l.ModelUsage {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

// userLine is a user message for the engine's stdin.
func userLine(text string) []byte {
	b, _ := json.Marshal(map[string]any{ // strings and nil always marshal
		"type":               "user",
		"message":            map[string]any{"role": "user", "content": text},
		"parent_tool_use_id": nil,
		"uuid":               newUUID(),
	})
	return b
}

// interruptLine asks the engine to stop the running turn. It answers with a
// control_response and ends the turn with a result.
func interruptLine(requestID string) []byte {
	b, _ := json.Marshal(map[string]any{ // strings always marshal
		"type":       "control_request",
		"request_id": requestID,
		"request":    map[string]any{"subtype": "interrupt"},
	})
	return b
}

// newUUID is a random (version 4) UUID.
func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never fails; it crashes the program instead
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return strings.Join([]string{h[0:8], h[8:12], h[12:16], h[16:20], h[20:32]}, "-")
}
