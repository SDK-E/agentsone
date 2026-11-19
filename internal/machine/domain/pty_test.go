// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

func TestNewPtySpecDefaults(t *testing.T) {
	s, err := domain.NewPtySpec(nil, nil, nil, "/bin/bash")
	if err != nil {
		t.Fatal(err)
	}
	want := domain.PtySpec{Command: []string{"/bin/bash", "-l"}, Cols: 120, Rows: 32}
	if diff := cmp.Diff(want, s); diff != "" {
		t.Fatal(diff)
	}
	if s, _ := domain.NewPtySpec(nil, nil, nil, "/usr/sbin/nologin"); s.Command[0] != "/bin/bash" {
		t.Fatalf("agents without a login shell get bash: %v", s.Command)
	}
	if s, _ := domain.NewPtySpec([]string{"htop"}, nil, nil, "/bin/bash"); !cmp.Equal(s.Command, []string{"htop"}) {
		t.Fatalf("command %v", s.Command)
	}
}

func TestNewPtySpecLimits(t *testing.T) {
	n := func(v int) *int { return &v }
	for _, tc := range []struct {
		cols, rows *int
		ok         bool
	}{
		{n(10), n(5), true}, {n(1000), n(500), true},
		{n(9), nil, false}, {n(1001), nil, false}, {nil, n(4), false}, {nil, n(501), false},
	} {
		_, err := domain.NewPtySpec(nil, tc.cols, tc.rows, "/bin/sh")
		if tc.ok != (err == nil) {
			t.Errorf("cols %v rows %v: err %v", tc.cols, tc.rows, err)
		}
	}
	if _, err := domain.NewPtySpec([]string{""}, nil, nil, "/bin/sh"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("empty program: %v", err)
	}
}

func TestPtyOwnershipAndLimits(t *testing.T) {
	mine := domain.PtySession{ID: "pty_1", Owner: custom.Label()}
	theirs := domain.PtySession{ID: "pty_2", Owner: "agent:coder"}
	if !mine.VisibleTo(custom) || theirs.VisibleTo(custom) || !theirs.VisibleTo(human) || !theirs.VisibleTo(rootAg) {
		t.Fatal("visibility rules")
	}
	open := make([]domain.PtySession, domain.MaxPtyPerActor)
	for i := range open {
		open[i] = domain.PtySession{Owner: custom.Label()}
	}
	if err := domain.CanOpenPty(custom, open); !errors.Is(err, domain.ErrTooMany) {
		t.Fatalf("per-actor cap: %v", err)
	}
	if err := domain.CanOpenPty(human, open); err != nil {
		t.Fatalf("other actor under cap: %v", err)
	}
	all := make([]domain.PtySession, domain.MaxPtyTotal)
	if err := domain.CanOpenPty(human, all); !errors.Is(err, domain.ErrTooMany) {
		t.Fatalf("machine cap: %v", err)
	}
	exited := []domain.PtySession{{Owner: custom.Label(), Exited: true}}
	if err := domain.CanOpenPty(custom, append(open[1:], exited...)); err != nil {
		t.Fatalf("exited sessions do not count: %v", err)
	}
}

func TestPtyIdle(t *testing.T) {
	start := time.Unix(1000, 0)
	s := domain.PtySession{LastActive: start}
	if s.Idle(start.Add(domain.PtyIdleTimeout)) {
		t.Fatal("idle at exactly the timeout")
	}
	if !s.Idle(start.Add(domain.PtyIdleTimeout + time.Second)) {
		t.Fatal("not idle after the timeout")
	}
	s.Connected = true
	if s.Idle(start.Add(time.Hour)) {
		t.Fatal("a connected terminal is never idle")
	}
}

func TestParsePtyControl(t *testing.T) {
	c, err := domain.ParsePtyControl([]byte(`{"type":"resize","cols":100,"rows":40}`))
	if err != nil || c != (domain.PtyResize{Cols: 100, Rows: 40}) {
		t.Fatalf("resize %+v %v", c, err)
	}
	for _, bad := range []string{`{"type":"resize","cols":5,"rows":40}`, `{"type":"eject"}`, `not json`} {
		if _, err := domain.ParsePtyControl([]byte(bad)); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if got := string(domain.PtyExitMessage(3)); got != `{"type":"exit","code":3}` {
		t.Fatalf("exit message %s", got)
	}
}
