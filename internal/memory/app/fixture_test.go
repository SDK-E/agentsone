// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/memory/adapters/memory"
	"github.com/SDK-E/agentsone/internal/memory/app"
	"github.com/SDK-E/agentsone/internal/memory/domain"
)

// The chart: team "builder" is builder with scribe below it; "keeper" is a team
// of one; "founder" is a root agent. They stand in separate trees so the
// coretest directory and the real one give the same team heads.
type fixture struct {
	svc   *app.Service
	fakes *coretest.Fakes
	repo  *memory.Repository
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	fakes, ports := coretest.NewFakes()
	fakes.Agents.Add(core.Agent{Name: "builder", Kind: core.AgentAlwaysOn})
	fakes.Agents.Add(core.Agent{Name: "scribe", Kind: core.AgentChat, Manager: "builder"})
	fakes.Agents.Add(core.Agent{Name: "keeper", Kind: core.AgentAlwaysOn})
	fakes.Agents.Add(core.Agent{Name: "founder", Kind: core.AgentAlwaysOn, IsRoot: true})
	repo := memory.New()
	return &fixture{svc: app.New(repo, ports, fakes.Clock, fakes.IDs), fakes: fakes, repo: repo}
}

func agent(name string) context.Context {
	return core.WithCaller(context.Background(), core.Caller{
		Kind: core.CallerAgent, Name: name, UID: -1, IsRoot: name == "founder",
	})
}

func caller(c core.Caller) context.Context { return core.WithCaller(context.Background(), c) }

func (f *fixture) write(t *testing.T, as string, scope, title string) domain.Record {
	t.Helper()
	rec, err := f.svc.Write(agent(as), app.WriteInput{Scope: scope, Title: title, Body: "body of " + title})
	if err != nil {
		t.Fatalf("write %q as %s: %v", title, as, err)
	}
	return rec
}

func titles(records []domain.Record) []string {
	out := make([]string, 0, len(records))
	for _, r := range records {
		out = append(out, r.Title)
	}
	return out
}
