// SPDX-License-Identifier: MIT

package sdk

import (
	"context"
	"encoding/json"
	"time"
)

// ProtocolVersion is the newest runner protocol version this SDK speaks.
// Version 1.1 adds credentials.update; the SDK also speaks 1.0.
const ProtocolVersion = "1.1"

// protocolVersions are the versions the SDK speaks, highest first (rule H2).
var protocolVersions = []string{ProtocolVersion, "1.0"}

// EnvMCPToken is the variable in the welcome's mcp.env that holds the MCP
// token of a stdio endpoint.
const EnvMCPToken = "AGENTSONE_TOKEN"

// MaxOneLine is the longest profile line, in characters.
const MaxOneLine = 255

// Info names the runner in hello.
type Info struct {
	Name    string
	Version string
}

// Capabilities are what the runner can do, sent in hello.
type Capabilities struct {
	Streaming  bool
	Cancel     bool
	Compaction bool
	Resume     bool
}

// Secret is a scoped token from the welcome. It prints as "[redacted]" so it
// cannot reach a log by accident; Reveal returns the value.
type Secret string

// String hides the value.
func (Secret) String() string { return "[redacted]" }

// GoString hides the value in %#v.
func (Secret) GoString() string { return `"[redacted]"` }

// MarshalText hides the value in JSON and other text encodings.
func (Secret) MarshalText() ([]byte, error) { return []byte("[redacted]"), nil }

// Reveal returns the token.
func (s Secret) Reveal() string { return string(s) }

// UnmarshalJSON reads the token from a JSON string.
func (s *Secret) UnmarshalJSON(b []byte) error {
	var v string
	if err := json.Unmarshal(b, &v); err != nil {
		return err //nolint:wrapcheck // plain JSON decode error
	}
	*s = Secret(v)
	return nil
}

// Welcome is the daemon's answer to hello: everything the runner needs to act
// as its agent.
type Welcome struct {
	ProtocolVersion string   `json:"protocolVersion"`
	Agent           Agent    `json:"agent"`
	MCP             MCP      `json:"mcp"`
	Model           Model    `json:"model"`
	Session         Session  `json:"session"`
	Config          Settings `json:"config"`
	// Credentials hold the latest gateway and MCP tokens. They start as the
	// welcome's tokens and are renewed in place on protocol 1.1. Read them
	// before every model call and MCP request; never keep a token copy.
	Credentials *Credentials `json:"-"`
}

// Agent is the identity of the agent this runner serves.
type Agent struct {
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

// MCP is the machine MCP endpoint. For streamable-http, URL and Token are
// set; for stdio, Command, Args and Env. Token (or AGENTSONE_TOKEN in Env) is
// the first token only: it expires, so use Welcome.Credentials for requests.
type MCP struct {
	Transport      string            `json:"transport"`
	URL            string            `json:"url"`
	Token          Secret            `json:"token"`
	TokenExpiresAt string            `json:"tokenExpiresAt"`
	Command        string            `json:"command"`
	Args           []string          `json:"args"`
	Env            map[string]string `json:"env"`
}

// Model is the model gateway: an OpenAI-compatible API with a scoped token.
// Token is the first token only: it expires, so use Welcome.Credentials for
// calls.
type Model struct {
	BaseURL       string   `json:"baseUrl"`
	Name          string   `json:"name"`
	Token         Secret   `json:"token"`
	Weights       []string `json:"weights"`
	DefaultWeight string   `json:"defaultWeight"`
}

// Session is what survives between runner processes.
type Session struct {
	Summary      *string    `json:"summary"`
	CompactedAt  *time.Time `json:"compactedAt"`
	PausedTurnID *string    `json:"pausedTurnId"`
}

// Settings is the welcome config.
type Settings struct {
	HeartbeatIntervalMs int            `json:"heartbeatIntervalMs"`
	HeartbeatTimeoutMs  int            `json:"heartbeatTimeoutMs"`
	MaxLineBytes        int            `json:"maxLineBytes"`
	Runner              map[string]any `json:"runner"`
}

// WakeReason says why the daemon started a turn.
type WakeReason string

// The wake reasons of the protocol.
const (
	WakeMessage WakeReason = "message"
	WakeTask    WakeReason = "task"
	WakeEvent   WakeReason = "event"
	WakeTimer   WakeReason = "timer"
	WakeChat    WakeReason = "chat"
)

// BudgetRestoredEvent is the event type that resumes a paused turn.
const BudgetRestoredEvent = "agentsone.agents.budget.restored"

// BusMessage is a message from another agent on the bus.
type BusMessage struct {
	ID            string  `json:"id"`
	From          string  `json:"from"`
	To            string  `json:"to"`
	Subject       string  `json:"subject"`
	Body          string  `json:"body"`
	SentAt        string  `json:"sentAt"`
	ReplyTo       *string `json:"replyTo"`
	CorrelationID *string `json:"correlationId"`
}

// ChatMessage is a message in a chat with a human or an agent.
type ChatMessage struct {
	ID       string  `json:"id"`
	ChatID   string  `json:"chatId"`
	From     string  `json:"from"`
	Text     string  `json:"text"`
	SentAt   string  `json:"sentAt"`
	RoutedBy *string `json:"routedBy"`
}

// Task is work assigned to the agent.
type Task struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Body          string  `json:"body"`
	From          string  `json:"from"`
	CreatedAt     string  `json:"createdAt"`
	Due           *string `json:"due"`
	CorrelationID *string `json:"correlationId"`
}

