// SPDX-License-Identifier: MIT

package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

func TestParseFreezeScope(t *testing.T) {
	valid := map[string]core.FreezeScope{
		"all":          {Kind: core.FreezeAll},
		"team:founder": {Kind: core.FreezeTeam, Target: "founder"},
		"agent:dev-2":  {Kind: core.FreezeAgent, Target: "dev-2"},
	}
	for in, want := range valid {
		got, err := core.ParseFreezeScope(in)
		if err != nil || got != want {
			t.Errorf("ParseFreezeScope(%q) = %+v %v, want %+v", in, got, err, want)
		}
		if got.String() != in {
			t.Errorf("String() = %q, want %q", got.String(), in)
		}
	}
	for _, in := range []string{"", "all:x", "team", "team:", "agent:Bob", "agent:1x", "pack:x", "agent:a:b"} {
		if _, err := core.ParseFreezeScope(in); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("ParseFreezeScope(%q) error %v, want ErrInvalid", in, err)
		}
	}
}

func TestBusMessageValidate(t *testing.T) {
	ok := core.BusMessage{From: "agent:founder", To: "channel:general", Body: "hello"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid message: %v", err)
	}
	for name, mutate := range map[string]func(*core.BusMessage){
		"from": func(m *core.BusMessage) { m.From = "" },
		"to":   func(m *core.BusMessage) { m.To = "" },
		"body": func(m *core.BusMessage) { m.Body = "" },
	} {
		m := ok
		mutate(&m)
		if err := m.Validate(); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("empty %s: %v", name, err)
		}
	}
}

func TestTaskOutcomeValidate(t *testing.T) {
	for _, s := range []string{core.TaskDone, core.TaskFailed, core.TaskCancelled} {
		if err := (core.TaskOutcome{Status: s}).Validate(); err != nil {
			t.Errorf("%s rejected: %v", s, err)
		}
	}
	for _, s := range []string{"", "open", "in-progress", "finished"} {
		if err := (core.TaskOutcome{Status: s}).Validate(); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%q accepted: %v", s, err)
		}
	}
}

func TestTaskPriority(t *testing.T) {
	cases := map[string]string{"": core.TaskPriorityNormal, "low": core.TaskPriorityLow, "high": core.TaskPriorityHigh}
	for in, want := range cases {
		if got, err := core.TaskPriority(in); err != nil || got != want {
			t.Errorf("TaskPriority(%q) = %q %v, want %q", in, got, err, want)
		}
	}
	if _, err := core.TaskPriority("urgent"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("urgent accepted: %v", err)
	}
}

func TestNewPortsUnavailable(t *testing.T) {
	p := core.NewPorts()
	ctx := context.Background()
	all := core.FreezeScope{Kind: core.FreezeAll}
	calls := map[string]func() error{
		"Send":         func() error { _, err := p.Bus.Send(ctx, core.BusMessage{}); return err },
		"Freeze":       func() error { return p.Freezer.Freeze(ctx, all) },
		"Release":      func() error { return p.Freezer.Release(ctx, all) },
		"State":        func() error { _, err := p.Freezer.State(ctx); return err },
		"Issue":        func() error { _, err := p.Tokens.Issue(ctx, core.Caller{}, nil, time.Minute); return err },
		"Verify":       func() error { _, err := p.Tokens.Verify(ctx, "tok"); return err },
		"List":         func() error { _, err := p.Templates.List(ctx); return err },
		"Get":          func() error { _, err := p.Templates.Get(ctx, "tpl"); return err },
		"GetTask":      func() error { _, err := p.Tasks.GetTask(ctx, "tsk"); return err },
		"CompleteTask": func() error { _, err := p.Tasks.CompleteTask(ctx, "tsk", core.TaskOutcome{}); return err },
		"Profiles":     func() error { _, err := p.Profiles.Profiles(ctx); return err },
		"PostChat":     func() error { _, err := p.Chat.PostChat(ctx, "dev", "hi", ""); return err },
		"Wait":         func() error { return p.Startup.Wait(ctx) },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, core.ErrUnavailable) {
			t.Errorf("%s: %v, want ErrUnavailable", name, err)
		}
	}
}
