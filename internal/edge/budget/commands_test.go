// SPDX-License-Identifier: MIT

package budget_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/budget"
	"github.com/SDK-E/agentsone/internal/host/hostenv"
)

func run(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := &hostenv.Env{Out: &out, Err: &errOut, Log: slog.New(slog.DiscardHandler), DataDir: dir}
	cmds := budget.Commands(env)
	if len(cmds) != 1 || cmds[0].Name() != "budget" {
		t.Fatalf("commands = %v", cmds)
	}
	c := cmds[0]
	c.SetArgs(args)
	c.SetOut(&out)
	c.SetErr(&errOut)
	err := c.Execute()
	return out.String(), err
}

func TestSetShowAndDefence(t *testing.T) {
	dir := t.TempDir()
	out, err := run(t, dir, "show")
	if err != nil || !strings.Contains(out, "Total budget:     $0.00") {
		t.Fatalf("show on a fresh folder: %v\n%s", err, out)
	}
	if out, err := run(t, dir, "set", "25.50"); err != nil || !strings.Contains(out, "$25.50") {
		t.Fatalf("set: %v\n%s", err, out)
	}
	if out, err := run(t, dir, "defence", "share", "20"); err != nil || !strings.Contains(out, "20%") {
		t.Fatalf("defence share: %v\n%s", err, out)
	}
	if out, err := run(t, dir, "defence", "on"); err != nil || !strings.Contains(out, "on") {
		t.Fatalf("defence on: %v\n%s", err, out)
	}
	out, err = run(t, dir, "show")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	for _, want := range []string{"$25.50", "on, 20%", "$5.10", "Left for agents:  $20.40", "Left for defence: $25.50"} {
		if !strings.Contains(out, want) {
			t.Fatalf("show is missing %q:\n%s", want, out)
		}
	}
	if out, err := run(t, dir, "defence", "off"); err != nil || !strings.Contains(out, "off") {
		t.Fatalf("defence off: %v\n%s", err, out)
	}
}

func TestBadInput(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"set", "lots"},
		{"set"},
		{"defence", "share", "120"},
		{"defence", "share", "x"},
		{"defence", "maybe"},
	} {
		if _, err := run(t, dir, args...); err == nil {
			t.Fatalf("budget %v succeeded", args)
		}
	}
}