// Timer describes a schedule that fired.
type Timer struct {
	ScheduleID string  `json:"scheduleId"`
	Name       string  `json:"name"`
	Due        string  `json:"due"`
	FiredAt    string  `json:"firedAt"`
	Note       *string `json:"note"`
}

// Event is a CloudEvents 1.0 envelope with the Agents One extensions.
type Event struct {
	ID         string         `json:"id"`
	Source     string         `json:"source"`
	Type       string         `json:"type"`
	Subject    *string        `json:"subject"`
	Time       string         `json:"time"`
	Data       map[string]any `json:"data"`
	Sequence   string         `json:"sequence"`
	Actor      string         `json:"actor"`
	ChartScope string         `json:"chartscope"`
}

// Wake starts a turn. Exactly one payload field is set, the one for Reason.
type Wake struct {
	Reason  WakeReason
	TurnID  string
	Message *BusMessage
	Task    *Task
	Event   *Event
	Timer   *Timer
	Chat    *ChatMessage
}

// Text is the main text of the wake payload: the chat or message body, the
// task body, the timer note. It is empty for events.
func (w Wake) Text() string {
	switch {
	case w.Chat != nil:
		return w.Chat.Text
	case w.Message != nil:
		return w.Message.Body
	case w.Task != nil:
		return w.Task.Body
	case w.Timer != nil && w.Timer.Note != nil:
		return *w.Timer.Note
	}
	return ""
}

// From is who sent the wake payload as <kind>:<name>, or "" for timers and
// events.
func (w Wake) From() string {
	switch {
	case w.Chat != nil:
		return w.Chat.From
	case w.Message != nil:
		return w.Message.From
	case w.Task != nil:
		return w.Task.From
	}
	return ""
}

// DeliveryKind says whether a delivered message is a bus or a chat message.
type DeliveryKind string

// The delivery kinds.
const (
	DeliverBus  DeliveryKind = "bus"
	DeliverChat DeliveryKind = "chat"
)

// Delivery is a message handed to the turn that is running.
type Delivery struct {
	Kind DeliveryKind
	Bus  *BusMessage
	Chat *ChatMessage
}

// Text is the body of the delivered message.
func (d Delivery) Text() string {
	switch {
	case d.Chat != nil:
		return string(d.Chat.Text)
	case d.Bus != nil:
		return string(d.Bus.Body)
	}
	return ""
}

// SleepRequest asks the runner to compact its session.
type SleepRequest struct {
	Reason          string
	MaxSummaryChars int
}

// Usage reports model tokens and a cost estimate (micro US dollars).
type Usage struct {
	Model           string
	Weight          string
	TokensIn        int
	TokensOut       int
	CacheReadTokens *int
	CostMicroUsd    int
}

// BusSend is a message to another agent.
type BusSend struct {
	To            string
	Subject       string
	Body          string
	ReplyTo       string
	CorrelationID string
}

// TaskOutcome is how work on a task ended.
type TaskOutcome string

// The task outcomes.
const (
	TaskDone     TaskOutcome = "done"
	TaskFailed   TaskOutcome = "failed"
	TaskDeclined TaskOutcome = "declined"
)

// TaskDoneReport ends work on a task.
type TaskDoneReport struct {
	TaskID  string
	Outcome TaskOutcome
	Summary string
	Details string
}

// LogLevel is a diagnostic level.
type LogLevel string

// The log levels.
const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// Handler is the part of a runner that is its own: what it does in a turn.
// The SDK calls it from one goroutine at a time for Wake and Resume.
type Handler interface {
	// Start is called once, after the welcome and before the first status.
	// A session summary in the welcome is how the session starts (rule S2).
	Start(ctx context.Context, w Welcome) error
	// Wake runs one turn. ctx ends when the turn is cancelled or the runner
	// shuts down. Return ErrBudgetExhausted to pause the turn (rule R1).
	// Return nil when the turn is done; the SDK ends it with status idle.
	Wake(ctx context.Context, t *Turn, w Wake) error
	// Sleep compacts the session into a Markdown summary of at most
	// req.MaxSummaryChars characters (rule S1).
	Sleep(ctx context.Context, req SleepRequest) (string, error)
}

// Resumer is implemented by runners that can resume a turn paused for budget.
// A runner without it, or with Capabilities.Resume false, answers the resume
// wake with error 1011 (rule R3).
type Resumer interface {
	// CanResume reports whether the paused turn's state is still held.
	CanResume(turnID string) bool
	// Resume continues the paused turn where it stopped (rule R2). Return
	// ErrBudgetExhausted to pause it again.
	Resume(ctx context.Context, t *Turn) error
}
