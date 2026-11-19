// SPDX-License-Identifier: MIT

package domain

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Candidate is an entry with its text-match score against a request.
type Candidate struct {
	Entry Entry
	Score float64
}

// BM25 parameters: the usual defaults.
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// stopwords are request words that say nothing about which agent fits.
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "you": true, "can": true, "please": true, "with": true,
	"who": true, "what": true, "how": true, "are": true, "this": true, "that": true, "from": true,
	"was": true, "has": true, "have": true, "not": true, "but": true, "any": true, "all": true,
	"our": true, "your": true, "about": true, "into": true, "need": true, "want": true, "would": true,
	"is": true, "to": true, "of": true, "in": true, "on": true, "it": true, "an": true, "as": true,
	"at": true, "be": true, "do": true, "we": true, "my": true, "me": true, "if": true, "or": true,
}

// Shortlist ranks entries against query with BM25 over each agent's name,
// display name, role and line, and returns the best limit of them. Entries
// that match nothing follow in name order, so the model call still has
// something to rate. Ties break by agent name, so the order never changes
// between runs.
func Shortlist(query string, entries []Entry, limit int) []Candidate {
	if limit <= 0 || len(entries) == 0 {
		return nil
	}
	docs := make([][]string, len(entries))
	var total int
	for i, e := range entries {
		docs[i] = documentTokens(e)
		total += len(docs[i])
	}
	avg := float64(total) / float64(len(entries))
	q := uniqueTokens(query)
	df := documentFrequencies(q, docs)

	out := make([]Candidate, len(entries))
	for i, e := range entries {
		out[i] = Candidate{Entry: e, Score: bm25(q, docs[i], df, len(entries), avg)}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Entry.Agent < out[j].Entry.Agent
	})
	return out[:min(limit, len(out))]
}

func bm25(query []string, doc []string, df map[string]int, n int, avg float64) float64 {
	if len(doc) == 0 || avg == 0 {
		return 0
	}
	tf := map[string]int{}
	for _, t := range doc {
		tf[t]++
	}
	var score float64
	for _, t := range query {
		f := float64(tf[t])
		if f == 0 {
			continue
		}
		idf := math.Log(1 + (float64(n)-float64(df[t])+0.5)/(float64(df[t])+0.5))
		score += idf * f * (bm25K1 + 1) / (f + bm25K1*(1-bm25B+bm25B*float64(len(doc))/avg))
	}
	return score
}

func documentFrequencies(query []string, docs [][]string) map[string]int {
	df := map[string]int{}
	for _, d := range docs {
		seen := map[string]bool{}
		for _, t := range d {
			seen[t] = true
		}
		for _, t := range query {
			if seen[t] {
				df[t]++
			}
		}
	}
	return df
}

// documentTokens are the words an entry can be found by. Name and role are
// counted twice: they say what the agent is, the line says what it does now.
func documentTokens(e Entry) []string {
	var tokens []string
	for _, field := range []string{e.Agent, e.DisplayName, e.Role} {
		tokens = append(tokens, tokenize(field)...)
	}
	tokens = append(tokens, tokens...)
	return append(tokens, tokenize(e.OneLine())...)
}

func uniqueTokens(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tokenize(s) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// tokenize lower-cases s, splits it on anything that is not a letter or
// digit, drops stopwords and one-letter words, and folds plurals.
func tokenize(s string) []string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := words[:0]
	for _, w := range words {
		if len([]rune(w)) < 2 || stopwords[w] {
			continue
		}
		out = append(out, stem(w))
	}
	return out
}

// stem folds a plural "s" so "logs" finds "log". Nothing fancier: the
// model call corrects what a crude match misses.
func stem(w string) string {
	if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
		return w[:len(w)-1]
	}
	return w
}
