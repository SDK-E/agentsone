// SPDX-License-Identifier: MIT

// Package app runs one runner per agent: it provisions the agent's account
// and unit, starts the runner when the agent is woken, speaks the daemon side
// of the runner protocol through a RunnerConn, puts sleeping runners to rest
// so they cost nothing, holds the freeze levers, and restarts runners whose
// heartbeat stops.
package app

import (
	"context"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// Launcher runs runner processes. The process adapter runs them as child
// processes of the daemon; another host could plug in a service manager.
type Launcher interface {
	// Install writes how the agent's runner starts. It does not start it.
	Install(ctx context.Context, spec domain.UnitSpec) error
	Start(ctx context.Context, agent string) error
	Stop(ctx context.Context, agent string) error
	Restart(ctx context.Context, agent string) error
	// Remove deletes what Install wrote. The runner must be stopped.
	Remove(ctx context.Context, agent string) error
	// Mode says whether the agent's limits hold hard (cgroup), soft
	// (process resource limits only) or not at all (root agents).
	Mode(agent string) domain.LimitMode
}

// Accounts manages the Linux users of custom agents.
type Accounts interface {
	// Ensure creates the user, its group membership and its 0700 home when
	// missing, and returns the uid. With a.UID > 0 the user gets exactly that
	// uid; an existing user with that name and uid is not an error.
	Ensure(ctx context.Context, a domain.Account) (uid int, err error)
	// Lock disables the user without deleting it, so its uid is never reused
	// for another agent while the archived home still holds its files.
	Lock(ctx context.Context, a domain.Account) error
}

// AgentIdentity is who the agent is, for the welcome.
type AgentIdentity struct {
	Agent   core.Agent
	Team    string
	Charter string
	OneLine string
	Goals   []string
}

// Identity is implemented by the society context (internal/agents/app).
type Identity interface {
	Identity(ctx context.Context, agent string) (AgentIdentity, error)
}

// OutputChunk is one output notification of a turn.
type OutputChunk struct {
	TurnID string
	ChatID string // empty for non-chat turns
	Index  int
	Text   string
	Final  bool
}

// OutputSink receives turn and chat text (chat store and stream).
type OutputSink interface {
	Output(ctx context.Context, agent string, c OutputChunk) error
}

// Summary is an agent's compacted session.
type Summary struct {
	Text         string
	CompactedAt  time.Time
	PausedTurnID string
}

// SummaryStore keeps the compacted session between runner processes.
type SummaryStore interface {
	// Load returns the stored summary, or the zero Summary when there is none.
	Load(ctx context.Context, agent string) (Summary, error)
	Save(ctx context.Context, agent string, s Summary) error
}

// Usage is one usage notification.
type Usage struct {
	TurnID          string
	Model           string
	Weight          string
	TokensIn        int
	TokensOut       int
	CacheReadTokens int
	CostMicroUSD    int
}

// UsageRecorder feeds the soft per-agent budget.
type UsageRecorder interface {
	RecordUsage(ctx context.Context, agent string, u Usage) error
}

// GatewayTokens issues the agent's model gateway token (internal/gatewaytoken).
type GatewayTokens interface {
	GatewayToken(ctx context.Context, agent string) (string, error)
}

// RunnerConn is the daemon's half of one runner connection.
type RunnerConn interface {
	Call(ctx context.Context, method string, params, result any) error
	Close() error
}

// Config holds the supervisor's settings.
type Config struct {
	// MCPURL is the machine MCP endpoint runners reach.
	MCPURL string
	// MCPTokenTTL is how long a welcome's MCP token lives.
	MCPTokenTTL time.Duration
	// GatewayURL is the model gateway base URL.
	GatewayURL string
	// HeartbeatInterval and HeartbeatTimeout go into the welcome config.
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration
	MaxLineBytes      int
	MaxSummaryChars   int
	ShutdownGrace     time.Duration
}

// DefaultConfig follows PROTOCOL.md defaults.
func DefaultConfig() Config {
	return Config{
		MCPURL:            "http://127.0.0.1:7700/mcp",
		MCPTokenTTL:       24 * time.Hour,
		GatewayURL:        "http://gateway.agentsone.internal:4000",
		HeartbeatInterval: 15 * time.Second,
		HeartbeatTimeout:  45 * time.Second,
		MaxLineBytes:      1 << 20,
		MaxSummaryChars:   4096,
		ShutdownGrace:     5 * time.Second,
	}
}

// Deps are the supervisor's collaborators. Ports is read when used, so it
// may be filled after construction.
type Deps struct {
	Clock     core.Clock
	IDs       core.IDs
	Ports     *core.Ports
	Launcher  Launcher
	Accounts  Accounts
	Identity  Identity
	Output    OutputSink
	Summaries SummaryStore
	Usage     UsageRecorder
	Gateway   GatewayTokens
	Config    Config
}
