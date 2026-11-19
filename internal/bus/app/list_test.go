// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/bus/app"
	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// seed stores five messages, one second apart, and returns them oldest first:
//
//	g1 builder -> #general        d1 builder -> intern     t1 builder -> #team.founder
//	d2 outsider -> builder        d3 intern -> founder
func seed(t *testing.T, f *fixture) []domain.Message {
	t.Helper()
	mustChannel(asAgent("builder"), t, f, channel("general"))
	mustChannel(asAgent("builder"), t, f, teamChannel("team.founder"))
	var out []domain.Message
	for _, s := range []struct {
		from string
		d    domain.Draft
	}{
		{"builder", toChannel("general", "g1")},
		{"builder", toAgent("intern", "d1")},
		{"builder", toChannel("team.founder", "t1")},
		{"outsider", toAgent("builder", "d2")},
		{"intern", toAgent("founder", "d3")},
	} {
		f.clock.Advance(time.Second)
		out = append(out, post(asAgent(s.from), t, f, s.d))
	}
	return out
}

func TestListMessages_ShowsEachCallerOnlyWhatTheyMaySee(t *testing.T) {
	f := newFixture(t)
	seed(t, f)
	tests := []struct {
		name string
		ctx  func() ([]domain.Message, error)
		want []string
	}{
		{"human sees all", list(asHuman("ada"), f), []string{"d3", "d2", "t1", "d1", "g1"}},
		{"manager sees the messages of the team below", list(asAgent("builder"), f), []string{"d3", "d2", "t1", "d1", "g1"}},
		{"intern sees its own and the team channel", list(asAgent("intern"), f), []string{"d3", "t1", "d1", "g1"}},
		{"outsider sees its own and public channels", list(asAgent("outsider"), f), []string{"d2", "g1"}},
		{"plugin sees public channels only", list(asPlugin(), f), []string{"g1"}},
	}
	for _, tt := range tests {
		got, err := tt.ctx()
		if err != nil || !slices.Equal(texts(got), tt.want) {
			t.Errorf("%s: texts = %v, %v, want %v", tt.name, texts(got), err, tt.want)
		}
	}
}

func list(ctx context.Context, f *fixture) func() ([]domain.Message, error) {
	return func() ([]domain.Message, error) {
		page, err := f.svc.ListMessages(ctx, app.Query{Limit: 50})
		return page.Messages, err
	}
}

func TestListMessages_PagesNewestFirstAndSkipsWhatIsHidden(t *testing.T) {
	f := newFixture(t)
	msgs := seed(t, f)

	first, err := f.svc.ListMessages(asHuman("ada"), app.Query{Limit: 2})
	if err != nil || !slices.Equal(texts(first.Messages), []string{"d3", "d2"}) || first.Next != msgs[3].Sequence {
		t.Fatalf("first page = %v, next %d, %v", texts(first.Messages), first.Next, err)
	}
	second, err := f.svc.ListMessages(asHuman("ada"), app.Query{Limit: 2, Before: first.Next})
	if err != nil || !slices.Equal(texts(second.Messages), []string{"t1", "d1"}) || second.Next != msgs[1].Sequence {
		t.Fatalf("second page = %v, next %d, %v", texts(second.Messages), second.Next, err)
	}
	last, err := f.svc.ListMessages(asHuman("ada"), app.Query{Limit: 2, Before: second.Next})
	if err != nil || !slices.Equal(texts(last.Messages), []string{"g1"}) || last.Next != 0 {
		t.Fatalf("last page = %v, next %d, %v", texts(last.Messages), last.Next, err)
	}

	hidden, err := f.svc.ListMessages(asAgent("outsider"), app.Query{Limit: 1})
	if err != nil || !slices.Equal(texts(hidden.Messages), []string{"d2"}) || hidden.Next != msgs[3].Sequence {
		t.Fatalf("outsider first page = %v, next %d, %v", texts(hidden.Messages), hidden.Next, err)
	}
	rest, err := f.svc.ListMessages(asAgent("outsider"), app.Query{Limit: 1, Before: hidden.Next})
	if err != nil || !slices.Equal(texts(rest.Messages), []string{"g1"}) || rest.Next != 0 {
		t.Fatalf("outsider second page = %v, next %d, %v", texts(rest.Messages), rest.Next, err)
	}
}

func TestListMessages_Filters(t *testing.T) {
	f := newFixture(t)
	msgs := seed(t, f)
	since := msgs[2].CreatedAt
	tests := []struct {
		name string
		q    app.Query
		want []string
	}{
		{"channel", app.Query{Filter: domain.MessageFilter{Channel: "general"}}, []string{"g1"}},
		{"recipient", app.Query{Filter: domain.MessageFilter{To: "intern"}}, []string{"d1"}},
		{"sender", app.Query{Filter: domain.MessageFilter{From: "agent:builder"}}, []string{"t1", "d1", "g1"}},
		{"since", app.Query{Filter: domain.MessageFilter{Since: &since}}, []string{"d3", "d2", "t1"}},
		{"channel and recipient never match", app.Query{Filter: domain.MessageFilter{Channel: "general", To: "intern"}}, nil},
	}
	for _, tt := range tests {
		tt.q.Limit = 50
		page, err := f.svc.ListMessages(asHuman("ada"), tt.q)
		if err != nil || !slices.Equal(texts(page.Messages), tt.want) {
			t.Errorf("%s: texts = %v, %v, want %v", tt.name, texts(page.Messages), err, tt.want)
		}
	}
	hidden, err := f.svc.ListMessages(asAgent("outsider"), app.Query{Filter: domain.MessageFilter{To: "intern"}, Limit: 50})
	if err != nil || len(hidden.Messages) != 0 {
		t.Fatalf("a filter reached a message the caller may not see: %v, %v", texts(hidden.Messages), err)
	}
}

func TestListMessages_RefusesBadFiltersAndClampsTheLimit(t *testing.T) {
	f := newFixture(t)
	seed(t, f)
	for name, filter := range map[string]domain.MessageFilter{
		"sender that is not an actor": {From: "builder"},
		"bad channel name":            {Channel: "Bad Name"},
		"bad recipient name":          {To: "Bad Name"},
	} {
		if _, err := f.svc.ListMessages(asHuman("ada"), app.Query{Filter: filter, Limit: 10}); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%s: error = %v, want ErrInvalid", name, err)
		}
	}
	page, err := f.svc.ListMessages(asHuman("ada"), app.Query{}) // no limit: the default applies
	if err != nil || len(page.Messages) != 5 {
		t.Fatalf("default limit page = %d messages, %v", len(page.Messages), err)
	}
}
