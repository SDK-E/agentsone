// SPDX-License-Identifier: MIT

package agents

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/adapters/memory"
	"github.com/SDK-E/agentsone/internal/agents/agentstest"
	"github.com/SDK-E/agentsone/internal/agents/app"
	supapp "github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

var (
	_ supapp.OutputSink    = output{}
	_ supapp.UsageRecorder = usage{}
)

func sinkWorld(t *testing.T) (*memory.Repository, *app.Society) {
	t.Helper()
	fakes, ports := coretest.NewFakes()
	repo := memory.New()
	for _, a := range agentstest.Society() {
		if err := repo.CreateAgent(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	return repo, app.New(repo, agentstest.NewRuntime(), ports, fakes.Clock, fakes.IDs)
}

// Runner chat output becomes the agent's stored reply; other output goes
// to the output stream.
func TestOutput_PassesRunnerOutputToTheSociety(t *testing.T) {
	t.Parallel()
	_, s := sinkWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	frames := s.Hub().Subscribe(ctx, "dev", app.StreamOutput)
	out := output{s: s}

	if err := out.Output(ctx, "dev", supapp.OutputChunk{TurnID: "trn_1", Index: 0, Text: "running tests"}); err != nil {
		t.Fatal(err)
	}
	select {
	case f := <-frames:
		if f.Output == nil || f.Output.Text != "running tests" {
			t.Fatalf("frame %+v, want the output", f)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no output frame")
	}

	if err := out.Output(ctx, "dev", supapp.OutputChunk{TurnID: "trn_2", ChatID: "cht_1", Text: "Done.", Final: true}); err != nil {
		t.Fatal(err)
	}
	msgs, err := s.Chat(core.WithCaller(ctx, core.Caller{Kind: core.CallerHuman, Name: "ada"}), "dev", "", 10)
	if err != nil || len(msgs) != 1 || msgs[0].Text != "Done." {
		t.Fatalf("chat = %+v, %v; want the agent's reply", msgs, err)
	}
}

// The cost of a runner's usage counts against the agent's soft share.
func TestUsage_RecordsTheCostOnTheAgent(t *testing.T) {
	t.Parallel()
	repo, s := sinkWorld(t)
	u := usage{s: s}

	if err := u.RecordUsage(context.Background(), "dev", supapp.Usage{TurnID: "trn_1", Model: "m", CostMicroUSD: 1500}); err != nil {
		t.Fatal(err)
	}
	used, err := repo.Usage(context.Background())
	if err != nil || used["dev"] != 1500 {
		t.Fatalf("usage = %v, %v; want 1500 micro-USD on dev", used, err)
	}
	if err := u.RecordUsage(context.Background(), "ghost", supapp.Usage{CostMicroUSD: 1}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("usage of an unknown agent = %v, want ErrNotFound", err)
	}
}
