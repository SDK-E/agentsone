// SPDX-License-Identifier: MIT

package agents

import (
	"context"

	"github.com/SDK-E/agentsone/internal/agents/app"
	supapp "github.com/SDK-E/agentsone/internal/agents/supervisor/app"
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
