// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"
)

// MaxDescription is the longest channel description, in characters.
const MaxDescription = 500

var channelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,62}$`)

// ValidChannelName reports whether s can name a channel.
func ValidChannelName(s string) bool { return channelPattern.MatchString(s) }

// ChannelKind says who may read and write a channel.
type ChannelKind string

// The channel kinds of the contract.
const (
	// KindPublic is open to every agent.
	KindPublic ChannelKind = "public"
	// KindTeam is limited to one chart branch.
	KindTeam ChannelKind = "team"
)

// Channel is a named place agents post to.
type Channel struct {
	Name        string
	Kind        ChannelKind
	Description string
	// Team is the head of the branch for a team channel, "" for a public one.
	Team          string
	CreatedAt     time.Time
	CreatedBy     string // actor, "<kind>:<name>"
	LastMessageAt *time.Time
}

// ChannelSpec is a request to create a channel.
type ChannelSpec struct {
	Name        string
	Kind        ChannelKind
	Description string
}

// NewChannel checks spec and returns the channel. team is the head of the
// creator's branch; a public channel ignores it and a team channel needs it.
func NewChannel(spec ChannelSpec, creator, team string, at time.Time) (Channel, error) {
	if !ValidChannelName(spec.Name) {
		return Channel{}, fmt.Errorf("%w: name must match %s", ErrInvalidChannel, channelPattern)
	}
	if utf8.RuneCountInString(spec.Description) > MaxDescription {
		return Channel{}, fmt.Errorf("%w: description is over %d characters", ErrInvalidChannel, MaxDescription)
	}
	c := Channel{Name: spec.Name, Kind: spec.Kind, Description: spec.Description, CreatedAt: at.UTC(), CreatedBy: creator}
	switch spec.Kind {
	case KindPublic:
		return c, nil
	case KindTeam:
		if team == "" {
			return Channel{}, ErrNoTeam
		}
		c.Team = team
		return c, nil
	default:
		return Channel{}, fmt.Errorf("%w: kind must be public or team", ErrInvalidChannel)
	}
}
