// SPDX-License-Identifier: MIT

package agents

import (
	"context"
	"fmt"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	supapp "github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/core"
)

// output is the supervisor's OutputSink over the society: chat output
// becomes the agent's reply, other output goes to its output stream.
type output struct{ s *app.Society }

func (o output) Output(ctx context.Context, agent string, c supapp.OutputChunk) error {
	return o.s.Output(ctx, agent, app.OutputChunk{TurnID: c.TurnID, ChatID: c.ChatID, Index: c.Index, Text: c.Text, Final: c.Final})
}

// usage is the supervisor's UsageRecorder over the society's soft budget:
// the cost of each usage notification counts against the agent's share.
type usage struct{ s *app.Society }

func (u usage) RecordUsage(ctx context.Context, agent string, v supapp.Usage) error {
	return u.s.Budget().RecordUsage(ctx, agent, int64(v.CostMicroUSD))
}

// runs is the supervisor's RunObserver over the society: what a runner
// does becomes the agent's shown state, and the end of a turn its last turn.
type runs struct{ s *app.Society }

var _ supapp.RunObserver = runs{}

// shown is the society state each run state shows as.
var shown = map[supapp.RunState]domain.State{
	supapp.RunStopped:  domain.StateSleeping,
	supapp.RunStarting: domain.StateWaking,
	supapp.RunIdle:     domain.StateAwake,
	supapp.RunWorking:  domain.StateWorking,
}

func (r runs) RunnerState(ctx context.Context, agent string, st supapp.RunState) error {
	state, ok := shown[st]
	if !ok {
		return fmt.Errorf("run state %q of %s: %w", st, agent, core.ErrInvalid)
	}
	return r.s.SetRunState(ctx, agent, state)
}

func (r runs) TurnEnded(ctx context.Context, agent string, at time.Time) error {
	return r.s.TurnEnded(ctx, agent, at)
}
