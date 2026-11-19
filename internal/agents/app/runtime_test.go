// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

func asHost() context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: core.CallerHost, Name: "agentsone", UID: -1})
}

func scope(t *testing.T, s string) core.FreezeScope {
	t.Helper()
	sc, err := core.ParseFreezeScope(s)
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func receive(t *testing.T, ch <-chan app.Frame) app.Frame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("no frame within 5s")
		return app.Frame{}
	}
}

func TestSendChat_StoresStreamsAndWakesTheAgent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx, cancel := context.WithCancel(asHuman("ada"))
	t.Cleanup(cancel)
	frames := e.society.Hub().Subscribe(ctx, "dev", app.StreamChat)

	m, err := e.society.SendChat(asHuman("ada"), "dev", "Hello")
	if err != nil {
		t.Fatal(err)
	}

	if m.Role != domain.ChatHuman || m.Author != "human:ada" || !strings.HasPrefix(m.ID, "cht_") {
		t.Fatalf("message %+v", m)
	}
	if f := receive(t, frames); f.Message == nil || f.Message.Message.ID != m.ID {
		t.Fatalf("frame %+v, want the stored message", f)
	}
	if diff := cmp.Diff([]core.WakeReason{core.WakeChat}, e.rt.Wakes()); diff != "" {
		t.Errorf("wakes (-want +got):\n%s", diff)
	}
	list, err := e.society.Chat(asHuman("ada"), "dev", "", 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("chat history %v %v", list, err)
	}
}

func TestSendChat_Refusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		prepare func(t *testing.T, e env)
		ctx     context.Context
		agent   string
		text    string
		wantErr error
	}{
		{"frozen agent", func(t *testing.T, e env) {
			if err := e.society.Freezer().Freeze(asHost(), scope(t, "agent:dev")); err != nil {
				t.Fatal(err)
			}
		}, asHuman("ada"), "dev", "hi", core.ErrFrozen},
		{"budget used up", func(t *testing.T, e env) {
			pushPool(t, e, 100)
			if err := e.society.Budget().RecordUsage(context.Background(), "dev", 100); err != nil {
				t.Fatal(err)
			}
		}, asHuman("ada"), "dev", "hi", core.ErrBudget},
		{"unknown agent", func(*testing.T, env) {}, asHuman("ada"), "ghost", "hi", domain.ErrAgentNotFound},
		{"empty text", func(*testing.T, env) {}, asHuman("ada"), "dev", "", domain.ErrInvalidChat},
		{"no caller", func(*testing.T, env) {}, context.Background(), "dev", "hi", core.ErrUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			tt.prepare(t, e)
			_, err := e.society.SendChat(tt.ctx, tt.agent, tt.text)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("SendChat() error = %v, want %v", err, tt.wantErr)
			}
			if list, _ := e.repo.ListChat(context.Background(), "dev", "", 10); len(list) != 0 {
				t.Errorf("stored %d messages", len(list))
			}
			if got := e.rt.Wakes(); len(got) != 0 {
				t.Errorf("woke %v", got)
			}
		})
	}
}

func TestOutput_StreamsDeltasThenStoresTheReply(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	frames := e.society.Hub().Subscribe(ctx, "dev", app.StreamChat)

	for i, part := range []string{"Good ", "day"} {
		if err := e.society.Output(ctx, "dev", app.OutputChunk{TurnID: "trn_1", ChatID: "cht_1", Index: i, Text: part, Final: i == 1}); err != nil {
			t.Fatal(err)
		}
	}

	first, second, stored := receive(t, frames), receive(t, frames), receive(t, frames)
	if first.Delta == nil || second.Delta == nil || first.Delta.Text+second.Delta.Text != "Good day" {
		t.Fatalf("deltas %+v %+v", first.Delta, second.Delta)
	}
	if stored.Message == nil || stored.Message.Message.Text != "Good day" || stored.Message.Message.Role != domain.ChatAgent || stored.Message.Message.ID != first.Delta.MessageID {
		t.Fatalf("stored frame %+v", stored.Message)
	}
	list, _ := e.repo.ListChat(ctx, "dev", "", 10)
	if len(list) != 1 || list[0].Text != "Good day" {
		t.Fatalf("history %+v", list)
	}
}

