// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/router/domain"
)

// shortlistSize is how many agents the one model call rates. Few, so the
// call stays quick and cheap.
const shortlistSize = 5

// mainRouter is the router a request starts with when the caller names none
// and an agent of this name exists.
const mainRouter = "router"

// RouterDeps are the collaborators of Router.
type RouterDeps struct {
	Live        *LiveList
	Chats       Chats
	Credentials Credentials
	Gateway     Gateway
	Assignments domain.AssignmentRepository
	Clock       core.Clock
	IDs         core.IDs
	Log         *slog.Logger
}

// Router assigns a human request to the best agent's chat. It shortlists
// by text match over the live list, makes one quick model call to rate the
// shortlist, and posts the request into the winner's chat. No agent is woken
// to bid.
type Router struct{ RouterDeps }

// NewRouter returns the routing use case.
func NewRouter(d RouterDeps) *Router { return &Router{RouterDeps: d} }

// Route assigns text to an agent. startRouter names the router to begin with;
// empty means the society's main router.
func (r *Router) Route(ctx context.Context, text, startRouter string) (domain.Assignment, error) {
	caller, ok := core.CallerFrom(ctx)
	if !ok {
		return domain.Assignment{}, fmt.Errorf("route request: no caller: %w", core.ErrUnauthorized)
	}
	text, err := domain.NewRequestText(text)
	if err != nil {
		return domain.Assignment{}, err
	}
	entries, err := r.Live.Entries(ctx)
	if err != nil {
		return domain.Assignment{}, fmt.Errorf("route request: %w", err)
	}
	first, err := pickStartRouter(entries, startRouter)
	if err != nil {
		return domain.Assignment{}, err
	}
	trail := domain.NewTrail(first)
	shortlist, err := r.rate(ctx, text, entries, trail)
	if err != nil {
		return domain.Assignment{}, err
	}
	id := r.IDs.New("asg")
	ref, shortlist, err := r.openChat(ctx, shortlist, text, id)
	if err != nil {
		return domain.Assignment{}, err
	}
	a, err := domain.BuildAssignment(domain.NewAssignment{
		ID: id, Text: text, Router: trail.Current(), Hops: trail.Hops(), Shortlist: shortlist,
		ChatPath: ref.Path, ChatMessageID: ref.MessageID,
		CreatedBy: string(caller.Kind) + ":" + caller.Name, Now: r.Clock.Now(),
	})
	if err != nil {
		return domain.Assignment{}, err
	}
	if err := r.Assignments.Save(ctx, a); err != nil {
		return domain.Assignment{}, fmt.Errorf("save assignment %s: %w", id, err)
	}
	return a, nil
}

// Get returns a stored assignment.
func (r *Router) Get(ctx context.Context, id string) (domain.Assignment, error) {
	a, err := r.Assignments.Get(ctx, id)
	if err != nil {
		return domain.Assignment{}, fmt.Errorf("get assignment: %w", err)
	}
	return a, nil
}

// rate runs routers until one rates a shortlist, following hand-offs.
func (r *Router) rate(ctx context.Context, text string, entries []domain.Entry, trail *domain.Trail) ([]domain.Shortlisted, error) {
	for {
		cands := domain.Shortlist(text, scope(entries, trail), shortlistSize)
		if len(cands) == 0 {
			return nil, fmt.Errorf("no agent can take this request: %w", core.ErrUnavailable)
		}
		others := otherRouters(entries, trail.Current())
		v, why := r.ask(ctx, trail.Current(), text, cands, others)
		if v.Handoff != "" {
			err := trail.HandOff(v.Handoff, v.HandoffReason)
			if err == nil {
				continue
			}
			r.Log.Warn("router hand-off refused", "router", trail.Current(), "to", v.Handoff, "err", err)
			if len(v.Ratings) == 0 {
				why = "router hand-off refused"
			}
		}
		if len(v.Ratings) == 0 {
			return domain.RateByText(cands, why), nil
		}
		return domain.Rate(cands, v), nil
	}
}

