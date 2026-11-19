// SPDX-License-Identifier: MIT

package httpadapter

import (
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/router/domain"
)

// This file is the only place generated DTOs meet domain values, so a
// contract rename stays a change to this file alone.

func toLiveEntry(e domain.Entry) api.LiveEntry {
	out := api.LiveEntry{
		Agent: e.Agent, DisplayName: e.DisplayName, Kind: api.AgentKind(e.Kind),
		State: api.AgentState(e.State), Team: e.Team, Role: e.Role,
	}
	if e.Line != nil {
		out.OneLine = &api.AgentOneLine{Text: e.Line.String(), UpdatedAt: api.Timestamp(e.Updated)}
	}
	return out
}

func toLiveList(entries []domain.Entry, next string) api.LiveList {
	out := api.LiveList{Items: make([]api.LiveEntry, 0, len(entries))}
	for _, e := range entries {
		out.Items = append(out.Items, toLiveEntry(e))
	}
	if next != "" {
		out.NextCursor = &next
	}
	return out
}

func toAssignment(a domain.Assignment) api.Assignment {
	hops := make([]api.RouterHop, 0, len(a.Hops))
	for _, h := range a.Hops {
		hops = append(hops, api.RouterHop{Router: h.Router, HandedTo: h.HandedTo, Reason: h.Reason})
	}
	list := make([]api.ShortlistEntry, 0, len(a.Shortlist))
	for _, s := range a.Shortlist {
		list = append(list, api.ShortlistEntry{Agent: s.Agent, Score: float32(s.Score), Reason: s.Reason, OneLine: s.OneLine})
	}
	return api.Assignment{
		Id: a.ID, Text: a.Text, Chosen: a.Chosen, Router: a.Router, Hops: hops, Shortlist: list,
		ChatPath: a.ChatPath, ChatMessageId: a.ChatMessageID, CreatedBy: a.CreatedBy, CreatedAt: api.Timestamp(a.CreatedAt),
	}
}

func entryFrame(e domain.Entry) api.LiveEntryFrame {
	return api.LiveEntryFrame{Kind: api.LiveEntryFrameKindEntry, Entry: toLiveEntry(e)}
}

func removedFrame(agent string) api.LiveRemovedFrame {
	return api.LiveRemovedFrame{Kind: api.LiveRemovedFrameKindRemoved, Agent: agent}
}

func heartbeatFrame(t time.Time) api.Heartbeat {
	return api.Heartbeat{Kind: api.HeartbeatKindHeartbeat, Time: api.Timestamp(t)}
}
