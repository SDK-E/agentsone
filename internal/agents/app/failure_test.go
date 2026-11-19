// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/agents/adapters/memory"
	"github.com/SDK-E/agentsone/internal/agents/agentstest"
	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

var errDisk = errors.New("disk failed")

// brokenEnv returns a seeded society whose repository fails the named methods.
func brokenEnv(t *testing.T, methods ...string) (*app.Society, *coretest.Fakes) {
	t.Helper()
	fakes, ports := coretest.NewFakes()
	repo := memory.New()
	for _, a := range agentstest.Society() {
		if err := repo.CreateAgent(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	g := domain.Goal{ID: "gol_01", Owner: "dev", Title: "Ship", Status: domain.GoalOpen}
	if err := repo.CreateGoal(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	fail := map[string]bool{}
	for _, m := range methods {
		fail[m] = true
	}
	broken := agentstest.Broken{Repository: repo, Err: errDisk, Fail: fail}
	return app.New(broken, agentstest.NewRuntime(), ports, fakes.Clock, fakes.IDs), fakes
}

func TestUseCases_ReportStorageFailuresAndPublishNothing(t *testing.T) {
	t.Parallel()
	title := "New"
	amount := int64(0) // the seeded society has no pushed total yet
	runner := "openai"
	tests := []struct {
		name   string
		method string
		call   func(s *app.Society) error
	}{
		{"hire reads the chart", "ListAgents", func(s *app.Society) error { _, err := s.Hire(asAgent("lead"), spec("mira")); return err }},
		{"found reads the chart", "ListAgents", func(s *app.Society) error {
			_, err := s.Found(core.WithCaller(context.Background(), core.Caller{Kind: core.CallerSystem, Name: "seed"}), spec("x"))
			return err
		}},
		{"update reads the chart", "ListAgents", func(s *app.Society) error {
			_, err := s.Update(asAgent("lead"), "dev", domain.Update{Role: &title})
			return err
		}},
		{"update saves", "SaveAgents", func(s *app.Society) error {
			_, err := s.Update(asAgent("lead"), "dev", domain.Update{Runner: &runner})
			return err
		}},
		{"retire reads the chart", "ListAgents", func(s *app.Society) error { return s.Retire(asAgent("lead"), "dev") }},
		{"retire saves", "SaveAgents", func(s *app.Society) error { return s.Retire(asAgent("lead"), "dev") }},
		{"move reads the chart", "ListAgents", func(s *app.Society) error { return s.Move(asAgent("founder"), "dev", "ops") }},
		{"move saves", "SaveAgents", func(s *app.Society) error { return s.Move(asAgent("founder"), "dev", "ops") }},
		{"list reads the chart", "ListAgents", func(s *app.Society) error { _, err := s.List(asHuman("ada"), app.ListFilter{}); return err }},
		{"directory lists", "ListAgents", func(s *app.Society) error { _, err := s.Directory().ListAgents(context.Background()); return err }},
		{"directory checks below", "ListAgents", func(s *app.Society) error {
			_, err := s.Directory().IsBelow(context.Background(), "dev", "lead")
			return err
		}},
		{"directory finds the team", "ListAgents", func(s *app.Society) error {
			_, err := s.Directory().TeamRoot(context.Background(), "dev")
			return err
		}},
		{"sleep reads the chart", "ListAgents", func(s *app.Society) error { _, err := s.Sleep(asAgent("dev"), "dev", true); return err }},
		{"charter reads", "Charter", func(s *app.Society) error { _, err := s.RewriteCharter(asAgent("founder"), 0, "x"); return err }},
		{"charter reads the chart", "ListAgents", func(s *app.Society) error {
			_, err := s.RewriteCharter(asAgent("founder"), 0, "x")
			return err
		}},
		{"charter saves", "SaveCharter", func(s *app.Society) error { _, err := s.RewriteCharter(asAgent("founder"), 0, "x"); return err }},
		{"goal set reads the chart", "ListAgents", func(s *app.Society) error {
			_, err := s.SetGoal(asAgent("dev"), domain.GoalSpec{Title: "x"})
			return err
		}},
		{"goal edit saves", "SaveGoal", func(s *app.Society) error {
			_, err := s.UpdateGoal(asAgent("dev"), "gol_01", domain.GoalUpdate{Title: &title})
			return err
		}},
		{"goal edit reads the chart", "ListAgents", func(s *app.Society) error { return s.DeleteGoal(asAgent("dev"), "gol_01") }},
		{"wake reads the pool", "Pool", func(s *app.Society) error { return s.Waker().Wake(context.Background(), "dev", core.WakeEvent, nil) }},
		{"wake reads allocations", "Allocations", func(s *app.Society) error { return s.Waker().Wake(context.Background(), "dev", core.WakeEvent, nil) }},
		{"wake reads usage", "Usage", func(s *app.Society) error { return s.Waker().Wake(context.Background(), "dev", core.WakeEvent, nil) }},
		{"wake reads the chart", "ListAgents", func(s *app.Society) error { return s.Waker().Wake(context.Background(), "dev", core.WakeEvent, nil) }},
		{"pool reads", "Pool", func(s *app.Society) error { _, err := s.Budget().Pool(context.Background()); return err }},
		{"allocation reads", "Pool", func(s *app.Society) error { _, err := s.Budget().Allocation(context.Background(), "dev"); return err }},
		{"allocate reads", "Pool", func(s *app.Society) error {
			_, err := s.Budget().Allocate(asAgent("founder"), "lead", &amount)
			return err
		}},
		{"allocate saves", "SetAllocation", func(s *app.Society) error {
			_, err := s.Budget().Allocate(asAgent("founder"), "lead", &amount)
			return err
		}},
		{"chat stores", "AppendChat", func(s *app.Society) error { _, err := s.SendChat(asHuman("ada"), "dev", "hi"); return err }},
		{"reply stores", "AppendChat", func(s *app.Society) error {
			return s.Output(context.Background(), "dev", app.OutputChunk{TurnID: "t", ChatID: "c", Text: "hi", Final: true})
		}},
		{"freeze state reads", "Freezes", func(s *app.Society) error { _, err := s.Freezer().State(context.Background()); return err }},
		{"freeze saves", "AddFreeze", func(s *app.Society) error {
			return s.Freezer().Freeze(asHost(), core.FreezeScope{Kind: core.FreezeAll})
		}},
		{"freeze reads the chart", "ListAgents", func(s *app.Society) error {
			return s.Freezer().Freeze(asHost(), core.FreezeScope{Kind: core.FreezeAll})
		}},
		{"freeze syncs states", "Freezes", func(s *app.Society) error {
			return s.Freezer().Freeze(asHost(), core.FreezeScope{Kind: core.FreezeAll})
		}},
		{"freeze saves states", "SaveAgents", func(s *app.Society) error {
			return s.Freezer().Freeze(asHost(), core.FreezeScope{Kind: core.FreezeAll})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, fakes := brokenEnv(t, tt.method)
			if err := tt.call(s); !errors.Is(err, errDisk) {
				t.Fatalf("error = %v, want %v", err, errDisk)
			}
			if got := fakes.Events.Published(); len(got) != 0 {
				t.Errorf("published %d events after a storage failure", len(got))
			}
		})
	}
}

func TestUseCases_ReadUnknownThings(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.society.Chat(asHuman("ada"), "ghost", "", 10); !errors.Is(err, domain.ErrAgentNotFound) {
		t.Errorf("Chat(ghost) error = %v", err)
	}
	if _, err := e.society.Directory().GetAgent(ctx, "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("GetAgent(ghost) error = %v", err)
	}
	if _, err := e.society.Directory().IsBelow(ctx, "ghost", "founder"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("IsBelow(ghost) error = %v", err)
	}
	if _, err := e.society.Directory().TeamRoot(ctx, "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("TeamRoot(ghost) error = %v", err)
	}
	if _, err := e.society.OneLine(ctx, "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("OneLine(ghost) error = %v", err)
	}
	if _, err := e.society.Update(asAgent("lead"), "ghost", domain.Update{}); !errors.Is(err, domain.ErrAgentNotFound) {
		t.Errorf("Update(ghost) error = %v", err)
	}
	if _, err := e.society.UpdateGoal(asAgent("lead"), "gol_missing", domain.GoalUpdate{}); !errors.Is(err, domain.ErrGoalNotFound) {
		t.Errorf("UpdateGoal(missing) error = %v", err)
	}
	if err := e.society.Move(asHuman("ada"), "dev", "ops"); !errors.Is(err, core.ErrForbidden) {
		t.Errorf("Move by a human error = %v", err)
	}
	if err := e.society.Retire(asHuman("ada"), "dev"); !errors.Is(err, core.ErrForbidden) {
		t.Errorf("Retire by a human error = %v", err)
	}
	if _, err := e.society.Update(asHuman("ada"), "dev", domain.Update{}); !errors.Is(err, core.ErrForbidden) {
		t.Errorf("Update by a human error = %v", err)
	}
	if _, err := e.society.Sleep(asHuman("ada"), "dev", true); !errors.Is(err, core.ErrForbidden) {
		t.Errorf("Sleep by a human error = %v", err)
	}
	if _, err := e.society.SetOneLine(asHuman("ada"), "dev", "x"); !errors.Is(err, core.ErrForbidden) {
		t.Errorf("SetOneLine by a human error = %v", err)
	}
}

func TestUpdate_RefusesAnInvalidEdit(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	empty := ""

	_, err := e.society.Update(asAgent("lead"), "dev", domain.Update{Role: &empty})

	if !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("error = %v, want %v", err, domain.ErrInvalidAgent)
	}
	e.assertNothingHappened(t)
}
