// SPDX-License-Identifier: MIT

package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Msg is one line from the runner, parsed.
type Msg struct {
	Raw    []byte // the line including its newline
	Method string // set for requests and notifications
	ID     *int   // set for requests and responses; nil for notifications and id:null errors
	// HasNullID is true for an error response with id null.
	HasNullID bool
	Params    json.RawMessage
	Result    json.RawMessage
	Error     *ErrorObject
}

// ErrorObject is a JSON-RPC error.
type ErrorObject struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// IsRequest reports a runner request (method and id).
func (m Msg) IsRequest() bool { return m.Method != "" && m.ID != nil }

// IsNotification reports a notification (method, no id).
func (m Msg) IsNotification() bool { return m.Method != "" && m.ID == nil }

// IsResponse reports a success or error response.
func (m Msg) IsResponse() bool { return m.Method == "" }

// Is reports whether m is the notification or request with this method.
func (m Msg) Is(method string) bool { return m.Method == method }

// Field reads one top-level field of the params as a string ("" when absent).
func (m Msg) Field(name string) string {
	var p map[string]json.RawMessage
	if json.Unmarshal(m.Params, &p) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(p[name], &s) != nil {
		return string(bytes.TrimSpace(p[name]))
	}
	return s
}

// String is the line without its newline, for failure messages.
func (m Msg) String() string { return string(bytes.TrimRight(m.Raw, "\n")) }

func parseMsg(raw []byte) (Msg, error) {
	m := Msg{Raw: raw}
	var obj struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *ErrorObject    `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &obj); err != nil {
		return m, fmt.Errorf("line is not a JSON object: %w", err)
	}
	m.Method, m.Params, m.Result, m.Error = obj.Method, obj.Params, obj.Result, obj.Error
	switch {
	case obj.ID == nil:
	case string(obj.ID) == "null":
		m.HasNullID = true
	default:
		var id int
		if err := json.Unmarshal(obj.ID, &id); err != nil {
			return m, fmt.Errorf("id is not an integer: %w", err)
		}
		m.ID = &id
	}
	return m, nil
}
