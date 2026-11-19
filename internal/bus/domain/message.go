// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/internal/core"
)

// MaxText is the longest message text, in characters.
const MaxText = 16000

var (
	agentPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	messagePattern = regexp.MustCompile(`^msg_[0-9A-HJKMNP-TV-Z]{26}$`)
	actorPattern   = regexp.MustCompile(`^((agent|human|system|host):[a-z][a-z0-9-]{0,30}|plugin:plg_[0-9A-HJKMNP-TV-Z]{26})$`)
)

// ValidAgentName reports whether s can name an agent.
func ValidAgentName(s string) bool { return agentPattern.MatchString(s) }

// ValidMessageID reports whether s looks like a bus message id.
func ValidMessageID(s string) bool { return messagePattern.MatchString(s) }

// ValidActor reports whether s is an actor in the contract's form.
func ValidActor(s string) bool { return actorPattern.MatchString(s) }

// ActorOf writes the caller as "<kind>:<name>". The name always comes from
// the caller, never from a request body.
func ActorOf(c core.Caller) (string, error) {
	actor := string(c.Kind) + ":" + c.Name
	if !ValidActor(actor) {
		return "", ErrInvalidActor
	}
	return actor, nil
}

// ParseTarget splits a core.BusMessage target, "agent:<name>" or
// "channel:<name>", into a channel name or an agent name.
func ParseTarget(target string) (channel, agent string, err error) {
	switch kind, name, _ := strings.Cut(target, ":"); {
	case kind == "channel" && ValidChannelName(name):
		return name, "", nil
	case kind == "agent" && ValidAgentName(name):
		return "", name, nil
	}
	return "", "", ErrInvalidTarget
}

// Message is one stored bus message. Exactly one of Channel and To is set.
type Message struct {
	// Sequence orders messages and is the SSE resume key. The store assigns it.
	Sequence      uint64
	ID            string
	Channel       string
	To            string // recipient agent
	From          string // actor
	Text          string
	ReplyTo       string
	CorrelationID string
	CreatedAt     time.Time
}

// IsDirect reports whether m is addressed to one agent.
func (m Message) IsDirect() bool { return m.To != "" }

// Draft is a request to send a message.
type Draft struct {
	Channel string
	To      string
	Text    string
	ReplyTo string
}

// Validate checks the draft's shape.
func (d Draft) Validate() error {
	switch {
	case (d.Channel == "") == (d.To == ""):
		return fmt.Errorf("%w: set exactly one of channel and to", ErrInvalidMessage)
	case d.Channel != "" && !ValidChannelName(d.Channel):
		return fmt.Errorf("%w: channel name is not valid", ErrInvalidMessage)
	case d.To != "" && !ValidAgentName(d.To):
		return fmt.Errorf("%w: recipient is not an agent name", ErrInvalidMessage)
	case strings.TrimSpace(d.Text) == "":
		return fmt.Errorf("%w: text is empty", ErrInvalidMessage)
	case utf8.RuneCountInString(d.Text) > MaxText:
		return fmt.Errorf("%w: text is over %d characters", ErrInvalidMessage, MaxText)
	case d.ReplyTo != "" && !ValidMessageID(d.ReplyTo):
		return fmt.Errorf("%w: reply_to is not a message id", ErrInvalidMessage)
	}
	return nil
}

// Message builds the stored message. correlation is the id that ties the
// conversation: the first message's own id, or the one it replies to.
func (d Draft) Message(id, from, correlation string, at time.Time) Message {
	return Message{
		ID: id, Channel: d.Channel, To: d.To, From: from, Text: d.Text, ReplyTo: d.ReplyTo,
		CorrelationID: correlation, CreatedAt: at.UTC(),
	}
}
