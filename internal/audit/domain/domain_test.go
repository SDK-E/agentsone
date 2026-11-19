// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/audit/domain"
)

func TestNewActionAcceptsDottedLowercaseWords(t *testing.T) {
	for _, s := range []string{"agent.hire", "power.shutdown", "net.allow-list.add", "a1.b2"} {
		a, err := domain.NewAction(s)
		if err != nil {
			t.Errorf("%q rejected: %v", s, err)
		}
		if a.String() != s {
			t.Errorf("%q became %q", s, a)
		}
	}
}

func TestNewActionRejectsOtherShapes(t *testing.T) {
	bad := []string{"", "hire", "Agent.hire", "agent.", ".hire", "agent..hire", "agent.Hire", "1agent.hire", "agent.hire!", "a." + strings.Repeat("b", 64)}
	for _, s := range bad {
		if _, err := domain.NewAction(s); !errors.Is(err, domain.ErrInvalidAction) {
			t.Errorf("%q: got %v, want ErrInvalidAction", s, err)
		}
	}
}

func TestParseActorRef(t *testing.T) {
	a, err := domain.ParseActorRef("agent:founder")
	if err != nil || a.Kind != domain.ActorAgent || a.Name != "founder" {
		t.Fatalf("got %+v, %v", a, err)
	}
	for _, s := range []string{"", "founder", "robot:x", "agent:"} {
		if _, err := domain.ParseActorRef(s); !errors.Is(err, domain.ErrInvalidActor) {
			t.Errorf("%q: got %v, want ErrInvalidActor", s, err)
		}
	}
}

func TestParseTargetRef(t *testing.T) {
	tg, err := domain.ParseTargetRef("plugin:small-seed")
	if err != nil || tg.Kind != "plugin" || tg.ID != "small-seed" {
		t.Fatalf("got %+v, %v", tg, err)
	}
	if tg, err := domain.ParseTargetRef("machine"); err != nil || tg.Kind != "machine" || tg.ID != "machine" {
		t.Fatalf("bare kind: got %+v, %v", tg, err)
	}
	if _, err := domain.ParseTargetRef(""); !errors.Is(err, domain.ErrInvalidTarget) {
		t.Fatalf("empty target: %v", err)
	}
}

func TestRecordFollows(t *testing.T) {
	first := domain.Record{Seq: 1, PrevHash: domain.GenesisHash, Hash: "aa"}
	if !first.Follows(domain.Record{}, false) {
		t.Fatal("first record does not start the chain")
	}
	second := domain.Record{Seq: 2, PrevHash: "aa"}
	if !second.Follows(first, true) {
		t.Fatal("second record does not follow the first")
	}
	if (domain.Record{Seq: 3, PrevHash: "aa"}).Follows(first, true) {
		t.Fatal("gap accepted")
	}
	if (domain.Record{Seq: 2, PrevHash: "bb"}).Follows(first, true) {
		t.Fatal("wrong link accepted")
	}
}