// ask makes the one model call. On any failure it returns an empty verdict
// and a short reason that ends up in the shortlist, so the human sees that
// the pick is a text match only.
func (r *Router) ask(ctx context.Context, router, text string, cands []domain.Candidate, others []domain.Entry) (domain.Verdict, string) {
	prompt, err := buildPrompt(text, cands, others)
	if err != nil {
		r.Log.Error("build routing prompt", "err", err)
		return domain.Verdict{}, "the model call could not be built"
	}
	token, err := r.Credentials.TokenFor(ctx, router)
	if errors.Is(err, core.ErrUnavailable) {
		r.Log.Warn("model gateway not reachable", "router", router, "err", err)
		return domain.Verdict{}, "model gateway not reachable"
	}
	if err != nil {
		r.Log.Warn("router has no model credential", "router", router, "err", err)
		return domain.Verdict{}, "no model credential for the router"
	}
	raw, err := r.Gateway.Complete(ctx, token, prompt)
	switch {
	case errors.Is(err, core.ErrBudget):
		return domain.Verdict{}, "the router's model budget is used up"
	case err != nil:
		r.Log.Warn("routing model call failed", "router", router, "err", err)
		return domain.Verdict{}, "the model call failed"
	}
	v, err := domain.ParseVerdict(raw, candidateNames(cands), entryNames(others))
	if err != nil {
		r.Log.Warn("routing model answer unreadable", "router", router, "err", err)
		return domain.Verdict{}, "the model answer was unreadable"
	}
	return v, ""
}

// openChat posts the request to the best agent that takes it. An agent that
// is frozen, out of budget or gone is skipped and left off the shortlist.
func (r *Router) openChat(ctx context.Context, shortlist []domain.Shortlisted, text, assignmentID string) (ChatRef, []domain.Shortlisted, error) {
	for i, s := range shortlist {
		ref, err := r.Chats.OpenChat(ctx, s.Agent, text, assignmentID)
		switch {
		case err == nil:
			return ref, shortlist[i:], nil
		case errors.Is(err, core.ErrFrozen), errors.Is(err, core.ErrBudget), errors.Is(err, core.ErrNotFound):
			r.Log.Info("agent cannot take the request", "agent", s.Agent, "err", err)
		default:
			return ChatRef{}, nil, fmt.Errorf("open chat with %s: %w", s.Agent, err)
		}
	}
	return ChatRef{}, nil, fmt.Errorf("no agent on the shortlist could take the request: %w", core.ErrUnavailable)
}

func usable(p domain.Profile) bool {
	return p.State != "frozen" && p.State != "failed" && p.State != "retired"
}

// pickStartRouter returns the named router, or the main one: the agent called
// "router", else the first router by name.
func pickStartRouter(entries []domain.Entry, named string) (string, error) {
	var routers []string
	for _, e := range entries {
		if e.Router && usable(e.Profile) {
			routers = append(routers, e.Agent)
		}
	}
	slices.Sort(routers)
	switch {
	case named != "":
		if !slices.Contains(routers, named) {
			return "", fmt.Errorf("router %s: %w", named, core.ErrNotFound)
		}
		return named, nil
	case slices.Contains(routers, mainRouter):
		return mainRouter, nil
	case len(routers) > 0:
		return routers[0], nil
	}
	return "", fmt.Errorf("no router agent is running: %w", core.ErrUnavailable)
}

// scope returns the agents a router may pick from. The first router covers
// the whole society; a router it handed over to covers its own team, or the
// whole society when its team has nobody else.
func scope(entries []domain.Entry, trail *domain.Trail) []domain.Entry {
	var all, team []domain.Entry
	var teamOf string
	if len(trail.Hops()) > 0 {
		for _, e := range entries {
			if e.Agent == trail.Current() {
				teamOf = e.Team
			}
		}
	}
	for _, e := range entries {
		if !e.Eligible() {
			continue
		}
		all = append(all, e)
		if teamOf != "" && e.Team == teamOf {
			team = append(team, e)
		}
	}
	if len(team) > 0 {
		return team
	}
	return all
}

func otherRouters(entries []domain.Entry, current string) []domain.Entry {
	var out []domain.Entry
	for _, e := range entries {
		if e.Router && usable(e.Profile) && e.Agent != current {
			out = append(out, e)
		}
	}
	return out
}

func candidateNames(cs []domain.Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Entry.Agent
	}
	return out
}

func entryNames(es []domain.Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Agent
	}
	return out
}
