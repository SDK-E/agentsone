// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/router/domain"
)

func TestNewLine_Sanitises(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
	}{
		{"plain text stays", "Goal: ship. Now: coding.", "Goal: ship. Now: coding."},
		{"newlines and tabs become spaces", "Goal: ship.\nNow:\tcoding.", "Goal: ship. Now: coding."},
		{"runs of spaces collapse", "a    b \r\n  c", "a b c"},
		{"outer space is trimmed", "  hello  ", "hello"},
		{"control characters become spaces", "a\x00b\x1bc", "a b c"},
		{"unicode line separator becomes a space", "a b", "a b"},
		{"invalid utf-8 is dropped", "ok\xff\xfe!", "ok!"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.NewLine(tc.in)
			if err != nil {
				t.Fatalf("NewLine(%q) error = %v", tc.in, err)
			}
			if got.String() != tc.want {
				t.Fatalf("NewLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNewLine_Rejects(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, in string }{
		{"empty", ""},
		{"only space", " \n\t "},
		{"too long", strings.Repeat("x", core.MaxLiveLine+1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := domain.NewLine(tc.in); !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("NewLine error = %v, want core.ErrInvalid", err)
			}
		})
	}
}

func TestNewLine_AcceptsTheLongestLine(t *testing.T) {
	t.Parallel()
	if _, err := domain.NewLine(strings.Repeat("x", core.MaxLiveLine)); err != nil {
		t.Fatalf("longest line rejected: %v", err)
	}
}

func entry(agent, display, role, line string) domain.Entry {
	e := domain.Entry{Profile: domain.Profile{Agent: agent, DisplayName: display, Role: role, Kind: "chat", State: "awake", Team: "founder"}}
	if line != "" {
		l, err := domain.NewLine(line)
		if err != nil {
			panic(err)
		}
		e.Line = &l
	}
	return e
}

var society = []domain.Entry{
	entry("builder", "Builder", "Builder", "Goal: ship the docs site. Now: writing the page layout and asking scribe for copy."),
	entry("scribe", "Scribe", "Docs writer", "Goal: clear docs for humans. Now: asleep until builder sends the page outline."),
	entry("keeper", "Keeper", "Disk and package keeper", "Goal: keep disk under 70 percent. Now: cleaning old logs in /var/log/journal."),
	entry("founder", "Founder", "Founder", "Goal: keep the society healthy. Now: reviewing builder's plan for the docs site."),
}

func names(cs []domain.Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Entry.Agent
	}
	return out
}

func TestShortlist_RanksByTextMatch(t *testing.T) {
	t.Parallel()
	got := domain.Shortlist("Please clean the disk logs", society, 2)
	if names(got)[0] != "keeper" {
		t.Fatalf("shortlist = %v, want keeper first", names(got))
	}
	if got[0].Score <= 0 {
		t.Fatalf("top score = %v, want positive", got[0].Score)
	}
}

func TestShortlist_MatchesRoleAndName(t *testing.T) {
	t.Parallel()
	got := domain.Shortlist("who is the docs writer", society, 1)
	if names(got)[0] != "scribe" {
		t.Fatalf("shortlist = %v, want scribe first", names(got))
	}
}

func TestShortlist_StemsPlurals(t *testing.T) {
	t.Parallel()
	got := domain.Shortlist("logs", society, 1)
	if names(got)[0] != "keeper" {
		t.Fatalf("shortlist = %v, want keeper", names(got))
	}
	got = domain.Shortlist("log", society, 1)
	if names(got)[0] != "keeper" {
		t.Fatalf("shortlist = %v, want keeper", names(got))
	}
}

func TestShortlist_TiesBreakByAgentName(t *testing.T) {
	t.Parallel()
	same := []domain.Entry{
		entry("zed", "Zed", "Helper", "Goal: help."),
		entry("amy", "Amy", "Helper", "Goal: help."),
		entry("moe", "Moe", "Helper", "Goal: help."),
	}
	got := names(domain.Shortlist("help", same, 3))
	want := []string{"amy", "moe", "zed"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("shortlist = %v, want %v", got, want)
		}
	}
}

