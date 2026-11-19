// SPDX-License-Identifier: MIT

package conformance

// WelcomeSpec is the content of the welcome the suite sends. DefaultWelcome
// gives the founder agent of the PROTOCOL.md transcript.
type WelcomeSpec struct {
	ProtocolVersion     string
	AgentName           string
	OneLine             string
	Charter             string
	Goals               []string
	MCPURL              string
	MCPToken            string
	ModelBaseURL        string
	ModelToken          string
	Summary             *string
	PausedTurnID        *string
	HeartbeatIntervalMs int
	HeartbeatTimeoutMs  int
	MaxLineBytes        int
	RunnerSettings      map[string]any
}

// DefaultWelcome is the welcome of the transcript, with a heartbeat interval
// of 5 minutes so that heartbeats stay out of the way of most rules.
func DefaultWelcome() WelcomeSpec {
	return WelcomeSpec{ //nolint:gosec // G101: these are the made-up tokens of the PROTOCOL.md transcript
		ProtocolVersion:     "1.0",
		AgentName:           "founder",
		OneLine:             "Goal: keep this machine useful to its humans. Now: starting up.",
		Charter:             "I found and grow the society on this machine.",
		Goals:               []string{"Keep the machine healthy", "Hire agents for the work humans ask for"},
		MCPURL:              "http://127.0.0.1:7700/mcp",
		MCPToken:            "test-mcp-token-founder-0001",
		ModelBaseURL:        "http://edge:4000/v1",
		ModelToken:          "test-gateway-token-founder-0001",
		HeartbeatIntervalMs: 300000,
		HeartbeatTimeoutMs:  900000,
		MaxLineBytes:        1048576,
		RunnerSettings:      map[string]any{},
	}
}

// result renders the welcome as the result of hello, in transcript field order.
func (w WelcomeSpec) result() any {
	type agent struct {
		Name    string   `json:"name"`
		Kind    string   `json:"kind"`
		IsRoot  bool     `json:"isRoot"`
		UID     int      `json:"uid"`
		Team    string   `json:"team"`
		Manager *string  `json:"manager"`
		OneLine string   `json:"oneLine"`
		Charter string   `json:"charter"`
		Goals   []string `json:"goals"`
	}
	type mcp struct {
		Transport      string `json:"transport"`
		URL            string `json:"url"`
		Token          string `json:"token"`
		TokenExpiresAt string `json:"tokenExpiresAt"`
	}
	type model struct {
		BaseURL       string   `json:"baseUrl"`
		Name          string   `json:"name"`
		Token         string   `json:"token"`
		Weights       []string `json:"weights"`
		DefaultWeight string   `json:"defaultWeight"`
	}
	type session struct {
		Summary      *string `json:"summary"`
		CompactedAt  *string `json:"compactedAt"`
		PausedTurnID *string `json:"pausedTurnId"`
	}
	type config struct {
		HeartbeatIntervalMs int            `json:"heartbeatIntervalMs"`
		HeartbeatTimeoutMs  int            `json:"heartbeatTimeoutMs"`
		MaxLineBytes        int            `json:"maxLineBytes"`
		Runner              map[string]any `json:"runner"`
	}
	var compacted *string
	if w.Summary != nil {
		at := "2026-11-19T18:00:00Z"
		compacted = &at
	}
	return struct {
		ProtocolVersion string  `json:"protocolVersion"`
		Agent           agent   `json:"agent"`
		MCP             mcp     `json:"mcp"`
		Model           model   `json:"model"`
		Session         session `json:"session"`
		Config          config  `json:"config"`
	}{
		ProtocolVersion: w.ProtocolVersion,
		Agent: agent{Name: w.AgentName, Kind: "always-on", IsRoot: true, UID: 0, Team: w.AgentName,
			OneLine: w.OneLine, Charter: w.Charter, Goals: w.Goals},
		MCP: mcp{Transport: "streamable-http", URL: w.MCPURL, Token: w.MCPToken, TokenExpiresAt: "2026-11-20T19:00:00Z"}, //nolint:gosec // G101: the token is a test value from the spec
		Model: model{BaseURL: w.ModelBaseURL, Name: "autoagents/one", Token: w.ModelToken,
			Weights: []string{"quick", "normal", "deep"}, DefaultWeight: "normal"},
		Session: session{Summary: w.Summary, CompactedAt: compacted, PausedTurnID: w.PausedTurnID},
		Config: config{HeartbeatIntervalMs: w.HeartbeatIntervalMs, HeartbeatTimeoutMs: w.HeartbeatTimeoutMs,
			MaxLineBytes: w.MaxLineBytes, Runner: w.RunnerSettings},
	}
}
