// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"sort"
)

// noLine stands in for the one-line of an agent that has not emitted one.
const noLine = "No one-line yet."

// Shortlisted is one rated candidate of a routing decision.
type Shortlisted struct {
	Agent   string
	Score   float64 // 0..1, higher fits better
	Reason  string  // at most MaxReason characters
	OneLine string
}

func shortlisted(c Candidate, score float64, reason string) Shortlisted {
	line := c.Entry.OneLine()
	if line == "" {
		line = noLine
	}
	return Shortlisted{Agent: c.Entry.Agent, Score: score, Reason: reason, OneLine: line}
}

// Rate applies the model's verdict to the candidates and returns them best
// first. Candidates the model did not rate score 0. Equal scores follow the
// text-match score, then the agent name.
func Rate(cands []Candidate, v Verdict) []Shortlisted {
	rated := map[string]Rating{}
	for _, r := range v.Ratings {
		rated[r.Agent] = r
	}
	text := map[string]float64{}
	out := make([]Shortlisted, 0, len(cands))
	for _, c := range cands {
		text[c.Entry.Agent] = c.Score
		r, ok := rated[c.Entry.Agent]
		if !ok {
			out = append(out, shortlisted(c, 0, "Not rated by the model."))
			continue
		}
		out = append(out, shortlisted(c, r.Score, r.Reason))
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if text[a.Agent] != text[b.Agent] {
			return text[a.Agent] > text[b.Agent]
		}
		return a.Agent < b.Agent
	})
	return out
}

// RateByText is the fallback when the model call fails: the text-match
// winner leads, scores are the match scores scaled so the best is 1, and
// every reason says why the model did not rate.
func RateByText(cands []Candidate, why string) []Shortlisted {
	var best float64
	for _, c := range cands {
		best = max(best, c.Score)
	}
	out := make([]Shortlisted, 0, len(cands))
	for _, c := range cands {
		var score float64
		reason := fmt.Sprintf("No text match; listed to fill the shortlist. Model rating unavailable: %s.", why)
		if best > 0 {
			score = c.Score / best
		}
		if c.Score > 0 {
			reason = fmt.Sprintf("Matches the request text on the live list. Model rating unavailable: %s.", why)
		}
		out = append(out, shortlisted(c, score, truncateRunes(reason, MaxReason)))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Agent < out[j].Agent
	})
	return out
}
