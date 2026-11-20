// SPDX-License-Identifier: MIT

// Package app runs one runner per agent: it provisions the agent's account
// and unit, starts the runner when the agent is woken, speaks the daemon side
// of the runner protocol through a RunnerConn, puts sleeping runners to rest
// so they cost nothing, holds the freeze levers, and restarts runners whose
// heartbeat stops.
package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/gatewaytoken"
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
	// EnsureRootHome makes a root agent's 0700 home, owned by root, when
	// missing. Root agents have no user of their own.
	EnsureRootHome(ctx context.Context, a domain.Account) error
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

// AgentStates hears when an agent's runner cannot run and when it runs
// again (the society context, internal/agents/app), so the agent's status
// can say why it stopped.
type AgentStates interface {
	// RunnerFailed says the runner cannot start or stopped with an error.
	// reason is one plain sentence for a human, never an error's detail.
	RunnerFailed(ctx context.Context, agent, reason string) error
	// RunnerRunning says the runner finished its handshake and is ready.
	RunnerRunning(ctx context.Context, agent string) error
	// RunnerCleared says what kept the runner from running is gone (the
	// network wall holds again), though the runner is not running yet.
	RunnerCleared(ctx context.Context, agent string) error
}

// RunState is what an agent's runner is doing, as the society shows it.
type RunState string

// Run states.
const (
	// RunStopped: no runner. The agent shows as sleeping.
	RunStopped RunState = "stopped"
	// RunStarting: the runner was started and has not finished its handshake.
	RunStarting RunState = "starting"
	// RunIdle: the runner is connected and waits for a wake.
	RunIdle RunState = "idle"
	// RunWorking: a turn is running.
	RunWorking RunState = "working"
)

// RunObserver hears what each agent's runner is doing and when a turn ends
// (the society context, internal/agents/app), so the agent's record shows
// its real state and its last turn.
type RunObserver interface {
	// RunnerState says what agent's runner is doing now.
	RunnerState(ctx context.Context, agent string, state RunState) error
	// TurnEnded says a turn of agent ended at at.
	TurnEnded(ctx context.Context, agent string, at time.Time) error
}

// NetworkWall checks the machine's network wall: no route out and one
// network only, so the edge is the only way out (adapters/netwall reads
// /proc).
type NetworkWall interface {
	Check(ctx context.Context) domain.NetWall
}

// RunnerConn is the daemon's half of one runner connection.
type RunnerConn interface {
	// Call refuses until the runner's first status has been accepted (rule H8).
	Call(ctx context.Context, method string, params, result any) error
	// WaitReady returns nil once Call no longer refuses for that reason. The
	// connection marks itself ready only after Handler.Status returns, so a
	// call started from Status, or because the session is already idle,
	// waits here first. It returns an error when the connection ends or ctx is done.
	WaitReady(ctx context.Context) error
	Close() error
	// Notify sends a notification; it fails before the runner's first status.
	Notify(ctx context.Context, method string, params any) error
}

// Config holds the supervisor's settings.
type Config struct {
	// MCPURL is the machine MCP endpoint runners reach.
	MCPURL string
	// MCPTokenTTL is how long a welcome's MCP token lives.
	MCPTokenTTL time.Duration
	// GatewayURL is the model gateway base URL.
	GatewayURL string
	// GatewayTokenTTL is how long a gateway token lives; the gateway
	// refuses more than 15 minutes.
	GatewayTokenTTL time.Duration
	// HeartbeatInterval and HeartbeatTimeout go into the welcome config.
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration
	MaxLineBytes      int
	MaxSummaryChars   int
	ShutdownGrace     time.Duration
	// WallRecheck is how often Tick checks the network wall again; zero
	// means 5 minutes.
	WallRecheck time.Duration
}

// DefaultConfig follows PROTOCOL.md defaults.
func DefaultConfig() Config {
	return Config{
		MCPURL:            "http://127.0.0.1:7700" + core.MCPPath,
		MCPTokenTTL:       24 * time.Hour,
		GatewayURL:        "http://gateway.agentsone.internal:4000" + gatewaytoken.BasePath,
		GatewayTokenTTL:   10 * time.Minute,
		HeartbeatInterval: 15 * time.Second,
		HeartbeatTimeout:  45 * time.Second,
		MaxLineBytes:      1 << 20,
		MaxSummaryChars:   4096,
		ShutdownGrace:     5 * time.Second,
		WallRecheck:       defaultWallRecheck,
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
	Config    Config
	// States hears runner failures and recoveries; nil hears nothing.
	States AgentStates
	// Runs hears what runners do and when turns end; nil hears nothing.
	Runs RunObserver
	// Wall checks the network wall. Nil cannot prove it, so no agent wakes.
	Wall NetworkWall
	// Log takes the supervisor's lines (the network wall, the credential
	// renewals); nil means slog.Default().
	Log *slog.Logger
}
