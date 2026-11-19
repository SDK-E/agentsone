// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/events/app"
	"github.com/SDK-E/agentsone/internal/events/domain"
)

// publishScopes stores one machine event, one for the builder team (founder
// root), one for agent:intern and one for another team.
func publishScopes(t *testing.T, f *fixture) {
	t.Helper()
	for _, d := range []domain.Draft{
		diskEvent(),
		{Type: "agentsone.agents.hired", Source: "/emitters/agents", Data: json.RawMessage(`{"agent":"builder"}`)},
		{Type: "agentsone.agents.woke", Source: "/emitters/agents", Data: json.RawMessage(`{"agent":"intern"}`)},
		{Type: "agentsone.agents.hired", Source: "/emitters/agents", Data: json.RawMessage(`{"agent":"outsider"}`)},
	} {
		if _, err := f.svc.PublishDraft(asSystem(), d); err != nil {
			t.Fatal(err)
		}
	}
}

func scopes(events []core.Event) []string {
	var out []string
	for _, e := range events {
		out = append(out, e.ChartScope)
	}
	return out
}

func TestList_ShowsEachCallerOnlyWhatTheyMaySee(t *testing.T) {
	f := newFixture(t)
	publishScopes(t, f)
	tests := []struct {
		name string
		ctx  context.Context
		want []string
	}{
		{"human sees all", asHuman("hsaddek"), []string{"machine", "team:founder", "agent:intern", "team:outsider"}},
		{"builder sees its team and the agent below", asAgent("builder"), []string{"machine", "team:founder", "agent:intern"}},
		{"outsider sees only its team", asAgent("outsider"), []string{"machine", "team:outsider"}},
		{"intern sees its own agent scope", asAgent("intern"), []string{"machine", "team:founder", "agent:intern"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := f.svc.List(tt.ctx, app.Query{Limit: 50})

			if err != nil {
				t.Fatal(err)
			}
			got := scopes(page.Events)
			if len(got) != len(tt.want) {
				t.Fatalf("scopes = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("scopes = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestList_PagesByLimitAndFilters(t *testing.T) {
	f := newFixture(t)
	publishScopes(t, f)

	first, err := f.svc.List(asHuman("h"), app.Query{Limit: 2})
	if err != nil || len(first.Events) != 2 || first.Next != first.Events[1].Sequence {
		t.Fatalf("first page = %d events, next %q, %v", len(first.Events), first.Next, err)
	}
	after, _ := domain.ParseSequence(first.Next)
	second, err := f.svc.List(asHuman("h"), app.Query{Limit: 2, After: after})
	if err != nil || len(second.Events) != 2 || second.Next != "" {
		t.Fatalf("second page = %d events, next %q, %v", len(second.Events), second.Next, err)
	}
	woke, err := f.svc.List(asHuman("h"), app.Query{Limit: 10, Filter: domain.Filter{Types: []string{"agentsone.agents.woke"}}})
	if err != nil || len(woke.Events) != 1 {
		t.Fatalf("filtered = %d events, %v", len(woke.Events), err)
	}
}

func recv(t *testing.T, ch <-chan core.Event) core.Event {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatal("stream closed")
		}
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event within 5s")
	}
	return core.Event{}
}

func TestStream_LiveEventsOnly_WithoutAResumePoint(t *testing.T) {
	f := newFixture(t)
	publishScopes(t, f)
	ctx, cancel := context.WithCancel(asHuman("h"))
	defer cancel()
	ch, err := f.svc.Stream(ctx, domain.Filter{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	live, err := f.svc.PublishDraft(asSystem(), diskEvent())
	if err != nil {
		t.Fatal(err)
	}

	if got := recv(t, ch); got.ID != live.ID {
		t.Fatalf("first streamed event = %s, want the live one %s", got.ID, live.ID)
	}
}

func TestStream_ReplaysAfterTheResumePointThenGoesLive(t *testing.T) {
	f := newFixture(t)
	publishScopes(t, f)
	ctx, cancel := context.WithCancel(asAgent("outsider"))
	defer cancel()
	after := uint64(1)
	ch, err := f.svc.Stream(ctx, domain.Filter{}, &after)
	if err != nil {
		t.Fatal(err)
	}

	// Events 2 (team:founder) and 3 (agent:intern) are hidden from the outsider.
	if got := recv(t, ch); got.Sequence != "00000000000000000004" {
		t.Fatalf("replayed event = %s, want sequence 4", got.Sequence)
	}
	live, _ := f.svc.PublishDraft(asSystem(), diskEvent())
	if got := recv(t, ch); got.ID != live.ID {
		t.Fatalf("live event = %s, want %s", got.ID, live.ID)
	}
}

func TestStream_ClosesWhenTheContextEnds(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(asHuman("h"))
	ch, err := f.svc.Stream(ctx, domain.Filter{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("got an event after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream stayed open after cancel")
	}
}

func TestStream_ASlowSubscriberIsClosedAndCanResumeWithoutLoss(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(asHuman("h"))
	defer cancel()
	ch, err := f.svc.Stream(ctx, domain.Filter{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const total = 600
	for range total {
		if _, err := f.svc.PublishDraft(asSystem(), diskEvent()); err != nil {
			t.Fatal(err)
		}
	}

	var lastSeen uint64
	received := 0
	for e := range ch {
		lastSeen, _ = domain.ParseSequence(e.Sequence)
		received++
	}

	if received >= total {
		t.Fatalf("received %d of %d: the slow subscriber was never closed", received, total)
	}
	resumed, err := f.svc.Stream(ctx, domain.Filter{}, &lastSeen)
	if err != nil {
		t.Fatal(err)
	}
	for range total - received {
		recv(t, resumed)
	}
}

func TestSubscribe_FiltersByTypePrefixAndSource(t *testing.T) {
	f := newFixture(t)
	publishScopes(t, f)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, err := f.svc.Subscribe(ctx, core.EventFilter{Types: []string{"agentsone.agents."}, Source: "/emitters/agents"})
	if err != nil {
		t.Fatal(err)
	}

	for range 3 {
		if got := recv(t, ch); got.Source != "/emitters/agents" {
			t.Fatalf("event = %+v", got)
		}
	}
}

func TestSubscribe_ReplaysAfterASequenceAndRejectsAnInvalidOne(t *testing.T) {
	f := newFixture(t)
	publishScopes(t, f)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, err := f.svc.Subscribe(ctx, core.EventFilter{AfterSequence: core.FormatSequence(3)})
	if err != nil {
		t.Fatal(err)
	}
	if got := recv(t, ch); got.Sequence != core.FormatSequence(4) {
		t.Fatalf("first replayed = %s, want sequence 4", got.Sequence)
	}
	if _, err := f.svc.Subscribe(ctx, core.EventFilter{AfterSequence: "x"}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("Subscribe() with a bad sequence error = %v, want ErrInvalid", err)
	}
}
