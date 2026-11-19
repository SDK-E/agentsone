// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/router/adapters/memory"
	"github.com/SDK-E/agentsone/internal/router/app"
	"github.com/SDK-E/agentsone/internal/router/routertest"
)

type rig struct {
	live     *app.LiveList
	router   *app.Router
	profiles *routertest.Profiles
	chats    *routertest.Chats
	gateway  *routertest.Gateway
}

func human(ctx context.Context) context.Context {
	return core.WithCaller(ctx, core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1})
}

func newRig(t *testing.T, creds app.Credentials, replies ...routertest.Reply) *rig {
	t.Helper()
	clock := coretest.NewClock(coretest.Epoch)
	profiles := routertest.NewProfiles(
		routertest.Router("router"),
		routertest.Router("docs-router"),
		routertest.Agent("builder", "Builder"),
		routertest.Agent("scribe", "Docs writer"),
		routertest.Agent("keeper", "Disk and package keeper"),
	)
	live := app.NewLiveList(memory.NewLines(), profiles, clock)
	ctx := context.Background()
	for agent, line := range map[string]string{
		"builder": "Goal: ship the docs site. Now: writing the page layout.",
		"scribe":  "Goal: clear docs for humans. Now: asleep until builder sends the outline.",
		"keeper":  "Goal: keep disk under 70 percent. Now: cleaning old logs.",
	} {
		if err := live.UpdateLine(ctx, agent, line); err != nil {
			t.Fatal(err)
		}
		clock.Advance(1)
	}
	chats := routertest.NewChats()
	gw := routertest.NewGateway(replies...)
	r := app.NewRouter(app.RouterDeps{
		Live: live, Chats: chats, Credentials: creds, Gateway: gw,
		Assignments: memory.NewAssignments(), Clock: clock, IDs: coretest.NewIDs(),
		Log: slog.New(slog.DiscardHandler),
	})
	return &rig{live: live, router: r, profiles: profiles, chats: chats, gateway: gw}
}

func good(agent string) routertest.Reply {
	return routertest.Reply{Text: fmt.Sprintf(`{"ratings":[{"agent":%q,"score":0.95,"reason":"Best fit."}],"handoff":null}`, agent)}
}

func TestRoute_ModelRatingPicksTheAgentAndOpensItsChat(t *testing.T) {
	t.Parallel()
	r := newRig(t, routertest.Credentials{}, good("scribe"))

	a, err := r.router.Route(human(context.Background()), "Can you add a page about the budget walls to the docs site?", "")
	if err != nil {
		t.Fatal(err)
	}

	if a.Chosen != "scribe" || a.Router != "router" || len(a.Hops) != 0 {
		t.Fatalf("assignment = %+v", a)
	}
	if a.Shortlist[0].Agent != "scribe" || a.Shortlist[0].Score != 0.95 || a.Shortlist[0].Reason != "Best fit." {
		t.Fatalf("shortlist = %+v", a.Shortlist)
	}
	if a.CreatedBy != "human:ada" || a.ChatPath != "/v1/agents/scribe/chat" || !strings.HasPrefix(a.ID, "asg_") {
		t.Fatalf("assignment = %+v", a)
	}
	if len(r.chats.Posts) != 1 || r.chats.Posts[0].Agent != "scribe" || r.chats.Posts[0].AssignmentID != a.ID {
		t.Fatalf("posts = %+v", r.chats.Posts)
	}
	got, err := r.router.Get(context.Background(), a.ID)
	if err != nil || got.Chosen != "scribe" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
}

func TestRoute_CallsTheGatewayOnceWithTheRoutersToken(t *testing.T) {
	t.Parallel()
	r := newRig(t, routertest.Credentials{}, good("builder"))

	if _, err := r.router.Route(human(context.Background()), "docs site layout", ""); err != nil {
		t.Fatal(err)
	}

	if r.gateway.CallCount() != 1 {
		t.Fatalf("gateway calls = %d, want 1", r.gateway.CallCount())
	}
	c := r.gateway.Calls[0]
	if c.Token != "token-of-router" {
		t.Fatalf("token = %q", c.Token)
	}
	if !strings.Contains(c.Prompt.User, "docs site layout") || !strings.Contains(c.Prompt.User, "writing the page layout") {
		t.Fatalf("prompt does not carry the request and the lines: %s", c.Prompt.User)
	}
	if strings.Contains(c.Prompt.User, "token-of") || strings.Contains(c.Prompt.System, "token-of") {
		t.Fatal("prompt leaks the token")
	}
}