func TestOutput_SendsOtherOutputToTheOutputStream(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	frames := e.society.Hub().Subscribe(ctx, "dev", app.StreamOutput)

	if err := e.society.Output(ctx, "dev", app.OutputChunk{TurnID: "trn_1", Text: "running tests"}); err != nil {
		t.Fatal(err)
	}

	if f := receive(t, frames); f.Output == nil || f.Output.Text != "running tests" {
		t.Fatalf("frame %+v", f)
	}
}

func TestHub_ClosesTheChannelWhenTheSubscriberLeaves(t *testing.T) {
	t.Parallel()
	hub := app.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	frames := hub.Subscribe(ctx, "dev", app.StreamChat)

	cancel()

	select {
	case _, open := <-frames:
		if open {
			t.Fatal("got a frame, want a closed channel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("channel still open after cancel")
	}
}

func TestWaker_WaitsForTheMachineStartup(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fakes.Startup.Hold()
	held, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.society.Waker().Wake(held, "dev", core.WakeEvent, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("wake before the machine is ready: %v", err)
	}
	e.fakes.Startup.Open()
	if err := e.society.Waker().Wake(context.Background(), "dev", core.WakeEvent, nil); err != nil {
		t.Fatalf("wake after the machine is ready: %v", err)
	}
}

func TestWaker_Refusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		agent   string
		prepare func(t *testing.T, e env)
		wantErr error
	}{
		{"awake agent", "dev", func(*testing.T, env) {}, nil},
		{"retired agent", "intern", func(t *testing.T, e env) {
			if err := e.society.Retire(asAgent("lead"), "intern"); err != nil {
				t.Fatal(err)
			}
		}, domain.ErrRetired},
		{"agent in a frozen team", "intern", func(t *testing.T, e env) {
			if err := e.society.Freezer().Freeze(asHost(), scope(t, "team:lead")); err != nil {
				t.Fatal(err)
			}
		}, core.ErrFrozen},
		{"unknown agent", "ghost", func(*testing.T, env) {}, domain.ErrAgentNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			tt.prepare(t, e)
			err := e.society.Waker().Wake(context.Background(), tt.agent, core.WakeEvent, nil)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Wake() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestWakeAgent_DeliversTheReasonAsATaskOrMessage(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	if _, err := e.society.WakeAgent(asAgent("watcher"), "dev", app.WakeRequest{Reason: "Disk is full"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.society.WakeAgent(asAgent("watcher"), "dev", app.WakeRequest{Reason: "Fix it", TaskID: "tsk_1"}); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]core.WakeReason{core.WakeMessage, core.WakeTask}, e.rt.Wakes()); diff != "" {
		t.Fatalf("wakes (-want +got):\n%s", diff)
	}
}

func TestWakeAgent_Refusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		ctx     context.Context
		reason  string
		wantErr error
	}{
		{"human", asHuman("ada"), "hi", core.ErrForbidden},
		{"no caller", context.Background(), "hi", core.ErrUnauthorized},
		{"empty reason", asAgent("lead"), "", core.ErrInvalid},
		{"reason of 501 characters", asAgent("lead"), strings.Repeat("r", 501), core.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			_, err := e.society.WakeAgent(tt.ctx, "dev", app.WakeRequest{Reason: tt.reason})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("WakeAgent() error = %v, want %v", err, tt.wantErr)
			}
			if got := e.rt.Wakes(); len(got) != 0 {
				t.Errorf("woke %v", got)
			}
		})
	}
}

func TestSleep_SelfOrBelow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, actor, agent string
		wantErr            error
	}{
		{"itself", "dev", "dev", nil},
		{"a report", "lead", "intern", nil},
		{"its manager", "dev", "lead", domain.ErrNotBelowActor},
		{"unknown agent", "dev", "ghost", domain.ErrAgentNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			_, err := e.society.Sleep(asAgent(tt.actor), tt.agent, true)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Sleep() error = %v, want %v", err, tt.wantErr)
			}
			if want := tt.wantErr == nil; (len(e.rt.Slept()) == 1) != want {
				t.Errorf("slept %v", e.rt.Slept())
			}
		})
	}
}

