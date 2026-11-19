// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Ports are the small interfaces modules use to reach each other. Each port
// is owned by one module, which sets it in Provide. Until then every port
// answers ErrUnavailable, so a daemon with stub modules still runs.
type Ports struct {
	Agents    AgentDirectory  // owned by internal/agents
	Waker     Waker           // owned by internal/agents
	Events    EventPublisher  // owned by internal/events
	Subscribe EventSubscriber // owned by internal/events
	Tasks     TaskService     // owned by internal/sched
	Audit     AuditLog        // owned by internal/audit
	Live      LiveList        // owned by internal/router
	Budget    BudgetView      // owned by internal/agents (reads the host gateway)
	Runners   RunnerRegistry  // owned by internal/plugins
	Bus       BusSender       // owned by internal/bus
	Freezer   Freezer         // owned by internal/agents
	Tokens    TokenIssuer     // owned by internal/auth
	Templates TemplateCatalog // owned by internal/plugins
	Profiles  AgentProfiles   // owned by internal/agents
	Chat      ChatPoster      // owned by internal/agents
}

// NewPorts returns Ports whose every field answers ErrUnavailable.
func NewPorts() *Ports {
	u := unavailable{}
	return &Ports{
		Agents: u, Waker: u, Events: u, Subscribe: u, Tasks: u,
		Audit: u, Live: u, Budget: u, Runners: u,
		Bus: u, Freezer: u, Tokens: u, Templates: u,
		Profiles: u, Chat: u,
	}
}

// AgentKind is how an agent lives, with the contract's values.
type AgentKind string

// Agent kinds.
const (
	AgentAlwaysOn AgentKind = "always-on"
	AgentChat     AgentKind = "chat"
	AgentWaker    AgentKind = "waker"
)

// Agent is the directory view of one agent.
type Agent struct {
	Name string
	Kind AgentKind
	// IsRoot is true for root agents: real uid 0, no sandbox. Custom agents
	// have their own Linux user.
	IsRoot  bool
	Manager string // empty for agents at the top of the chart
	Runner  string // runner adapter name, for example "echo"
	UID     int
	Home    string
}

// AgentDirectory reads agents and the org chart.
type AgentDirectory interface {
	GetAgent(ctx context.Context, name string) (Agent, error)
	ListAgents(ctx context.Context) ([]Agent, error)
	// Manager returns the agent's manager, or "" at the top of the chart.
	Manager(ctx context.Context, name string) (string, error)
	// IsBelow reports whether name is strictly below ancestor in the chart.
	IsBelow(ctx context.Context, name, ancestor string) (bool, error)
	// TeamRoot returns the top of the chart branch that holds name.
	TeamRoot(ctx context.Context, name string) (string, error)
}

// WakeReason says why an agent is woken, with the runner protocol's values.
// Agents only wake for these.
type WakeReason string

// Wake reasons.
const (
	WakeMessage WakeReason = "message"
	WakeTask    WakeReason = "task"
	WakeEvent   WakeReason = "event"
	WakeTimer   WakeReason = "timer"
	WakeChat    WakeReason = "chat"
)

// Waker wakes a sleeping agent. Waking an awake agent delivers the payload.
// Waking a frozen agent returns ErrFrozen.
type Waker interface {
	Wake(ctx context.Context, agent string, reason WakeReason, payload json.RawMessage) error
}

// Event is the CloudEvents 1.0 envelope with the Agents One extensions. It
// matches contract/schemas/event.schema.json; a contract test checks it.
type Event struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Subject         string          `json:"subject,omitempty"`
	Time            *time.Time      `json:"time,omitempty"`
	DataContentType string          `json:"datacontenttype,omitempty"`
	DataSchema      string          `json:"dataschema,omitempty"`
	Data            json.RawMessage `json:"data,omitempty"`
	// Sequence is assigned by the bus when the event is stored. Build it
	// with FormatSequence so string order equals number order.
	Sequence string `json:"sequence"`
	// Actor is "<kind>:<name>", for example "agent:founder".
	Actor string `json:"actor"`
	// ChartScope is the part of the chart the event concerns, for example "team:founder".
	ChartScope    string `json:"chartscope"`
	CausationID   string `json:"causationid,omitempty"`
	CorrelationID string `json:"correlationid,omitempty"`
}

// EventFilter selects events for a subscription.
type EventFilter struct {
	// Types are type prefixes, for example "agents.one.git.". Empty means all.
	Types []string
	// Source limits to one source. Empty means all.
	Source string
	// AfterSequence replays stored events after this sequence first.
	// Empty replays everything stored.
	AfterSequence string
}