func TestRoute_RoutersAndFrozenAgentsAreNotCandidates(t *testing.T) {
	t.Parallel()
	r := newRig(t, routertest.Credentials{}, good("builder"))
	frozen := routertest.Agent("keeper", "Disk and package keeper")
	frozen.State = "frozen"
	r.profiles.Set(routertest.Router("router"), routertest.Agent("builder", "Builder"), frozen)

	a, err := r.router.Route(human(context.Background()), "clean the disk logs", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range a.Shortlist {
		if s.Agent == "router" || s.Agent == "keeper" {
			t.Fatalf("shortlist holds %s: %+v", s.Agent, a.Shortlist)
		}
	}
}

func TestRoute_FallsBackToTheTextWinnerAndSaysWhy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		creds app.Credentials
		reply routertest.Reply
		why   string
		calls int
	}{
		{"garbage answer", routertest.Credentials{}, routertest.Reply{Text: "I would pick keeper, honestly."}, "unreadable", 1},
		{"budget exhausted", routertest.Credentials{}, routertest.Reply{Err: fmt.Errorf("gateway: %w", core.ErrBudget)}, "budget", 1},
		{"gateway down", routertest.Credentials{}, routertest.Reply{Err: errors.New("connection refused")}, "model call failed", 1},
		{"no credential", routertest.Credentials{Err: errors.New("no token")}, routertest.Reply{}, "credential", 0},
		{"gateway not reachable", routertest.Credentials{Err: fmt.Errorf("mint: %w", core.ErrUnavailable)}, routertest.Reply{}, "model gateway not reachable", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, tc.creds, tc.reply)

			a, err := r.router.Route(human(context.Background()), "please clean the old logs on disk", "")
			if err != nil {
				t.Fatal(err)
			}

			if a.Chosen != "keeper" {
				t.Fatalf("chosen = %s, want the text winner keeper", a.Chosen)
			}
			if !strings.Contains(a.Shortlist[0].Reason, tc.why) {
				t.Fatalf("reason %q does not say %q", a.Shortlist[0].Reason, tc.why)
			}
			if r.gateway.CallCount() != tc.calls {
				t.Fatalf("gateway calls = %d, want %d", r.gateway.CallCount(), tc.calls)
			}
			if a.Shortlist[0].Score != 1 {
				t.Fatalf("top text score = %v, want 1", a.Shortlist[0].Score)
			}
		})
	}
}

func TestRoute_HandsToASpecialisedRouter(t *testing.T) {
	t.Parallel()
	handoff := routertest.Reply{Text: `{"ratings":[],"handoff":{"router":"docs-router","reason":"Docs questions are its field."}}`}
	r := newRig(t, routertest.Credentials{}, handoff, good("scribe"))

	a, err := r.router.Route(human(context.Background()), "docs page please", "")
	if err != nil {
		t.Fatal(err)
	}

	if a.Router != "docs-router" || a.Chosen != "scribe" {
		t.Fatalf("assignment = %+v", a)
	}
	if len(a.Hops) != 1 || a.Hops[0].Router != "router" || a.Hops[0].HandedTo != "docs-router" || a.Hops[0].Reason == "" {
		t.Fatalf("hops = %+v", a.Hops)
	}
	if r.gateway.CallCount() != 2 || r.gateway.Calls[1].Token != "token-of-docs-router" {
		t.Fatalf("calls = %+v", r.gateway.Calls)
	}
}

func TestRoute_StopsAHandoffLoop(t *testing.T) {
	t.Parallel()
	toDocs := routertest.Reply{Text: `{"ratings":[],"handoff":{"router":"docs-router","reason":"x"}}`}
	back := routertest.Reply{Text: `{"ratings":[],"handoff":{"router":"router","reason":"back"}}`}
	r := newRig(t, routertest.Credentials{}, toDocs, back)

	a, err := r.router.Route(human(context.Background()), "docs page please", "")
	if err != nil {
		t.Fatal(err)
	}

	if a.Router != "docs-router" || len(a.Hops) != 1 || r.gateway.CallCount() != 2 {
		t.Fatalf("assignment = %+v after %d calls", a, r.gateway.CallCount())
	}
	if !strings.Contains(a.Shortlist[0].Reason, "hand-off") {
		t.Fatalf("reason %q does not say the hand-off was refused", a.Shortlist[0].Reason)
	}
}

func TestRoute_StartsWithTheNamedRouter(t *testing.T) {
	t.Parallel()
	r := newRig(t, routertest.Credentials{}, good("scribe"))

	a, err := r.router.Route(human(context.Background()), "docs", "docs-router")
	if err != nil {
		t.Fatal(err)
	}
	if a.Router != "docs-router" || r.gateway.Calls[0].Token != "token-of-docs-router" {
		t.Fatalf("assignment = %+v, token %q", a, r.gateway.Calls[0].Token)
	}
}

