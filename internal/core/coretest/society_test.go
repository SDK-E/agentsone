// SPDX-License-Identifier: MIT

package coretest_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

func TestTasksComplete(t *testing.T) {
	ctx := context.Background()
	ts := coretest.NewTasks(coretest.NewClock(start), coretest.NewIDs())
	task, _ := ts.CreateTask(ctx, core.NewTask{Title: "Ship", Assignee: "dev"})
	done, err := ts.CompleteTask(ctx, task.ID, core.TaskOutcome{Status: core.TaskDone, Result: "shipped"})
	if err != nil || done.Status != core.TaskDone || done.ID != task.ID || done.Result != "shipped" {
		t.Fatalf("complete %+v %v", done, err)
	}
	if got, _ := ts.GetTask(ctx, task.ID); got.Status != core.TaskDone || got.Result != "shipped" {
		t.Fatalf("stored %+v", got)
	}
	if got := ts.Outcome(task.ID); got.Result != "shipped" {
		t.Fatalf("outcome %+v", got)
	}
	if _, err := ts.CompleteTask(ctx, task.ID, core.TaskOutcome{Status: "open"}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bad outcome: %v", err)
	}
	if _, err := ts.CompleteTask(ctx, "tsk_ghost", core.TaskOutcome{Status: core.TaskDone}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown task: %v", err)
	}
}

func TestTasksCreateKeepsFields(t *testing.T) {
	ctx := context.Background()
	ts := coretest.NewTasks(coretest.NewClock(start), coretest.NewIDs())
	high, err := ts.CreateTask(ctx, core.NewTask{Title: "Patch", Assignee: "dev", CreatedBy: "agent:lead", Priority: core.TaskPriorityHigh})
	if err != nil || high.Priority != core.TaskPriorityHigh || high.CreatedBy != "agent:lead" {
		t.Fatalf("create %+v %v", high, err)
	}
	plain, _ := ts.CreateTask(ctx, core.NewTask{Title: "Tidy"})
	if plain.Priority != core.TaskPriorityNormal {
		t.Fatalf("default priority %q", plain.Priority)
	}
	if _, err := ts.CreateTask(ctx, core.NewTask{Title: "x", Priority: "urgent"}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bad priority: %v", err)
	}
	got, err := ts.GetTask(ctx, high.ID)
	if err != nil || got != high {
		t.Fatalf("get %+v %v", got, err)
	}
	if _, err := ts.GetTask(ctx, "tsk_ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestWakerFrozenAgent(t *testing.T) {
	w := coretest.NewWaker()
	w.Freeze("dev")
	if err := w.Wake(context.Background(), "dev", core.WakeMessage, nil); !errors.Is(err, core.ErrFrozen) {
		t.Fatalf("frozen wake: %v", err)
	}
	if len(w.Wakes()) != 0 {
		t.Fatal("frozen wake recorded")
	}
	if err := w.Wake(context.Background(), "lead", core.WakeMessage, nil); err != nil {
		t.Fatalf("other agent: %v", err)
	}
}

func TestBusSend(t *testing.T) {
	ctx := context.Background()
	b := coretest.NewBus(coretest.NewClock(start), coretest.NewIDs())
	sent, err := b.Send(ctx, core.BusMessage{From: "agent:founder", To: "agent:dev", Body: "hi"})
	if err != nil || !strings.HasPrefix(sent.ID, "msg_") || !sent.Sent.Equal(start) {
		t.Fatalf("sent %+v %v", sent, err)
	}
	if _, err := b.Send(ctx, core.BusMessage{From: "agent:founder", To: "agent:dev"}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("empty body: %v", err)
	}
	if got := b.Sent(); len(got) != 1 || got[0].Body != "hi" {
		t.Fatalf("stored %+v", got)
	}
}

func TestFreezer(t *testing.T) {
	ctx := context.Background()
	f := coretest.NewFreezer()
	team := core.FreezeScope{Kind: core.FreezeTeam, Target: "lead"}
	all := core.FreezeScope{Kind: core.FreezeAll}
	for _, s := range []core.FreezeScope{team, all, team} {
		if err := f.Freeze(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	state, _ := f.State(ctx)
	if len(state) != 2 || state[0] != all || state[1] != team {
		t.Fatalf("state %+v, want [all team:lead] without duplicates", state)
	}
	if err := f.Release(ctx, all); err != nil {
		t.Fatal(err)
	}
	if state, _ := f.State(ctx); len(state) != 1 || state[0] != team {
		t.Fatalf("after release %+v", state)
	}
	if err := f.Release(ctx, all); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("release of inactive freeze: %v", err)
	}
	if err := f.Freeze(ctx, core.FreezeScope{Kind: "pack"}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bad scope: %v", err)
	}
}

func TestTokens(t *testing.T) {
	ctx := context.Background()
	clock := coretest.NewClock(start)
	tk := coretest.NewTokens(clock, coretest.NewIDs())
	who := core.Caller{Kind: core.CallerHuman, Name: "ada", UID: 1000}
	tok, err := tk.Issue(ctx, who, []string{"chat"}, time.Hour)
	if err != nil || tok.Value == "" || !tok.Expires.Equal(start.Add(time.Hour)) {
		t.Fatalf("issue %+v %v", tok, err)
	}
	got, err := tk.Verify(ctx, tok.Value)
	if err != nil || got.Name != "ada" || !got.Has("chat") || got.Has("budget") {
		t.Fatalf("verify %+v %v", got, err)
	}
	if _, err := tk.Verify(ctx, "nope"); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("unknown token: %v", err)
	}
	clock.Advance(time.Hour)
	if _, err := tk.Verify(ctx, tok.Value); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("expired token: %v", err)
	}
	if _, err := tk.Issue(ctx, who, nil, 0); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("zero ttl: %v", err)
	}
}