func TestFreezer_FreezesAndReleasesATeam(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	if err := e.society.Freezer().Freeze(asHost(), scope(t, "team:lead")); err != nil {
		t.Fatal(err)
	}
	states := func() []domain.State {
		var out []domain.State
		for _, n := range []string{"lead", "dev", "intern", "ops"} {
			a, _ := e.repo.GetAgent(ctx, n)
			out = append(out, a.State)
		}
		return out
	}
	frozen := domain.StateFrozen
	if diff := cmp.Diff([]domain.State{frozen, frozen, frozen, domain.StateSleeping}, states()); diff != "" {
		t.Fatalf("states after freeze (-want +got):\n%s", diff)
	}
	if !e.rt.Frozen("team:lead") {
		t.Error("runtime did not freeze team:lead")
	}
	active, _ := e.society.Freezer().State(ctx)
	if len(active) != 1 || active[0].String() != "team:lead" {
		t.Errorf("State() = %v", active)
	}

	if err := e.society.Freezer().Release(asHost(), scope(t, "team:lead")); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]domain.State{domain.StateSleeping, domain.StateSleeping, domain.StateSleeping, domain.StateSleeping}, states()); diff != "" {
		t.Fatalf("states after release (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"agent.freeze", "agent.release"}, e.auditActions()); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
}

func TestFreezer_ReleaseKeepsAgentsAnotherScopeStillCovers(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, s := range []string{"all", "agent:dev"} {
		if err := e.society.Freezer().Freeze(asHost(), scope(t, s)); err != nil {
			t.Fatal(err)
		}
	}

	if err := e.society.Freezer().Release(asHost(), scope(t, "all")); err != nil {
		t.Fatal(err)
	}

	dev, _ := e.repo.GetAgent(context.Background(), "dev")
	lead, _ := e.repo.GetAgent(context.Background(), "lead")
	if dev.State != domain.StateFrozen || lead.State != domain.StateSleeping {
		t.Fatalf("dev %s, lead %s; want frozen, sleeping", dev.State, lead.State)
	}
}

func TestFreezer_Refusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		ctx     context.Context
		scope   core.FreezeScope
		wantErr error
	}{
		{"agent pulls the lever", asAgent("founder"), core.FreezeScope{Kind: core.FreezeAll}, core.ErrForbidden},
		{"human pulls the lever", asHuman("ada"), core.FreezeScope{Kind: core.FreezeAll}, core.ErrForbidden},
		{"unknown agent", asHost(), core.FreezeScope{Kind: core.FreezeAgent, Target: "ghost"}, domain.ErrAgentNotFound},
		{"bad scope", asHost(), core.FreezeScope{Kind: "planet"}, core.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			err := e.society.Freezer().Freeze(tt.ctx, tt.scope)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Freeze() error = %v, want %v", err, tt.wantErr)
			}
			if active, _ := e.society.Freezer().State(context.Background()); len(active) != 0 {
				t.Errorf("active freezes %v", active)
			}
			e.assertNothingHappened(t)
		})
	}
}

func TestOneLine_OnlyTheAgentSetsItsOwnLine(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	got, err := e.society.SetOneLine(asAgent("dev"), "dev", "Goal: docs. Now: layout.")
	if err != nil {
		t.Fatal(err)
	}
	if got.Line != "Goal: docs. Now: layout." {
		t.Fatalf("line %+v", got)
	}
	if _, err := e.society.SetOneLine(asAgent("lead"), "dev", "x"); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("manager sets report's line: %v, want %v", err, core.ErrForbidden)
	}
	if read, err := e.society.OneLine(asHuman("ada"), "dev"); err != nil || read.Line != got.Line {
		t.Fatalf("OneLine() = %+v %v", read, err)
	}
	if _, err := e.society.OneLine(asHuman("ada"), "lead"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("no line yet: %v, want %v", err, core.ErrNotFound)
	}
	if _, err := e.society.SetOneLine(asAgent("ghost"), "ghost", "x"); !errors.Is(err, domain.ErrAgentNotFound) {
		t.Fatalf("unknown agent: %v", err)
	}
}

func TestOutput_IgnoresAnEmptyNonFinalChatChunk(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	if err := e.society.Output(context.Background(), "dev", app.OutputChunk{TurnID: "t", ChatID: "c"}); err != nil {
		t.Fatal(err)
	}

	if list, _ := e.repo.ListChat(context.Background(), "dev", "", 10); len(list) != 0 {
		t.Fatalf("stored %v", list)
	}
}
