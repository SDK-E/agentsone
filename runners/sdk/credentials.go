// SPDX-License-Identifier: MIT

package sdk

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Credentials hold the latest scoped tokens of the agent: the model gateway
// token and the MCP token. The SDK fills them from the welcome. On a
// connection that negotiated protocol 1.1 it swaps both at once on every valid
// credentials.update, while turns keep running (rule K2).
//
// Read a token right before each new model call or MCP request, and do not
// keep a copy: a call already in flight finishes with the token it read, and
// every later call gets the new one. Credentials are safe for concurrent use.
//
// The tokens are secrets. They come back as Secret, and Credentials itself
// prints as "[redacted]".
type Credentials struct {
	cur atomic.Pointer[tokenPair]
}

type tokenPair struct {
	gateway Secret
	mcp     Secret
}

// GatewayToken is the latest model gateway token.
func (c *Credentials) GatewayToken() Secret { return c.load().gateway }

// MCPToken is the latest MCP token: the bearer token of a streamable-http
// endpoint, or the AGENTSONE_TOKEN value for a stdio one. A stdio MCP server
// reads its token at start, so restart it between turns or tool calls when
// this value changed.
func (c *Credentials) MCPToken() Secret { return c.load().mcp }

// String hides the tokens.
func (*Credentials) String() string { return "[redacted]" }

// GoString hides the tokens in %#v.
func (*Credentials) GoString() string { return "[redacted]" }

func (c *Credentials) load() tokenPair {
	if c == nil {
		return tokenPair{}
	}
	if p := c.cur.Load(); p != nil {
		return *p
	}
	return tokenPair{}
}

// set replaces both tokens in one step, so no reader sees half an update.
func (c *Credentials) set(gateway, mcp Secret) {
	c.cur.Store(&tokenPair{gateway: gateway, mcp: mcp})
}

var (
	// scopedToken is the pattern of ScopedToken in messages.schema.json; its
	// length, 16 to 4096, is checked apart (regexp repeats stop at 1000).
	scopedToken = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]+$`)
	// timestamp is Timestamp of common.schema.json: RFC 3339 in UTC with Z.
	timestamp = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$`)

	credentialFields = []string{"gatewayToken", "gatewayTokenExpiresAt", "mcpToken", "mcpTokenExpiresAt"}
)

// parseCredentials checks credentials.update params and returns the two
// tokens. A token expires at or before now is refused. The params hold
// secrets, so an error names fields only and never quotes a value or a key
// from the wire.
func parseCredentials(raw json.RawMessage, now time.Time) (gateway, mcp Secret, err error) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return "", "", errors.New("params are not an object")
	}
	for k := range obj {
		if !slices.Contains(credentialFields, k) {
			return "", "", errors.New("params have a field that is not in the protocol")
		}
	}
	values := make(map[string]string, len(credentialFields))
	for _, name := range credentialFields {
		v, ok := obj[name]
		if !ok {
			return "", "", errors.New(name + " is missing")
		}
		var s string
		if json.Unmarshal(v, &s) != nil {
			return "", "", errors.New(name + " is not a string")
		}
		values[name] = s
	}
	for _, name := range []string{"gatewayToken", "mcpToken"} {
		v := values[name]
		if len(v) < 16 || len(v) > 4096 || !scopedToken.MatchString(v) {
			return "", "", errors.New(name + " is not a scoped token")
		}
	}
	for _, name := range []string{"gatewayTokenExpiresAt", "mcpTokenExpiresAt"} {
		at, perr := time.Parse(time.RFC3339Nano, values[name])
		if !timestamp.MatchString(values[name]) || perr != nil {
			return "", "", errors.New(name + " is not an RFC 3339 time in UTC")
		}
		if !at.After(now) {
			return "", "", errors.New(name + " is not in the future")
		}
	}
	return Secret(values["gatewayToken"]), Secret(values["mcpToken"]), nil
}

// renewsCredentials reports whether a protocol version has credentials.update:
// 1.1 and every later 1.x.
func renewsCredentials(version string) bool {
	major, minor, ok := strings.Cut(version, ".")
	if !ok {
		return false
	}
	a, err1 := strconv.Atoi(major)
	b, err2 := strconv.Atoi(minor)
	return err1 == nil && err2 == nil && a == 1 && b >= 1
}