func TestTemplates(t *testing.T) {
	ctx := context.Background()
	c := coretest.NewTemplates()
	c.Add(core.AgentTemplate{ID: "tpl-waker", Name: "Waker", Kind: core.AgentWaker, Raw: json.RawMessage(`{}`)})
	c.Add(core.AgentTemplate{ID: "tpl-founder", Name: "Founder", IsRoot: true})
	list, err := c.List(ctx)
	if err != nil || len(list) != 2 || list[0].ID != "tpl-founder" {
		t.Fatalf("list %+v %v", list, err)
	}
	got, err := c.Get(ctx, "tpl-waker")
	if err != nil || got.Kind != core.AgentWaker {
		t.Fatalf("get %+v %v", got, err)
	}
	if _, err := c.Get(ctx, "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestFakesFillNewPorts(t *testing.T) {
	f, p := coretest.NewFakes()
	if p.Bus != f.Bus || p.Freezer != f.Freezer || p.Tokens != f.Tokens || p.Templates != f.Templates ||
		p.Profiles != f.Profiles || p.Chat != f.Chat || p.Seed != f.Seed {
		t.Fatal("new ports not wired to fakes")
	}
}

func TestHumans_FindsWhatWasAdded(t *testing.T) {
	f, p := coretest.NewFakes()
	if p.Humans != f.Humans {
		t.Fatal("humans port not wired to the fake")
	}
	ctx := context.Background()
	f.Humans.Add("ada", 1000)
	all, err := p.Humans.HumanAccounts(ctx)
	if err != nil || len(all) != 1 || all[0] != (core.HumanAccount{Name: "ada", UID: 1000}) {
		t.Fatalf("humans %+v %v", all, err)
	}
	if h, err := p.Humans.HumanAccount(ctx, "ada"); err != nil || h.UID != 1000 {
		t.Fatalf("ada %+v %v", h, err)
	}
	if _, err := p.Humans.HumanAccount(ctx, "eve"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("eve: %v", err)
	}
}

func TestSeed_ReportsWhatItIsSet(t *testing.T) {
	s := coretest.NewSeed()
	if got, err := s.SeedInstalled(context.Background()); err != nil || got {
		t.Fatalf("new seed = %v, %v, want not installed", got, err)
	}
	s.SetInstalled(true)
	if got, _ := s.SeedInstalled(context.Background()); !got {
		t.Fatal("seed not installed after SetInstalled(true)")
	}
}

func TestProfiles_ListsSortedByName(t *testing.T) {
	p := coretest.NewProfiles()
	p.Add(core.AgentProfile{Name: "router", Role: "Router", Kind: core.AgentChat, State: "sleeping", Team: "router", Template: "small-seed/router"})
	p.Add(core.AgentProfile{Name: "founder", Kind: core.AgentAlwaysOn, State: "awake", Team: "founder"})

	got, err := p.Profiles(context.Background())

	if err != nil || len(got) != 2 || got[0].Name != "founder" || got[1].Template != "small-seed/router" {
		t.Fatalf("profiles %+v %v", got, err)
	}
}

func TestChat_RecordsPostsWithTheCallerAsAuthor(t *testing.T) {
	c := coretest.NewChat(coretest.NewIDs())
	ctx := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerHuman, Name: "ada"})

	id, err := c.PostChat(ctx, "dev", "Please fix the login", "asg_1")
	if err != nil || !strings.HasPrefix(id, "cht_") {
		t.Fatalf("PostChat() = %q %v", id, err)
	}

	want := coretest.ChatPost{ID: id, Agent: "dev", Text: "Please fix the login", AssignmentID: "asg_1", Author: "human:ada"}
	if got := c.Posts(); len(got) != 1 || got[0] != want {
		t.Fatalf("posts %+v, want %+v", got, want)
	}
	if _, err := c.PostChat(ctx, "dev", "", ""); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("empty text error = %v, want %v", err, core.ErrInvalid)
	}
}
