// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

const gatewayReason = "The model gateway is not reachable."

// A runner failure shows on the agent with its reason until the runner
// runs again, and each change goes out as a state frame.
func TestRunnerFailed_ShowsTheReasonUntilTheRunnerRunsAgain(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	frames := e.society.Hub().Subscribe(ctx, "dev", app.StreamOutput)

	if err := e.society.RunnerFailed(ctx, "dev", gatewayReason); err != nil {
		t.Fatal(err)
	}

	if f := receive(t, frames); f.State == nil || f.State.State != domain.StateFailed || f.State.Reason != gatewayReason {
		t.Fatalf("frame %+v, want state failed with the reason", f.State)
	}
	dev, err := e.society.Get(asHuman("ada"), "dev")
	if err != nil || dev.State != domain.StateFailed || dev.StateReason != gatewayReason {
		t.Fatalf("Get = %+v, %v; want failed with the reason", dev, err)
	}
	failed, err := e.society.List(asHuman("ada"), app.ListFilter{State: domain.StateFailed})
	if err != nil || len(failed) != 1 || failed[0].Name != "dev" || failed[0].StateReason != gatewayReason {
		t.Fatalf("List(failed) = %+v, %v", failed, err)
	}
	chart, err := e.society.Chart(asHuman("ada"))
	if a, _ := chart.Get("dev"); err != nil || a.State != domain.StateFailed {
		t.Fatalf("chart dev = %+v, %v", a, err)
	}

	if err := e.society.RunnerRunning(ctx, "dev"); err != nil {
		t.Fatal(err)
	}

	if f := receive(t, frames); f.State == nil || f.State.State != domain.StateAwake || f.State.Reason != "" {
		t.Fatalf("frame %+v, want state awake without a reason", f.State)
	}
	dev, _ = e.society.Get(asHuman("ada"), "dev")
	if dev.State == domain.StateFailed || dev.StateReason != "" {
		t.Fatalf("Get after the runner runs again = %+v, want no failure", dev)
	}
}

// When what kept a runner off is gone (the network wall holds again), the
// failure clears and the frame carries the agent's own state, not awake:
// the runner has not started yet. An agent that showed no failure gets no
// frame.
func TestRunnerCleared_ClearsTheFailureWithTheStoredState(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	frames := e.society.Hub().Subscribe(ctx, "dev", app.StreamOutput)
	before, err := e.society.Get(asHuman("ada"), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.society.RunnerFailed(ctx, "dev", "The network wall does not hold."); err != nil {
		t.Fatal(err)
	}
	_ = receive(t, frames)

	if err := e.society.RunnerCleared(ctx, "dev"); err != nil {
		t.Fatal(err)
	}

	if f := receive(t, frames); f.State == nil || f.State.State != before.State || f.State.Reason != "" {
		t.Fatalf("frame %+v, want state %s without a reason", f.State, before.State)
	}
	dev, _ := e.society.Get(asHuman("ada"), "dev")
	if dev.State != before.State || dev.StateReason != "" {
		t.Fatalf("Get after clearing = %+v, want %s and no reason", dev, before.State)
	}
	if err := e.society.RunnerCleared(ctx, "dev"); err != nil {
		t.Fatal(err)
	}
	select {
	case f := <-frames:
		t.Fatalf("a second clear sent frame %+v; nothing changed", f)
	default:
	}
	if err := e.society.RunnerCleared(ctx, "nobody"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("RunnerCleared(nobody) = %v, want core.ErrNotFound", err)
	}
}

// The failure lives with the runner, not in core.db: saving the agent for
// another reason never stores it.
func TestRunnerFailed_IsNotStored(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_ = e.society.RunnerFailed(context.Background(), "dev", gatewayReason)
	role := "Tester"
	if _, err := e.society.Update(asAgent("lead"), "dev", domain.Update{Role: &role}); err != nil {
		t.Fatal(err)
	}
	stored, err := e.repo.GetAgent(context.Background(), "dev")
	if err != nil || stored.State != domain.StateSleeping {
		t.Fatalf("stored dev = %+v, %v; want sleeping", stored, err)
	}
}

// Frozen and retired say more than a runner failure, so they win.
func TestRunnerFailed_FrozenAndRetiredWin(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	host := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerHost, Name: "agentsone"})
	if err := e.society.Freezer().Freeze(host, core.FreezeScope{Kind: core.FreezeAgent, Target: "dev"}); err != nil {
		t.Fatal(err)
	}
	_ = e.society.RunnerFailed(context.Background(), "dev", gatewayReason)
	if dev, _ := e.society.Get(asHuman("ada"), "dev"); dev.State != domain.StateFrozen || dev.StateReason != "" {
		t.Fatalf("frozen dev = %+v, want frozen without a reason", dev)
	}
}

// The reason is one line of at most 500 characters; a blank one still says
// something.
func TestRunnerFailed_KeepsTheReasonShortAndNeverBlank(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_ = e.society.RunnerFailed(context.Background(), "dev", "  "+strings.Repeat("é", 600)+"\n")
	if dev, _ := e.society.Get(asHuman("ada"), "dev"); len([]rune(dev.StateReason)) != 500 || strings.ContainsAny(dev.StateReason, " \n") {
		t.Fatalf("reason has %d characters: %q", len([]rune(dev.StateReason)), dev.StateReason)
	}
	_ = e.society.RunnerFailed(context.Background(), "dev", " \n ")
	if dev, _ := e.society.Get(asHuman("ada"), "dev"); dev.StateReason != "The runner stopped with an error." {
		t.Fatalf("blank reason became %q", dev.StateReason)
	}
}

func TestRunnerFailed_UnknownAgentIsNotFound(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if err := e.society.RunnerFailed(context.Background(), "ghost", gatewayReason); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}
