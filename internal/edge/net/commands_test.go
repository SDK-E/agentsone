// SPDX-License-Identifier: MIT

package net_test

import (
	"bytes"
	"strings"
	"testing"

	edgenet "github.com/SDK-E/agentsone/internal/edge/net"
)

func run(t *testing.T, dataDir string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := edgenet.NewNetCommand(dataDir, &out)
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.ExecuteContext(t.Context())
	return out.String(), err
}

func TestAllowListList_ShowsTheDefaultsWithReasons(t *testing.T) {
	t.Parallel()
	got, err := run(t, t.TempDir(), "allowlist", "list")
	if err != nil {
		t.Fatalf("list error = %v", err)
	}
	for _, want := range []string{"deb.debian.org", "Debian packages", "registry.npmjs.org"} {
		if !strings.Contains(got, want) {
			t.Errorf("list output lacks %q:\n%s", want, got)
		}
	}
}

func TestAllowListAdd_AddsTheEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	got, err := run(t, dir, "allowlist", "add", "*.example.org", "--why", "team docs")
	if err != nil {
		t.Fatalf("add error = %v", err)
	}
	if !strings.Contains(got, "Added *.example.org") {
		t.Fatalf("add output = %q, want a confirmation", got)
	}

	listed, _ := run(t, dir, "allowlist", "list")
	if !strings.Contains(listed, "*.example.org") || !strings.Contains(listed, "team docs") {
		t.Fatalf("list after add lacks the entry:\n%s", listed)
	}
}

func TestAllowListRemove_RemovesTheEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, err := run(t, dir, "allowlist", "remove", "github.com"); err != nil {
		t.Fatalf("remove error = %v", err)
	}

	listed, _ := run(t, dir, "allowlist", "list")
	for line := range strings.SplitSeq(listed, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "github.com ") {
			t.Fatalf("github.com still listed:\n%s", listed)
		}
	}
}

func TestAllowList_RefusesBadInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"add a top level wildcard", []string{"allowlist", "add", "*.com"}, "invalid allow list pattern"},
		{"add twice", []string{"allowlist", "add", "github.com"}, "already present"},
		{"remove an unknown entry", []string{"allowlist", "remove", "example.org"}, "not found"},
		{"add without a pattern", []string{"allowlist", "add"}, "accepts 1 arg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := run(t, t.TempDir(), tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want one mentioning %q", err, tt.want)
			}
		})
	}
}