func TestShortlist_FillsWithUnmatchedAgentsByName(t *testing.T) {
	t.Parallel()
	got := domain.Shortlist("zebra quantum", society, 3)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (filled)", len(got))
	}
	if names(got)[0] != "builder" || got[0].Score != 0 {
		t.Fatalf("shortlist = %v scores %v", names(got), got)
	}
}

func TestShortlist_LimitsAndHandlesEmpty(t *testing.T) {
	t.Parallel()
	if got := domain.Shortlist("docs", nil, 3); len(got) != 0 {
		t.Fatalf("no entries gave %v", got)
	}
	if got := domain.Shortlist("docs", society, 0); len(got) != 0 {
		t.Fatalf("limit 0 gave %v", got)
	}
	if got := domain.Shortlist("docs", society, 10); len(got) != len(society) {
		t.Fatalf("limit above size gave %d", len(got))
	}
}

func TestShortlist_IsDeterministic(t *testing.T) {
	t.Parallel()
	first := names(domain.Shortlist("docs site layout", society, 4))
	for range 20 {
		again := names(domain.Shortlist("docs site layout", society, 4))
		for i := range first {
			if first[i] != again[i] {
				t.Fatalf("order changed: %v then %v", first, again)
			}
		}
	}
}

func TestEligible(t *testing.T) {
	t.Parallel()
	tests := []struct {
		state  string
		router bool
		want   bool
	}{
		{"awake", false, true}, {"sleeping", false, true}, {"working", false, true}, {"waking", false, true},
		{"frozen", false, false}, {"failed", false, false}, {"retired", false, false},
		{"awake", true, false},
	}
	for _, tc := range tests {
		p := domain.Profile{Agent: "a", State: tc.state, Router: tc.router}
		if got := p.Eligible(); got != tc.want {
			t.Errorf("state %s router %v: Eligible = %v, want %v", tc.state, tc.router, got, tc.want)
		}
	}
}

