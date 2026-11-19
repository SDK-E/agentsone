// SPDX-License-Identifier: MIT

package domain_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/audit/domain"
)

func TestJudge(t *testing.T) {
	t.Parallel()
	head := domain.Head{Seq: 2, Hash: "h2"}
	for _, tc := range []struct {
		name     string
		head     domain.Head
		link     domain.Link
		computed string
		stored   string
		want     domain.Verdict
	}{
		{"first record on the genesis hash", domain.Empty(), domain.Link{Seq: 1, PrevHash: domain.GenesisHash, Hash: "h1"}, "h1", "", domain.Append},
		{"next record on the head", head, domain.Link{Seq: 3, PrevHash: "h2", Hash: "h3"}, "h3", "", domain.Append},
		{"content that does not match its hash", head, domain.Link{Seq: 3, PrevHash: "h2", Hash: "h3"}, "other", "", domain.Broken},
		{"a record sent again", head, domain.Link{Seq: 2, PrevHash: "h1", Hash: "h2"}, "h2", "h2", domain.Duplicate},
		{"a rewritten old record", head, domain.Link{Seq: 2, PrevHash: "h1", Hash: "x2"}, "x2", "h2", domain.Fork},
		{"a next record linking elsewhere", head, domain.Link{Seq: 3, PrevHash: "x2", Hash: "h3"}, "h3", "", domain.Fork},
		{"a record past a hole", head, domain.Link{Seq: 5, PrevHash: "h4", Hash: "h5"}, "h5", "", domain.Gap},
		{"a restarted log", head, domain.Link{Seq: 1, PrevHash: domain.GenesisHash, Hash: "n1"}, "n1", "h1", domain.Fork},
		{"a sequence below one", head, domain.Link{Seq: 0, PrevHash: domain.GenesisHash, Hash: "h0"}, "h0", "", domain.Broken},
	} {
		if got := domain.Judge(tc.head, tc.link, tc.computed, tc.stored); got != tc.want {
			t.Errorf("%s: Judge = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestVerdict_NamesEachAlertKind(t *testing.T) {
	t.Parallel()
	for v, want := range map[domain.Verdict]string{
		domain.Append: "append", domain.Duplicate: "duplicate", domain.Gap: "gap",
		domain.Fork: "fork", domain.Broken: "broken", domain.Verdict(99): "unknown",
	} {
		if got := v.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", v, got, want)
		}
	}
}

func TestVerdict_OnlyBreaksRaiseAlerts(t *testing.T) {
	t.Parallel()
	for v, want := range map[domain.Verdict]bool{
		domain.Append: false, domain.Duplicate: false, domain.Gap: true, domain.Fork: true, domain.Broken: true,
	} {
		if got := v.Alert(); got != want {
			t.Errorf("%v.Alert() = %v, want %v", v, got, want)
		}
	}
}

func TestEmpty_IsTheGenesisHead(t *testing.T) {
	t.Parallel()
	if h := domain.Empty(); h.Seq != 0 || h.Hash != domain.GenesisHash {
		t.Fatalf("Empty = %+v", h)
	}
}