// FormatSequence writes a bus sequence number as 20 zero-padded digits.
func FormatSequence(n uint64) string { return fmt.Sprintf("%020d", n) }

// EventPublisher stores and fans out events.
type EventPublisher interface {
	// Publish stores e, assigns ID (when empty), time and sequence, and
	// returns the stored event.
	Publish(ctx context.Context, e Event) (Event, error)
}

// EventSubscriber streams events.
type EventSubscriber interface {
	// Subscribe returns a channel of matching events. The channel closes
	// when ctx ends.
	Subscribe(ctx context.Context, f EventFilter) (<-chan Event, error)
}

// NewTask is a task to create.
type NewTask struct {
	Title    string
	Body     string
	Assignee string // agent name
	// CreatedBy is filled from the caller when empty.
	CreatedBy string
	// CauseEventID links a task created from an event.
	CauseEventID string
	// Priority is low, normal or high; empty means normal.
	Priority string
}

// Task is a created task.
type Task struct {
	ID       string
	Title    string
	Assignee string
	// Status is a contract TaskStatus value: open, in-progress, blocked, done, cancelled or failed.
	Status    string
	Priority  string
	CreatedBy string
	// Result is what the assignee reported when the task ended.
	Result  string
	Created time.Time
}

// TaskService creates and completes tasks.
type TaskService interface {
	// CreateTask stores an open task. A priority TaskPriority rejects is an ErrInvalid.
	CreateTask(ctx context.Context, t NewTask) (Task, error)
	GetTask(ctx context.Context, id string) (Task, error)
	// CompleteTask ends a task. An outcome that fails Validate is an ErrInvalid.
	CompleteTask(ctx context.Context, id string, outcome TaskOutcome) (Task, error)
}

// BusSender sends messages to agents and channels.
type BusSender interface {
	// Send stores m with an ID and send time and returns it. A message that
	// fails Validate is an ErrInvalid.
	Send(ctx context.Context, m BusMessage) (BusMessage, error)
}

// Freezer holds the daemon's freeze levers. Freezing all is also held as a
// hard wall by the host; team and agent freezes are soft.
type Freezer interface {
	Freeze(ctx context.Context, scope FreezeScope) error
	Release(ctx context.Context, scope FreezeScope) error
	// State returns the active freezes.
	State(ctx context.Context) ([]FreezeScope, error)
}

// TokenIssuer issues and checks bearer tokens.
type TokenIssuer interface {
	Issue(ctx context.Context, subject Caller, scopes []string, ttl time.Duration) (Token, error)
	// Verify returns the token's caller, or ErrUnauthorized when the token
	// is unknown or expired.
	Verify(ctx context.Context, value string) (Caller, error)
}

// TemplateCatalog lists installed agent templates.
type TemplateCatalog interface {
	List(ctx context.Context) ([]AgentTemplate, error)
	Get(ctx context.Context, id string) (AgentTemplate, error)
}

// AgentProfiles reads how agents present themselves, for the live list.
type AgentProfiles interface {
	// Profiles returns every active agent, sorted by name.
	Profiles(ctx context.Context) ([]AgentProfile, error)
}

// ChatPoster posts into an agent's chat, as sendChatMessage does: the
// caller on ctx is the author, and the agent is woken for chat.
type ChatPoster interface {
	// PostChat returns the new message's id. assignmentID links a message
	// the router assigned; it is empty otherwise. A frozen agent is an
	// ErrFrozen, a used-up budget an ErrBudget, an unknown agent an
	// ErrNotFound and empty text an ErrInvalid.
	PostChat(ctx context.Context, agent, text, assignmentID string) (string, error)
}

// AuditEntry is one fact to record. The audit module adds sequence, time,
// prev_hash and hash.
type AuditEntry struct {
	Actor  string
	Action string // dotted verb, for example "agent.hire"
	Target string
	Detail json.RawMessage
}

// AuditLog appends to the hash-chained audit log.
type AuditLog interface {
	Append(ctx context.Context, e AuditEntry) error
}

// MaxLiveLine is the longest live-list line, in bytes. Every agent turn
// emits one line under 255 characters.
const MaxLiveLine = 254

// LiveLine is one agent's current line.
type LiveLine struct {
	Agent   string
	Line    string
	Updated time.Time
}

