// SPDX-License-Identifier: MIT

// Package domain holds the edge's judgement of the machine's audit chain as it arrives: whether
// each record continues the chain, repeats it, skips ahead or breaks from it.
package domain

import "time"

// GenesisHash is the prev_hash of the first record of a chain.
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// Head is the last record of a chain the edge holds.
type Head struct {
	Seq  int64
	Hash string
}

// Empty is the head of a chain with no records.
func Empty() Head { return Head{Hash: GenesisHash} }

// Link is what a record says about its place in the chain.
type Link struct {
	Seq      int64
	PrevHash string
	Hash     string
}

// Verdict is what the edge makes of an arriving record.
type Verdict int

const (
	// Append means the record continues the chain.
	Append Verdict = iota
	// Duplicate means the edge already holds this exact record (a resend); nothing to do.
	Duplicate
	// Gap means the record skips records the edge never got.
	Gap
	// Fork means the record contradicts what the edge holds (a rewritten or restarted log).
	Fork
	// Broken means the record's content does not match its own hash, or its sequence is invalid.
	Broken
)

// String names the verdict; it is also the alert kind.
func (v Verdict) String() string {
	switch v {
	case Append:
		return "append"
	case Duplicate:
		return "duplicate"
	case Gap:
		return "gap"
	case Fork:
		return "fork"
	case Broken:
		return "broken"
	}
	return "unknown"
}

// Alert reports whether the verdict is a break the human must see.
func (v Verdict) Alert() bool { return v == Gap || v == Fork || v == Broken }

// Judge decides what an arriving record is. computed is the hash of the record's content as the
// edge computes it; stored is the hash the edge holds at l.Seq, empty when it holds none.
func Judge(head Head, l Link, computed, stored string) Verdict {
	switch {
	case l.Seq < 1 || computed != l.Hash:
		return Broken
	case l.Seq <= head.Seq:
		if stored == l.Hash {
			return Duplicate
		}
		return Fork
	case l.Seq > head.Seq+1:
		return Gap
	case l.PrevHash != head.Hash:
		return Fork
	}
	return Append
}

// Alert is a break in the machine's chain, kept by the edge.
type Alert struct {
	Kind   string
	Seq    int64
	Detail string
	Time   time.Time
}
