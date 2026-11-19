// SPDX-License-Identifier: MIT

package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// ErrUnreadable is returned when a model answer does not hold a usable
// verdict. The router then falls back to the text-match winner.
var ErrUnreadable = errors.New("model answer is not a usable verdict")

// MaxReason is the longest reason text, in characters, as the contract allows.
const MaxReason = 255

// Rating is the model's score for one candidate, from 0 (no fit) to 1.
type Rating struct {
	Agent  string
	Score  float64
	Reason string
}

// Verdict is what the quick model call answers: ratings, and optionally a
// hand-off to a more specialised router.
type Verdict struct {
	Ratings       []Rating
	Handoff       string // router agent, or "" for none
	HandoffReason string
}

type verdictJSON struct {
	Ratings []struct {
		Agent  string  `json:"agent"`
		Score  float64 `json:"score"`
		Reason string  `json:"reason"`
	} `json:"ratings"`
	Handoff json.RawMessage `json:"handoff"`
}

type handoffJSON struct {
	Router string `json:"router"`
	Reason string `json:"reason"`
}

// ParseVerdict reads the model's answer defensively. It finds the first
// JSON object in raw (models add chatter and code fences), drops ratings of
// agents that are not in candidates, clamps scores into 0..1, keeps the
// first rating of each agent and cuts reasons to one line. A hand-off to
// anything that is not in routers is ignored. An answer with neither a
// rating nor a hand-off is ErrUnreadable.
func ParseVerdict(raw string, candidates, routers []string) (Verdict, error) {
	obj, ok := firstJSONObject(raw)
	if !ok {
		return Verdict{}, fmt.Errorf("no JSON object in answer: %w", ErrUnreadable)
	}
	var in verdictJSON
	if err := json.Unmarshal([]byte(obj), &in); err != nil {
		return Verdict{}, fmt.Errorf("decode answer: %w: %w", err, ErrUnreadable)
	}
	var v Verdict
	seen := map[string]bool{}
	for _, r := range in.Ratings {
		if !slices.Contains(candidates, r.Agent) || seen[r.Agent] || math.IsNaN(r.Score) {
			continue
		}
		seen[r.Agent] = true
		v.Ratings = append(v.Ratings, Rating{Agent: r.Agent, Score: clamp01(r.Score), Reason: oneSentence(r.Reason)})
	}
	var h handoffJSON
	if err := json.Unmarshal(in.Handoff, &h); err == nil && slices.Contains(routers, h.Router) {
		v.Handoff, v.HandoffReason = h.Router, oneSentence(h.Reason)
	}
	if len(v.Ratings) == 0 && v.Handoff == "" {
		return Verdict{}, fmt.Errorf("no rating of a candidate: %w", ErrUnreadable)
	}
	return v, nil
}

func clamp01(x float64) float64 { return math.Max(0, math.Min(1, x)) }

// oneSentence makes s one line of at most MaxReason characters.
func oneSentence(s string) string {
	s = strings.Join(strings.Fields(strings.ToValidUTF8(s, "")), " ")
	return truncateRunes(s, MaxReason)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// firstJSONObject returns the first balanced {...} in s, string-aware.
func firstJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	for start >= 0 {
		if end, ok := objectEnd(s, start); ok {
			return s[start : end+1], true
		}
		next := strings.IndexByte(s[start+1:], '{')
		if next < 0 {
			return "", false
		}
		start += 1 + next
	}
	return "", false
}

func objectEnd(s string, start int) (int, bool) {
	depth, inString, escaped := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}