// LiveList is the one-line-per-agent live list the router reads.
type LiveList interface {
	// UpdateLine sets the agent's line. Lines over MaxLiveLine are an ErrInvalid.
	UpdateLine(ctx context.Context, agent, line string) error
	Lines(ctx context.Context) ([]LiveLine, error)
}

// Budget is an agent's soft allocation. Amounts are in micro units of the
// human's budget currency (1 = one millionth).
type Budget struct {
	Agent           string
	AllocatedMicros int64
	RemainingMicros int64
	// Defence is true when the agent draws on the defence slice.
	Defence bool
}

// BudgetView reads per-agent budget. The hard wall lives in the host gateway.
type BudgetView interface {
	Allocation(ctx context.Context, agent string) (Budget, error)
}

// RunnerInfo describes a runner adapter.
type RunnerInfo struct {
	Name    string // for example "echo", "openai", "claude"
	Version string
	Source  string // "builtin" or the plugin id
}

// ExecSpec is how to start one runner process for one agent.
type ExecSpec struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

// RunnerAdapter starts runner processes of one kind.
type RunnerAdapter interface {
	Info() RunnerInfo
	// Exec returns the command that serves agent a on the Unix socket.
	Exec(ctx context.Context, a Agent, socket string) (ExecSpec, error)
}

// RunnerRegistry finds runner adapters by name.
type RunnerRegistry interface {
	Runner(ctx context.Context, name string) (RunnerAdapter, error)
	Runners(ctx context.Context) ([]RunnerInfo, error)
}

// unavailable answers ErrUnavailable for every port.
type unavailable struct{}

func (unavailable) GetAgent(context.Context, string) (Agent, error) { return Agent{}, ErrUnavailable }
func (unavailable) ListAgents(context.Context) ([]Agent, error)     { return nil, ErrUnavailable }
func (unavailable) Manager(context.Context, string) (string, error) { return "", ErrUnavailable }
func (unavailable) IsBelow(context.Context, string, string) (bool, error) {
	return false, ErrUnavailable
}
func (unavailable) TeamRoot(context.Context, string) (string, error) { return "", ErrUnavailable }
func (unavailable) Wake(context.Context, string, WakeReason, json.RawMessage) error {
	return ErrUnavailable
}
func (unavailable) Publish(context.Context, Event) (Event, error) { return Event{}, ErrUnavailable }
func (unavailable) Subscribe(context.Context, EventFilter) (<-chan Event, error) {
	return nil, ErrUnavailable
}
func (unavailable) CreateTask(context.Context, NewTask) (Task, error) { return Task{}, ErrUnavailable }
func (unavailable) Append(context.Context, AuditEntry) error          { return ErrUnavailable }
func (unavailable) UpdateLine(context.Context, string, string) error  { return ErrUnavailable }
func (unavailable) Lines(context.Context) ([]LiveLine, error)         { return nil, ErrUnavailable }
func (unavailable) Allocation(context.Context, string) (Budget, error) {
	return Budget{}, ErrUnavailable
}
func (unavailable) Runner(context.Context, string) (RunnerAdapter, error) {
	return nil, ErrUnavailable
}
func (unavailable) Runners(context.Context) ([]RunnerInfo, error) { return nil, ErrUnavailable }
func (unavailable) GetTask(context.Context, string) (Task, error) { return Task{}, ErrUnavailable }
func (unavailable) CompleteTask(context.Context, string, TaskOutcome) (Task, error) {
	return Task{}, ErrUnavailable
}
func (unavailable) Send(context.Context, BusMessage) (BusMessage, error) {
	return BusMessage{}, ErrUnavailable
}
func (unavailable) Freeze(context.Context, FreezeScope) error      { return ErrUnavailable }
func (unavailable) Release(context.Context, FreezeScope) error     { return ErrUnavailable }
func (unavailable) State(context.Context) ([]FreezeScope, error)   { return nil, ErrUnavailable }
func (unavailable) Verify(context.Context, string) (Caller, error) { return Caller{}, ErrUnavailable }
func (unavailable) List(context.Context) ([]AgentTemplate, error)  { return nil, ErrUnavailable }
func (unavailable) Get(context.Context, string) (AgentTemplate, error) {
	return AgentTemplate{}, ErrUnavailable
}
func (unavailable) Issue(context.Context, Caller, []string, time.Duration) (Token, error) {
	return Token{}, ErrUnavailable
}
func (unavailable) Profiles(context.Context) ([]AgentProfile, error) { return nil, ErrUnavailable }
func (unavailable) PostChat(context.Context, string, string, string) (string, error) {
	return "", ErrUnavailable
}
