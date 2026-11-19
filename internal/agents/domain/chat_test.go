// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/agents/agentstest"
	"github.com/SDK-E/agentsone/internal/agents/domain"
)

func TestNewChatMessage_TextLength(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		text    string
		wantErr error
	}{
		{"one character", "x", nil},
		{"32000 multi-byte characters", strings.Repeat("é", 32000), nil},
		{"32001 characters", strings.Repeat("x", 32001), domain.ErrInvalidChat},
		{"empty", "", domain.ErrInvalidChat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.NewChatMessage("cht_1", "dev", domain.ChatHuman, "human:ada", tt.text, agentstest.At)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewChatMessage() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestChatRoleFor_FollowsTheCallerKind(t *testing.T) {
	t.Parallel()
	tests := map[string]domain.ChatRole{"human": domain.ChatHuman, "agent": domain.ChatAgent, "system": domain.ChatSystem, "host": domain.ChatSystem}
	for kind, want := range tests {
		if got := domain.ChatRoleFor(kind); got != want {
			t.Errorf("ChatRoleFor(%s) = %s, want %s", kind, got, want)
		}
	}
}

func TestUpdate_RefusesLimitsOnARootAgent(t *testing.T) {
	t.Parallel()
	root := agentstest.New("guard", agentstest.Root())

	_, err := domain.Update{Limits: &domain.Limits{CPUPercent: 1, MemoryMB: 64, Tasks: 8}}.Apply(root)

	if !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("error = %v, want %v", err, domain.ErrInvalidAgent)
	}
}

func TestUpdate_AppliesEveryField(t *testing.T) {
	t.Parallel()
	name, role, desc, runner := "Mira", "Reviewer", "Reviews.", "openai"
	kind := domain.KindChat
	limits := domain.Limits{CPUPercent: 50, MemoryMB: 256, Tasks: 32}
	u := domain.Update{DisplayName: &name, Kind: &kind, Runner: &runner, Role: &role, Description: &desc, Limits: &limits}

	got, err := u.Apply(agentstest.New("dev"))
	if err != nil {
		t.Fatal(err)
	}

	if got.DisplayName != name || got.Kind != kind || got.Runner != runner || got.Role != role || got.Description != desc || *got.Limits != limits {
		t.Fatalf("Apply() = %+v", got)
	}
	if !u.Restarts() {
		t.Fatal("kind, runner and limits edits must restart the unit")
	}
}

func TestUpdate_RefusesInvalidFields(t *testing.T) {
	t.Parallel()
	empty, badRunner, long := "", "Bad", strings.Repeat("d", 4001)
	badKind := domain.Kind("x")
	for name, u := range map[string]domain.Update{
		"empty display name": {DisplayName: &empty},
		"bad kind":           {Kind: &badKind},
		"bad runner":         {Runner: &badRunner},
		"empty role":         {Role: &empty},
		"long description":   {Description: &long},
		"bad limits":         {Limits: &domain.Limits{}},
	} {
		if _, err := u.Apply(agentstest.New("dev")); !errors.Is(err, domain.ErrInvalidAgent) {
			t.Errorf("%s: error = %v, want %v", name, err, domain.ErrInvalidAgent)
		}
	}
}

func TestFound_CreatesARootAgentAtTheTop(t *testing.T) {
	t.Parallel()
	spec := domain.HireSpec{Name: "founder", DisplayName: "Founder", Kind: domain.KindAlwaysOn, Runner: "echo", Role: "Founder", Manager: "x"}

	got, err := domain.NewChart(nil).Found(spec, "system:seed", agentstest.At)
	if err != nil {
		t.Fatal(err)
	}

	if !got.IsRoot || got.Manager != "" || got.UID != 0 || got.Limits != nil {
		t.Fatalf("founder = %+v, want root at the top", got)
	}
	if _, err := agentstest.Chart().Found(spec, "system:seed", agentstest.At); !errors.Is(err, domain.ErrNameTaken) {
		t.Fatalf("second founder error = %v, want %v", err, domain.ErrNameTaken)
	}
}
