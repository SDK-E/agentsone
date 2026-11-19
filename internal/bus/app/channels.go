// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"

	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// CreateChannel makes a public channel, or a team channel for the branch of
// the caller. The creator is the caller; a request cannot name another.
func (s *Service) CreateChannel(ctx context.Context, spec domain.ChannelSpec) (domain.Channel, error) {
	c := callerOf(ctx)
	actor, err := domain.ActorOf(c)
	if err != nil {
		return domain.Channel{}, err
	}
	team := ""
	if spec.Kind == domain.KindTeam && c.Kind == core.CallerAgent {
		if team, err = s.d.Ports.Agents.TeamRoot(ctx, c.Name); err != nil {
			return domain.Channel{}, fmt.Errorf("find the team of %s: %w", c.Name, err)
		}
	}
	ch, err := domain.NewChannel(spec, actor, team, s.d.Clock.Now())
	if err != nil {
		return domain.Channel{}, err
	}
	if err := s.d.Channels.Create(ctx, ch); err != nil {
		return domain.Channel{}, err
	}
	s.audit(ctx, actor, "channel.create", "channel:"+ch.Name, map[string]any{"kind": string(ch.Kind), "team": ch.Team})
	return ch, nil
}

// GetChannel returns a channel the caller may see. A channel out of sight is
// reported as missing, so its existence stays private.
func (s *Service) GetChannel(ctx context.Context, name string) (domain.Channel, error) {
	ch, err := s.d.Channels.Get(ctx, name)
	if err != nil {
		return domain.Channel{}, err
	}
	if !s.viewerOf(ctx, callerOf(ctx)).CanSeeChannel(ch) {
		return domain.Channel{}, fmt.Errorf("channel %q: %w", name, core.ErrNotFound)
	}
	return ch, nil
}

// ChannelPage is one page of channels.
type ChannelPage struct {
	Channels []domain.Channel
	// Next is the last name of this page when more follow, to pass as the
	// start of the next page. It is "" at the end.
	Next string
}

// ListChannels returns the channels the caller may see, in name order,
// starting after afterName.
func (s *Service) ListChannels(ctx context.Context, afterName string, limit int) (ChannelPage, error) {
	limit = clampLimit(limit)
	viewer := s.viewerOf(ctx, callerOf(ctx))
	var page ChannelPage
	cursor := afterName
	for {
		rows, err := s.d.Channels.List(ctx, cursor, batch)
		if err != nil {
			return ChannelPage{}, err
		}
		for _, ch := range rows {
			cursor = ch.Name
			if !viewer.CanSeeChannel(ch) {
				continue
			}
			if len(page.Channels) == limit {
				page.Next = page.Channels[limit-1].Name
				return page, nil
			}
			page.Channels = append(page.Channels, ch)
		}
		if len(rows) < batch {
			return page, nil
		}
	}
}
