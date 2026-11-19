// SPDX-License-Identifier: MIT

package httpadapter

import (
	"fmt"
	"strconv"
	"time"

	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
)

// stamp converts a time to the contract's timestamp, which writes UTC.
func stamp(t time.Time) api.Timestamp { return api.Timestamp(t.UTC()) }

func stampPtr(t *time.Time) *api.Timestamp {
	if t == nil {
		return nil
	}
	s := stamp(*t)
	return &s
}

func timePtr(s *api.Timestamp) *time.Time {
	if s == nil {
		return nil
	}
	t := time.Time(*s)
	return &t
}

// optional returns nil for an empty string, so the contract's nullable
// fields are null, not "".
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func valueOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}

func toChannel(c domain.Channel) api.Channel {
	return api.Channel{
		Name: c.Name, Kind: api.ChannelKind(c.Kind), Description: c.Description, Team: optional(c.Team),
		CreatedAt: stamp(c.CreatedAt), CreatedBy: c.CreatedBy, LastMessageAt: stampPtr(c.LastMessageAt),
	}
}

func toChannelList(page []domain.Channel, next string) api.ChannelList {
	items := make([]api.Channel, 0, len(page))
	for _, c := range page {
		items = append(items, toChannel(c))
	}
	return api.ChannelList{Items: items, NextCursor: optional(next)}
}

func toSpec(c api.ChannelCreate) domain.ChannelSpec {
	return domain.ChannelSpec{Name: c.Name, Kind: domain.ChannelKind(c.Kind), Description: valueOr(c.Description, "")}
}

func toMessage(m domain.Message) api.BusMessage {
	return api.BusMessage{
		Id: m.ID, Channel: optional(m.Channel), To: optional(m.To), From: m.From, Text: m.Text,
		ReplyTo: optional(m.ReplyTo), CorrelationId: optional(m.CorrelationID), CreatedAt: stamp(m.CreatedAt),
	}
}

func toMessageList(page []domain.Message, next uint64) api.BusMessageList {
	items := make([]api.BusMessage, 0, len(page))
	for _, m := range page {
		items = append(items, toMessage(m))
	}
	var cursor *string
	if next != 0 {
		c := formatSequence(next)
		cursor = &c
	}
	return api.BusMessageList{Items: items, NextCursor: cursor}
}

func toDraft(c api.BusMessageCreate) domain.Draft {
	return domain.Draft{
		Channel: valueOr(c.Channel, ""), To: valueOr(c.To, ""), Text: c.Text, ReplyTo: valueOr(c.ReplyTo, ""),
	}
}

// formatSequence writes a message sequence as a cursor or an SSE id.
func formatSequence(n uint64) string { return strconv.FormatUint(n, 10) }

// parseSequence reads a cursor or Last-Event-ID. what names it in the error.
func parseSequence(s, what string) (uint64, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("%w: %s is not a bus message position", core.ErrInvalid, what)
	}
	return n, nil
}