func TestParseVerdict_ReadsStrictJSON(t *testing.T) {
	t.Parallel()
	raw := `{"ratings":[{"agent":"builder","score":0.9,"reason":"Owns the docs site."},{"agent":"scribe","score":0.4,"reason":"Writes copy."}],"handoff":null}`
	v, err := domain.ParseVerdict(raw, []string{"builder", "scribe"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Ratings) != 2 || v.Ratings[0].Agent != "builder" || v.Ratings[0].Score != 0.9 || v.Handoff != "" {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestParseVerdict_Defensive(t *testing.T) {
	t.Parallel()
	allowed := []string{"builder", "scribe"}
	tests := []struct {
		name    string
		raw     string
		wantErr bool
		check   func(t *testing.T, v domain.Verdict)
	}{
		{"code fence and chatter", "Sure!\n```json\n{\"ratings\":[{\"agent\":\"builder\",\"score\":1,\"reason\":\"x\"}]}\n```\nDone.", false,
			func(t *testing.T, v domain.Verdict) {
				if len(v.Ratings) != 1 {
					t.Fatalf("ratings = %+v", v.Ratings)
				}
			}},
		{"unknown agent dropped", `{"ratings":[{"agent":"ghost","score":1,"reason":"x"},{"agent":"scribe","score":0.5,"reason":"y"}]}`, false,
			func(t *testing.T, v domain.Verdict) {
				if len(v.Ratings) != 1 || v.Ratings[0].Agent != "scribe" {
					t.Fatalf("ratings = %+v", v.Ratings)
				}
			}},
		{"score above one clamps", `{"ratings":[{"agent":"builder","score":7,"reason":"x"}]}`, false,
			func(t *testing.T, v domain.Verdict) {
				if v.Ratings[0].Score != 1 {
					t.Fatalf("score = %v", v.Ratings[0].Score)
				}
			}},
		{"negative score clamps", `{"ratings":[{"agent":"builder","score":-3,"reason":"x"}]}`, false,
			func(t *testing.T, v domain.Verdict) {
				if v.Ratings[0].Score != 0 {
					t.Fatalf("score = %v", v.Ratings[0].Score)
				}
			}},
		{"duplicates keep the first", `{"ratings":[{"agent":"builder","score":0.2,"reason":"a"},{"agent":"builder","score":0.9,"reason":"b"}]}`, false,
			func(t *testing.T, v domain.Verdict) {
				if len(v.Ratings) != 1 || v.Ratings[0].Score != 0.2 {
					t.Fatalf("ratings = %+v", v.Ratings)
				}
			}},
		{"long reason is cut to 255", `{"ratings":[{"agent":"builder","score":1,"reason":"` + strings.Repeat("é", 400) + `"}]}`, false,
			func(t *testing.T, v domain.Verdict) {
				if n := len([]rune(v.Ratings[0].Reason)); n != 255 {
					t.Fatalf("reason has %d runes", n)
				}
			}},
		{"reason with newlines is one line", "{\"ratings\":[{\"agent\":\"builder\",\"score\":1,\"reason\":\"a\\nb\"}]}", false,
			func(t *testing.T, v domain.Verdict) {
				if v.Ratings[0].Reason != "a b" {
					t.Fatalf("reason = %q", v.Ratings[0].Reason)
				}
			}},
		{"garbage", "I think builder is best.", true, nil},
		{"empty", "", true, nil},
		{"json without ratings", `{"answer":"builder"}`, true, nil},
		{"only unknown agents", `{"ratings":[{"agent":"ghost","score":1,"reason":"x"}]}`, true, nil},
		{"score is a string", `{"ratings":[{"agent":"builder","score":"high","reason":"x"}]}`, true, nil},
		{"truncated json", `{"ratings":[{"agent":"builder","sc`, true, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, err := domain.ParseVerdict(tc.raw, allowed, nil)
			if tc.wantErr {
				if !errors.Is(err, domain.ErrUnreadable) {
					t.Fatalf("error = %v, want ErrUnreadable", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			tc.check(t, v)
		})
	}
}

func TestParseVerdict_Handoff(t *testing.T) {
	t.Parallel()
	raw := `{"ratings":[{"agent":"builder","score":0.5,"reason":"x"}],"handoff":{"router":"docs-router","reason":"Docs questions are its field."}}`
	v, err := domain.ParseVerdict(raw, []string{"builder"}, []string{"docs-router"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Handoff != "docs-router" || v.HandoffReason != "Docs questions are its field." {
		t.Fatalf("verdict = %+v", v)
	}
	v, err = domain.ParseVerdict(raw, []string{"builder"}, []string{"other-router"})
	if err != nil || v.Handoff != "" {
		t.Fatalf("unknown router kept: %+v, %v", v, err)
	}
}

func TestParseVerdict_HandoffWithoutRatingsIsReadable(t *testing.T) {
	t.Parallel()
	raw := `{"ratings":[],"handoff":{"router":"docs-router","reason":"Its field."}}`
	v, err := domain.ParseVerdict(raw, []string{"builder"}, []string{"docs-router"})
	if err != nil || v.Handoff != "docs-router" {
		t.Fatalf("verdict = %+v, err = %v", v, err)
	}
}

func cands(pairs ...any) []domain.Candidate {
	var out []domain.Candidate
	for rest := pairs; len(rest) >= 2; rest = rest[2:] {
		name, score := rest[0].(string), rest[1].(float64)
		out = append(out, domain.Candidate{Entry: entry(name, "X", "R", "Goal: "+name), Score: score})
	}
	return out
}

func TestRate_OrdersByModelScore(t *testing.T) {
	t.Parallel()
	c := cands("builder", 3.0, "scribe", 2.0, "keeper", 1.0)
	v := domain.Verdict{Ratings: []domain.Rating{
		{Agent: "scribe", Score: 0.9, Reason: "best"},
		{Agent: "builder", Score: 0.5, Reason: "ok"},
	}}
	got := domain.Rate(c, v)
	if got[0].Agent != "scribe" || got[1].Agent != "builder" || got[2].Agent != "keeper" {
		t.Fatalf("shortlist = %+v", got)
	}
	if got[2].Score != 0 || got[2].Reason == "" {
		t.Fatalf("unrated entry = %+v", got[2])
	}
	if got[0].Reason != "best" || got[0].OneLine != "Goal: scribe" {
		t.Fatalf("first = %+v", got[0])
	}
}

func TestRate_TiesFollowTextScoreThenName(t *testing.T) {
	t.Parallel()
	c := cands("amy", 1.0, "bob", 2.0, "cat", 2.0)
	v := domain.Verdict{Ratings: []domain.Rating{
		{Agent: "amy", Score: 0.7, Reason: "a"}, {Agent: "bob", Score: 0.7, Reason: "b"}, {Agent: "cat", Score: 0.7, Reason: "c"},
	}}
	got := domain.Rate(c, v)
	if got[0].Agent != "bob" || got[1].Agent != "cat" || got[2].Agent != "amy" {
		t.Fatalf("shortlist = %+v", got)
	}
}

func TestRateByText_NormalisesAndSaysWhy(t *testing.T) {
	t.Parallel()
	c := cands("builder", 4.0, "scribe", 2.0, "keeper", 0.0)
	got := domain.RateByText(c, "the model is out of budget")
	if got[0].Agent != "builder" || got[0].Score != 1 || got[1].Score != 0.5 || got[2].Score != 0 {
		t.Fatalf("shortlist = %+v", got)
	}
	if !strings.Contains(got[0].Reason, "the model is out of budget") || len([]rune(got[0].Reason)) > 255 {
		t.Fatalf("reason = %q", got[0].Reason)
	}
}

func TestRateByText_AllZeroScoresStayZero(t *testing.T) {
	t.Parallel()
	got := domain.RateByText(cands("amy", 0.0, "bob", 0.0), "why")
	if got[0].Score != 0 || got[0].Agent != "amy" {
		t.Fatalf("shortlist = %+v", got)
	}
}

func TestTrail_HandOff(t *testing.T) {
	t.Parallel()
	tr := domain.NewTrail("router")
	if err := tr.HandOff("docs-router", "Docs field."); err != nil {
		t.Fatal(err)
	}
	if tr.Current() != "docs-router" || len(tr.Hops()) != 1 || tr.Hops()[0].Router != "router" || tr.Hops()[0].HandedTo != "docs-router" {
		t.Fatalf("trail = %+v", tr)
	}
}

func TestTrail_StopsLoopsAndDepth(t *testing.T) {
	t.Parallel()
	tr := domain.NewTrail("a")
	if err := tr.HandOff("a", "self"); !errors.Is(err, domain.ErrHandoffLoop) {
		t.Fatalf("self hand-off error = %v", err)
	}
	if err := tr.HandOff("b", "x"); err != nil {
		t.Fatal(err)
	}
	if err := tr.HandOff("a", "back"); !errors.Is(err, domain.ErrHandoffLoop) {
		t.Fatalf("back hand-off error = %v", err)
	}
	for _, next := range []string{"c", "d"} {
		if err := tr.HandOff(next, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.HandOff("e", "x"); !errors.Is(err, domain.ErrHandoffLoop) {
		t.Fatalf("hop %d error = %v, want depth limit", domain.MaxHops+1, err)
	}
}

func TestTrail_HopsAreACopy(t *testing.T) {
	t.Parallel()
	tr := domain.NewTrail("a")
	_ = tr.HandOff("b", "x")
	tr.Hops()[0].Reason = "changed"
	if tr.Hops()[0].Reason != "x" {
		t.Fatal("Hops exposes internal state")
	}
}

func TestNewAssignment_ChecksItsFields(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 19, 18, 20, 2, 0, time.UTC)
	good := domain.NewAssignment{
		ID: "asg_1", Text: "do it", Router: "router", CreatedBy: "human:ada", Now: now,
		Shortlist: []domain.Shortlisted{{Agent: "builder", Score: 1, Reason: "r", OneLine: "l"}},
		ChatPath:  "/v1/agents/builder/chat", ChatMessageID: "cht_1",
	}
	a, err := domain.BuildAssignment(good)
	if err != nil {
		t.Fatal(err)
	}
	if a.Chosen != "builder" {
		t.Fatalf("chosen = %q, want the first shortlist entry", a.Chosen)
	}
	bad := good
	bad.Shortlist = nil
	if _, err := domain.BuildAssignment(bad); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("empty shortlist error = %v", err)
	}
	bad = good
	bad.Text = "  "
	if _, err := domain.BuildAssignment(bad); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("empty text error = %v", err)
	}
}

func TestRequestText(t *testing.T) {
	t.Parallel()
	if got, err := domain.NewRequestText("  hello \n"); err != nil || got != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, in := range []string{"", "   ", strings.Repeat("x", 4001)} {
		if _, err := domain.NewRequestText(in); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("NewRequestText(%d bytes) error = %v", len(in), err)
		}
	}
}