func TestRoute_SkipsAgentsThatRefuseTheChat(t *testing.T) {
	t.Parallel()
	r := newRig(t, routertest.Credentials{}, routertest.Reply{
		Text: `{"ratings":[{"agent":"scribe","score":0.9,"reason":"a"},{"agent":"builder","score":0.8,"reason":"b"}]}`,
	})
	r.chats.Refuse["scribe"] = fmt.Errorf("agent is frozen: %w", core.ErrFrozen)

	a, err := r.router.Route(human(context.Background()), "docs", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Chosen != "builder" || a.Shortlist[0].Agent != "builder" {
		t.Fatalf("assignment = %+v", a)
	}
}

func TestRoute_FailsWhenNoAgentTakesTheChat(t *testing.T) {
	t.Parallel()
	r := newRig(t, routertest.Credentials{}, good("scribe"))
	for _, name := range []string{"builder", "scribe", "keeper"} {
		r.chats.Refuse[name] = core.ErrFrozen
	}

	_, err := r.router.Route(human(context.Background()), "docs", "")
	if !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
}

func TestRoute_FailsOnOtherChatErrorsWithoutTryingFurther(t *testing.T) {
	t.Parallel()
	r := newRig(t, routertest.Credentials{}, good("scribe"))
	r.chats.Refuse["scribe"] = errors.New("disk full")

	if _, err := r.router.Route(human(context.Background()), "docs", ""); err == nil || errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("error = %v, want the chat error", err)
	}
	if len(r.chats.Posts) != 0 {
		t.Fatalf("posts = %+v", r.chats.Posts)
	}
}

func TestRoute_Rejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		ctx    context.Context
		text   string
		router string
		setup  func(*rig)
		want   error
	}{
		{"empty text", human(context.Background()), "  ", "", nil, core.ErrInvalid},
		{"no caller", context.Background(), "docs", "", nil, core.ErrUnauthorized},
		{"unknown router", human(context.Background()), "docs", "ghost", nil, core.ErrNotFound},
		{"named agent is not a router", human(context.Background()), "docs", "builder", nil, core.ErrNotFound},
		{"no router agent exists", human(context.Background()), "docs", "", func(r *rig) {
			r.profiles.Set(routertest.Agent("builder", "Builder"))
		}, core.ErrUnavailable},
		{"nobody can take the request", human(context.Background()), "docs", "", func(r *rig) {
			r.profiles.Set(routertest.Router("router"))
		}, core.ErrUnavailable},
		{"agents are unreadable", human(context.Background()), "docs", "", func(r *rig) {
			r.profiles.Err = errors.New("db down")
		}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, routertest.Credentials{}, good("builder"))
			if tc.setup != nil {
				tc.setup(r)
			}
			_, err := r.router.Route(tc.ctx, tc.text, tc.router)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRoute_PrefersTheRouterNamedRouter(t *testing.T) {
	t.Parallel()
	r := newRig(t, routertest.Credentials{}, good("builder"))
	r.profiles.Set(routertest.Router("a-router"), routertest.Router("router"), routertest.Agent("builder", "Builder"))

	a, err := r.router.Route(human(context.Background()), "docs", "")
	if err != nil || a.Router != "router" {
		t.Fatalf("assignment = %+v, %v", a, err)
	}
}

func TestRoute_FallsBackToTheFirstRouterByName(t *testing.T) {
	t.Parallel()
	r := newRig(t, routertest.Credentials{}, good("builder"))
	r.profiles.Set(routertest.Router("zed-router"), routertest.Router("a-router"), routertest.Agent("builder", "Builder"))

	a, err := r.router.Route(human(context.Background()), "docs", "")
	if err != nil || a.Router != "a-router" {
		t.Fatalf("assignment = %+v, %v", a, err)
	}
}

func TestGet_UnknownAssignmentIsNotFound(t *testing.T) {
	t.Parallel()
	r := newRig(t, routertest.Credentials{})
	if _, err := r.router.Get(context.Background(), "asg_missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("error = %v", err)
	}
}

func TestRoute_HandoffScopesCandidatesToTheRoutersTeam(t *testing.T) {
	t.Parallel()
	r := newRig(t, routertest.Credentials{},
		routertest.Reply{Text: `{"ratings":[],"handoff":{"router":"docs-router","reason":"x"}}`}, good("scribe"))
	docsRouter := routertest.Router("docs-router")
	docsRouter.Team = "docs"
	scribe := routertest.Agent("scribe", "Docs writer")
	scribe.Team = "docs"
	r.profiles.Set(routertest.Router("router"), docsRouter, scribe, routertest.Agent("keeper", "Keeper"))

	if _, err := r.router.Route(human(context.Background()), "anything", ""); err != nil {
		t.Fatal(err)
	}
	second := r.gateway.Calls[1].Prompt.User
	if strings.Contains(second, "keeper") || !strings.Contains(second, "scribe") {
		t.Fatalf("second prompt is not scoped to the docs team: %s", second)
	}
}
